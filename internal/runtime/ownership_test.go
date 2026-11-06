package runtime

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func residualProcess(t *testing.T) (*Supervisor, domain.Profile, string, <-chan error) {
	t.Helper()
	dir := t.TempDir()
	co, err := core.Bundled(context.Background(), filepath.Join("..", "..", "resources"), dir)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	path := filepath.Join(dir, "runtime", p.ID, "active-"+domain.NewID()+".toml")
	if err = storage.AtomicWrite(path, []byte("serverAddr = '127.0.0.1'\nserverPort = 1\nloginFailExit = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(co.Path, "-c", path)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { defer close(done); done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	sup := New(dir, &storage.MemorySecrets{}, "")
	t.Cleanup(func() { _ = sup.Close() })
	if err = sup.writeOwner(p, co, cmd.Process.Pid, path); err != nil {
		t.Fatal(err)
	}
	return sup, p, path, done
}

func TestResidualReviewStopsOnlyVerifiedProcess(t *testing.T) {
	s, p, path, done := residualProcess(t)
	unrelated := filepath.Join(filepath.Dir(path), "unrelated.toml")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := s.InspectResidual(context.Background(), p.ID)
	if err != nil || review.Status != "verified" {
		t.Fatalf("review: %+v, %v", review, err)
	}
	if err = s.ResolveResidual(context.Background(), review, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("process did not stop")
	}
	for _, name := range []string{path, s.ownerPath(p.ID)} {
		if _, err = os.Stat(name); !os.IsNotExist(err) {
			t.Fatal("residual remained", name, err)
		}
	}
	if _, err = os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file removed", err)
	}
}
func TestResidualReviewRejectsChangedRecordAndForeignPID(t *testing.T) {
	s, p, _, _ := residualProcess(t)
	ctx := context.Background()
	review, err := s.InspectResidual(ctx, p.ID)
	if err != nil || review.Status != "verified" {
		t.Fatal(review, err)
	}
	b, err := os.ReadFile(s.ownerPath(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.AtomicWrite(s.ownerPath(p.ID), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveResidual(ctx, review, false); err == nil {
		t.Fatal("changed review accepted")
	}
	current, err := s.InspectResidual(ctx, p.ID)
	if err != nil || current.Status != "verified" {
		t.Fatal("live process was affected", current, err)
	}
	// Same binary/config but a PID belonging to the checker must never be signalled.
	co := domain.Core{Path: current.Binary}
	if err = s.writeOwner(p, co, os.Getpid(), current.Config); err != nil {
		t.Fatal(err)
	}
	foreign, err := s.InspectResidual(ctx, p.ID)
	if err != nil || foreign.Status != "unverified" {
		t.Fatal(foreign, err)
	}
	if err = s.ResolveResidual(ctx, foreign, true); err == nil {
		t.Fatal("foreign PID accepted")
	}
}
func TestResidualReviewRejectsManagedProcess(t *testing.T) {
	s, p, _, _ := residualProcess(t)
	e := s.get(p.ID)
	s.mu.Lock()
	e.cmd = &exec.Cmd{}
	s.mu.Unlock()
	review, err := s.InspectResidual(context.Background(), p.ID)
	s.mu.Lock()
	e.cmd = nil
	s.mu.Unlock()
	if err != nil || review.Status != "managed" {
		t.Fatal(review, err)
	}
	if err = s.ResolveResidual(context.Background(), review, false); err == nil {
		t.Fatal("managed process accepted")
	}
}
func TestRestartFailuresUseRollingWindow(t *testing.T) {
	var e entry
	now := time.Now()
	for i := range 6 {
		got := e.recordFailure(now.Add(time.Duration(i) * time.Minute))
		want := i + 1
		if want > 5 {
			want = 5
		}
		if got != want {
			t.Fatalf("minute %d: %d != %d", i, got, want)
		}
	}
	if got := e.recordFailure(now.Add(11 * time.Minute)); got != 1 {
		t.Fatal("expired failures counted", got)
	}
}

func TestStaleRestartCannotCountOrOverwriteStoppedGeneration(t *testing.T) {
	s := New(t.TempDir(), &storage.MemorySecrets{}, "")
	e := s.get(domain.NewID())
	e.snapshot.Generation = 2
	e.snapshot.Error = "current state"
	e.desired = true
	for range 4 {
		e.recordFailure(time.Now())
	}
	s.restart(e, 1)
	if len(e.failureTimes) != 4 || e.snapshot.Error != "current state" {
		t.Fatal("old generation changed current state")
	}
	e.desired = false
	s.restart(e, 2)
	if len(e.failureTimes) != 4 || e.snapshot.Error != "current state" {
		t.Fatal("stopped connection counted failure")
	}
}

func TestCoreRemovalProtectsPendingRestartAndResidualProcess(t *testing.T) {
	s, p, _, _ := residualProcess(t)
	review, err := s.InspectResidual(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err = s.WithUnusedCore(review.Binary, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("live residual core accepted")
	}
	e := s.get(p.ID)
	s.mu.Lock()
	e.core.Path = review.Binary
	e.desired = true
	s.mu.Unlock()
	if err = s.WithUnusedCore(review.Binary, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("pending restart core accepted")
	}
	s.mu.Lock()
	e.desired = false
	s.mu.Unlock()
	if err = s.ResolveResidual(context.Background(), review, false); err != nil {
		t.Fatal(err)
	}
	if err = s.WithUnusedCore(review.Binary, func() error { called = true; return nil }); err != nil || !called {
		t.Fatal("unused core refused", err)
	}
}
