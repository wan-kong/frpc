package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func commandProfile(t *testing.T, c *Controller) domain.Profile {
	t.Helper()
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	if err := c.Store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	p, _ = c.Store.Snapshot().Profile(p.ID)
	return p
}
func metaFor(p domain.Profile) CommandMeta {
	return CommandMeta{OperationID: domain.NewID(), ProfileID: p.ID, ExpectedRevision: p.Revision}
}
func requireCommandCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	info, ok := domain.ErrorInfo(err)
	if !ok || info.Code != want {
		t.Fatalf("error: %v, want %s", err, want)
	}
}
func TestCommandDeleteReplaysWithoutRepeatingOrAliasingSnapshot(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	cmd := Command{Meta: metaFor(p), Action: DeleteProfile{}}
	first, err := c.Execute(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Persisted || first.Replayed || len(first.State.Trash) != 1 {
		t.Fatal("delete result", first)
	}
	first.State.Trash[0].Profile.Name = "changed response"
	revision := c.Store.Revision()
	again, err := c.Execute(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || c.Store.Revision() != revision || len(again.State.Trash) != 1 || again.State.Trash[0].Profile.Name != p.Name {
		t.Fatal("delete repeated or snapshot aliased")
	}
	restore := Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: p.ID, ExpectedRevision: 1}, Action: RestoreProfile{}}
	restored, err := c.Execute(context.Background(), restore)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := restored.State.Profile(p.ID)
	if current.Revision != 2 || current.AutoConnect {
		t.Fatal("restore lost revision or reconnected")
	}
	if _, err = c.Execute(context.Background(), restore); err != nil {
		t.Fatal("restore replay failed", err)
	}
}
func TestStaleDeleteCannotStopOrDeleteNewerConfiguration(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	cmd := Command{Meta: metaFor(p), Action: DeleteProfile{}}
	newer := p
	newer.Name = "newer configuration"
	if err := c.Store.SaveProfile(newer, p.Revision); err != nil {
		t.Fatal(err)
	}
	_, err := c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Conflict)
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatal("conflict cause lost")
	}
	state := c.Store.Snapshot()
	current, _ := state.Profile(p.ID)
	if current.Name != newer.Name || len(state.Trash) != 0 || len(c.Runtime.Snapshot()) != 0 {
		t.Fatal("stale command caused side effects")
	}
}

type countedSecrets struct {
	storage.Secrets
	sets atomic.Int64
}

func (s *countedSecrets) Set(id string, value []byte) error {
	s.sets.Add(1)
	return s.Secrets.Set(id, value)
}
func TestConcurrentImportCommandRunsOnceAndReturnsStableIdentity(t *testing.T) {
	c := testController(t)
	if err := c.Bootstrap(context.Background(), filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	secrets := &countedSecrets{Secrets: c.Secrets}
	c.Secrets = secrets
	cmd := Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: domain.NewID()}, Action: ImportTOML{Data: []byte("serverAddr='127.0.0.1'\nserverPort=7000\nauth.token='import-command-secret'\n"), Name: "imported"}}
	var wg sync.WaitGroup
	var originals atomic.Int64
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := c.Execute(context.Background(), cmd)
			if err != nil {
				t.Error(err)
				return
			}
			if result.ProfileID != cmd.Meta.ProfileID {
				t.Error("unstable import identity")
			}
			if !result.Replayed {
				originals.Add(1)
			}
		}()
	}
	wg.Wait()
	if originals.Load() != 1 || len(c.Store.Snapshot().Profiles) != 1 || secrets.sets.Load() != 1 {
		t.Fatal("import repeated", originals.Load(), secrets.sets.Load())
	}
}
func TestSaveCommandIncludesTransientMetadataAndDoesNotMutateInput(t *testing.T) {
	c := testController(t)
	if err := c.Bootstrap(context.Background(), filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = c.Store.Snapshot().Cores[0].ID
	p.Metadata = map[string]string{"team": "transient-secret"}
	x := domain.NewTunnel()
	x.Type = "stcp"
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	action := CreateProfile{Profile: p, Values: map[string]string{"secret/" + x.ID: "private-command-secret"}}
	cmd := Command{Meta: metaFor(p), Action: action}
	result, err := c.Execute(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := result.State.Profile(p.ID)
	if p.Tunnels[0].SecretRef != "" || p.Metadata["team"] != "transient-secret" || saved.Tunnels[0].SecretRef == "" || saved.MetadataRefs["team"] == "" {
		t.Fatal("input mutated or metadata omitted")
	}
	if _, err = c.Execute(context.Background(), cmd); err != nil {
		t.Fatal("immutable replay failed", err)
	}
	p.Metadata["team"] = "different-secret"
	changed := cmd
	changed.Action = CreateProfile{Profile: p, Values: action.Values}
	_, err = c.Execute(context.Background(), changed)
	requireCommandCode(t, err, domain.Conflict)
}
func TestOperationKindAndFailedOutcomeAreBoundToReceipt(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	cmd := Command{Meta: metaFor(p), Action: StartProfile{}}
	_, err := c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.BinaryMissing)
	again, err := c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.BinaryMissing)
	if !again.Replayed {
		t.Fatal("failed intent executed again")
	}
	cmd.Action = DeleteProfile{}
	_, err = c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Conflict)
	var nilAction *StartProfile
	_, err = c.Execute(context.Background(), Command{Meta: metaFor(p), Action: nilAction})
	requireCommandCode(t, err, domain.Validation)
}
func TestCommandScopeCancellationReleasesPartiallyAcquiredLocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := testController(t)
		a, b := domain.NewID(), domain.NewID()
		if a > b {
			a, b = b, a
		}
		release, err := c.acquireCommandScopes(context.Background(), map[string]uint64{b: 1})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			unlock, err := c.acquireCommandScopes(ctx, map[string]uint64{a: 1, b: 1})
			if unlock != nil {
				unlock()
			}
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err = <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		unlock, err := c.acquireCommandScopes(context.Background(), map[string]uint64{a: 1})
		if err != nil {
			t.Fatal("partial lock leaked", err)
		}
		unlock()
		release()
	})
}
func TestDifferentProfileCommandDoesNotWaitForOtherProfileGate(t *testing.T) {
	c := testController(t)
	a, b := commandProfile(t, c), commandProfile(t, c)
	release, err := c.acquireCommandScopes(context.Background(), map[string]uint64{a.ID: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() {
		_, err := c.Execute(context.Background(), Command{Meta: metaFor(b), Action: StopProfile{}})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated profile was serialized")
	}
}
func TestOldReceiptRemainsIdempotentBeyondThousandCommands(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	first := Command{Meta: metaFor(p), Action: StopProfile{}}
	if _, err := c.Execute(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	for range 1002 {
		if _, err := c.Execute(context.Background(), Command{Meta: metaFor(p), Action: StopProfile{}}); err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := c.Execute(context.Background(), first)
	if err != nil || !replayed.Replayed {
		t.Fatal("old receipt was discarded", err)
	}
}

func TestSaveAndConnectFailureReceiptDoesNotRepeatCommittedSave(t *testing.T) {
	c := testController(t)
	if err := c.Bootstrap(context.Background(), filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = c.Store.Snapshot().Cores[0].ID
	cmd := Command{Meta: metaFor(p), Action: CreateProfile{Profile: p, RunAfterSave: "connect"}}
	first, err := c.Execute(context.Background(), cmd)
	if err == nil || !first.Persisted {
		t.Fatal("fixture did not save before its invalid application executable failed", err)
	}
	stored, _ := first.State.Profile(p.ID)
	again, err := c.Execute(context.Background(), cmd)
	if err == nil || !again.Replayed || !again.Persisted {
		t.Fatal("partial result lost")
	}
	current, _ := again.State.Profile(p.ID)
	if current.Revision != stored.Revision {
		t.Fatal("retry repeated the committed save")
	}
}
func TestEntryCommandChecksEverySourceAndDestinationBeforeMutation(t *testing.T) {
	c := testController(t)
	a, b := commandProfile(t, c), commandProfile(t, c)
	x := domain.NewTunnel()
	x.LocalPort = 8080
	a.Tunnels = []domain.Tunnel{x}
	if err := c.Store.SaveProfile(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	a, _ = c.Store.Snapshot().Profile(a.ID)
	cmd, err := EntryCommand(c.Store.Snapshot(), []string{x.ID}, b.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	b.Name = "new target version"
	if err = c.Store.SaveProfile(b, b.Revision); err != nil {
		t.Fatal(err)
	}
	_, err = c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Conflict)
	incomplete := Command{Meta: metaFor(a), Action: TransformEntries{IDs: []string{x.ID}, Destination: b.ID}}
	_, err = c.Execute(context.Background(), incomplete)
	requireCommandCode(t, err, domain.Validation)
	source, _ := c.Store.Snapshot().Profile(a.ID)
	target, _ := c.Store.Snapshot().Profile(b.ID)
	if len(source.Tunnels) != 1 || len(target.Tunnels) != 0 {
		t.Fatal("stale or unguarded transform mutated state")
	}
}
func TestExecuteManyRetainsEachChildIdentityOnReplay(t *testing.T) {
	c := testController(t)
	a, b := commandProfile(t, c), commandProfile(t, c)
	commands := []Command{{Meta: metaFor(a), Action: StopProfile{}}, {Meta: metaFor(b), Action: StopProfile{}}}
	results, err := c.ExecuteMany(context.Background(), commands)
	if err != nil || len(results) != 2 {
		t.Fatal(err)
	}
	results, err = c.ExecuteMany(context.Background(), commands)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if !result.Replayed {
			t.Fatal("batch child was repeated")
		}
	}
}

type cancelReadSecrets struct {
	storage.Secrets
	cancel context.CancelFunc
}

func (s cancelReadSecrets) Get(id string) ([]byte, error) { s.cancel(); return s.Secrets.Get(id) }
func TestCanceledPreparationIsNotValidationFailureAndCanRetryNewIntent(t *testing.T) {
	c := testController(t)
	if err := c.Bootstrap(context.Background(), filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = c.Store.Snapshot().Cores[0].ID
	var err error
	p.TokenRef, err = storage.PutSecret(c.Secrets, "cancel-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Runtime.Secrets = cancelReadSecrets{c.Secrets, cancel}
	cmd := Command{Meta: metaFor(p), Action: CreateProfile{Profile: p}}
	result, err := c.Execute(ctx, cmd)
	requireCommandCode(t, err, domain.Canceled)
	if result.Persisted || len(c.Store.Snapshot().Profiles) != 0 {
		t.Fatal("canceled preparation committed")
	}
	result, err = c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Canceled)
	if !result.Replayed {
		t.Fatal("failed intent was rerun")
	}
	cmd.Meta.OperationID = domain.NewID()
	if _, err = c.Execute(context.Background(), cmd); err != nil {
		t.Fatal("new intent could not retry", err)
	}
}
