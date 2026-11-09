package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
)

func TestQuickTogglesMergeAndRemainExplicitlyUnapplied(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	first, second := domain.NewTunnel(), domain.NewTunnel()
	first.Name = "first"
	second.Name = "second"
	first.LocalPort = 8080
	second.LocalPort = 8081
	p.Tunnels = []domain.Tunnel{first, second}
	if err := a.Controller.Save(context.Background(), p, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	p, _ = a.State.Profile(p.ID)
	a.QueueTunnelToggle(p.ID, first.ID, false, false)
	if !a.HasUnsavedChanges() || !a.toggleWaiting {
		t.Fatal("pending intent not retained")
	}
	for _, r := range a.rows() {
		if r.Status != "待应用" {
			t.Fatal("pending toggle looked applied", r.Status)
		}
	}
	a.QueueTunnelToggle(p.ID, first.ID, false, true)
	a.QueueTunnelToggle(p.ID, second.ID, false, false)
	drain(t)
	saved, _ := a.State.Profile(p.ID)
	if saved.Revision != p.Revision+1 || !saved.Tunnels[0].Enabled || saved.Tunnels[1].Enabled || a.HasUnsavedChanges() {
		t.Fatal("toggles were not merged", saved.Revision, a.Notice)
	}
}
func TestToggleFailureRestoresPersistedIntent(t *testing.T) {
	a := newTestApp(t)
	drain := queuedUI(a)
	p := savedProfile(t, a)
	x := domain.NewTunnel()
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	if err := a.Controller.Save(context.Background(), p, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	a.QueueTunnelToggle(p.ID, x.ID, false, false)
	// Another save commits before this debounced command; stale intent must not overwrite it.
	current, _ := a.Controller.Store.Snapshot().Profile(p.ID)
	current.Name = "concurrent save"
	if err := a.Controller.Save(context.Background(), current, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	drain(t)
	saved, _ := a.State.Profile(p.ID)
	if !saved.Tunnels[0].Enabled || saved.Name != "concurrent save" || a.HasUnsavedChanges() || a.Notice == "" {
		t.Fatal("failed optimistic edit leaked into visible state", a.Notice)
	}
}
func TestLogSearchDebouncesAndCachesStableFrames(t *testing.T) {
	a := newTestApp(t)
	queue := make(chan func(), 4)
	a.dispatch = func(fn func()) { queue <- fn }
	a.Logs = []logging.Line{{Seq: 1, Level: "info", Text: "连接成功"}, {Seq: 2, Level: "error", Text: "认证失败"}}
	initial := a.filteredLogs()
	if len(initial) != 2 {
		t.Fatal(initial)
	}
	if next := a.filteredLogs(); &next[0] != &initial[0] {
		t.Fatal("unchanged frame filtered again")
	}
	a.LogSearch = "success"
	a.queueLogSearch()
	a.LogSearch = "失败"
	a.queueLogSearch()
	if len(a.filteredLogs()) != 2 {
		t.Fatal("search applied before debounce")
	}
	select {
	case fn := <-queue:
		fn()
	case <-time.After(time.Second):
		t.Fatal("search completion missing")
	}
	result := a.filteredLogs()
	if len(result) != 1 || !strings.Contains(result[0].Text, "失败") {
		t.Fatal(result)
	}
	a.LogSearch = "成功"
	a.queueLogSearch()
	a.CancelPendingUI()
	if lines := a.filteredLogs(); len(lines) != 1 || !strings.Contains(lines[0].Text, "成功") {
		t.Fatal("canceled quit left stale search", lines)
	}
}
func TestQuitCancelsQueuedToggle(t *testing.T) {
	a := newTestApp(t)
	_ = queuedUI(a)
	p := savedProfile(t, a)
	x := domain.NewTunnel()
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	if err := a.Controller.Save(context.Background(), p, nil, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	revision := a.Controller.Store.Revision()
	a.QueueTunnelToggle(p.ID, x.ID, false, false)
	a.CancelPendingUI()
	current, _ := a.State.Profile(p.ID)
	if a.HasUnsavedChanges() || a.toggleWaiting || a.Busy || !current.Tunnels[0].Enabled || a.Controller.Store.Revision() != revision {
		t.Fatal("quit persisted pending edits")
	}
}
