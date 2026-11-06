package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func testController(t *testing.T) *Controller {
	t.Helper()
	s, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(s, &storage.MemorySecrets{}, "/unused")
}
func TestBackupWrongPasswordAndMerge(t *testing.T) {
	c := testController(t)
	p := domain.NewProfile()
	p.Name = "原配置"
	p.ServerAddr = "example.net"
	p.AutoConnect = true
	p.TokenRef, _ = storage.PutSecret(c.Secrets, "top-secret-token")
	if err := c.Store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	blob, err := c.Backup("long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "top-secret-token") {
		t.Fatal("backup leaked secret")
	}
	if err = c.RestoreBackup(blob, "wrong-password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if len(c.Store.Snapshot().Profiles) != 1 {
		t.Fatal("failed restore mutated state")
	}
	if err = c.RestoreBackup(blob, "long-test-password"); err != nil {
		t.Fatal(err)
	}
	state := c.Store.Snapshot()
	if len(state.Profiles) != 2 || state.Profiles[1].ID == p.ID || state.Profiles[1].AutoConnect {
		t.Fatal(state)
	}
	token, err := storage.Secret(c.Secrets, state.Profiles[1].TokenRef)
	if err != nil || token != "top-secret-token" {
		t.Fatal(token, err)
	}
}
func TestResumeConsumedOnceAndNormalStartup(t *testing.T) {
	c := testController(t)
	id := domain.NewID()
	if err := c.WriteResume([]string{id, "../escape"}); err != nil {
		t.Fatal(err)
	}
	ids := c.ConsumeResume()
	if len(ids) != 1 || ids[0] != id {
		t.Fatal(ids)
	}
	if len(c.ConsumeResume()) != 0 {
		t.Fatal("resume consumed twice")
	}
}

func TestRestoreKeepsMissingCoreRequirement(t *testing.T) {
	c := testController(t)
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	p.CoreID = "official-0.70.0-arm64"
	p.CoreVersion = "0.70.0"
	if err := c.Store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	blob, err := c.Backup("long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	target := testController(t)
	if err = target.RestoreBackup(blob, "long-test-password"); err != nil {
		t.Fatal(err)
	}
	restored := target.Store.Snapshot().Profiles[0]
	if restored.CoreID != p.CoreID || restored.CoreVersion != p.CoreVersion {
		t.Fatal("missing pinned core was replaced")
	}
	if _, err = target.Resolve(restored); err == nil {
		t.Fatal("missing kernel accepted")
	}
}
func TestIntegrationSaveChecksRevisionAndCredentialPersistence(t *testing.T) {
	c := testController(t)
	if err := c.Bootstrap(context.Background(), filepath.Join("..", "..", "resources")); err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = c.Store.Snapshot().Cores[0].ID
	op := domain.NewID()
	if err := c.Save(context.Background(), p, map[string]string{"token": "saved-secret"}, op); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(context.Background(), p, map[string]string{"token": "saved-secret"}, op); err != nil {
		t.Fatal("identical replay failed", err)
	}
	if err := c.Save(context.Background(), p, map[string]string{"token": "different-secret"}, op); err == nil {
		t.Fatal("operationID accepted different credentials")
	} else if info, ok := domain.ErrorInfo(err); !ok || info.Code != domain.Conflict {
		t.Fatal("operationID mismatch was not classified", err)
	}
	saved, _ := c.Store.Snapshot().Profile(p.ID)
	value, err := storage.Secret(c.Secrets, saved.TokenRef)
	if err != nil || value != "saved-secret" {
		t.Fatal(value, err)
	}
	if err = c.Save(context.Background(), p, nil, domain.NewID()); err == nil {
		t.Fatal("stale revision accepted")
	}
	saved, _ = c.Store.Snapshot().Profile(p.ID)
	saved.Name = "renamed"
	saved.Metadata = map[string]string{"meta_token": "metadata-sensitive"}
	if err = c.Save(context.Background(), saved, map[string]string{"token": "saved-secret"}, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	next, _ := c.Store.Snapshot().Profile(p.ID)
	if next.TokenRef != saved.TokenRef {
		t.Fatal("unchanged credential reference changed")
	}
	bytes, err := os.ReadFile(filepath.Join(c.Store.Dir, storage.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "metadata-sensitive") {
		t.Fatal("metadata persisted in plaintext")
	}
}

func TestRestoreRejectsExternalRawBeforeMutation(t *testing.T) {
	c := testController(t)
	p := domain.NewProfile()
	p.Mode = "raw"
	p.RawRef = domain.NewID()
	if err := c.Vault.Write(p.RawRef, []byte("includes=['/private/local.toml']\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	blob, err := c.Backup("long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	before := c.Store.Snapshot().Revision
	if err = c.RestoreBackup(blob, "long-test-password"); err == nil {
		t.Fatal("unsafe raw backup restored")
	}
	if c.Store.Snapshot().Revision != before {
		t.Fatal("failed restore changed state")
	}
}
