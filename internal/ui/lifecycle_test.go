package ui

import (
	"context"
	"testing"
	"time"

	u "github.com/egoist/mygo/ui"
)

func TestDestroyedViewCancelsWorkAndRejectsAlreadyQueuedResults(t *testing.T) {
	a := newTestApp(t)
	queue := make(chan func(), 4)
	a.dispatch = func(fn func()) { queue <- fn }
	called := false
	a.Do("pending command", func(context.Context) error { return nil }, func() { called = true; a.ProfileOpen = true })
	var completion func()
	select {
	case completion = <-queue:
	case <-time.After(time.Second):
		t.Fatal("completion not queued")
	}
	a.Dispose()
	a.Notice = "new lifecycle"
	completion()
	if called || a.ProfileOpen || a.Busy || a.Notice != "new lifecycle" {
		t.Fatal("retired completion wrote to disposed view")
	}
	// A worker active at destruction must receive cancellation as well.
	b := newTestApp(t)
	b.dispatch = func(fn func()) { queue <- fn }
	started, stopped := make(chan struct{}), make(chan struct{})
	b.CoreTask("core work", func(ctx context.Context) error { close(started); <-ctx.Done(); close(stopped); return ctx.Err() }, func() { t.Error("retired core callback ran") })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("core job never started")
	}
	b.Dispose()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("core job was not canceled")
	}
	select {
	case fn := <-queue:
		fn()
	case <-time.After(time.Second):
		t.Fatal("core completion not queued")
	}
	if b.CoreBusy || b.CoreCancel != nil {
		t.Fatal("disposed core task still active")
	}
	executed := false
	b.Do("late command", func(context.Context) error { executed = true; return nil }, nil)
	b.CoreTask("late core", func(context.Context) error { executed = true; return nil }, nil)
	if executed || b.Busy || b.CoreBusy {
		t.Fatal("destroyed view accepted new work")
	}
}

func TestModalFocusReturnsAndEscapeClearsSearchAfterClosing(t *testing.T) {
	a := newTestApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	a.Search = "keep while editing"
	tt.Frame()
	if err := tt.Click("添加连接"); err != nil {
		t.Fatal(err)
	}
	if active, _ := tt.TextInputActive(); !active {
		t.Fatal("editor did not focus a text field")
	}
	for range 30 {
		tt.Key(0, u.KeyTab)
		if tt.Focused("添加连接") || tt.Focused("添加连接") {
			t.Fatal("Tab escaped the editor")
		}
	}
	tt.Key(0, u.KeyEscape)
	if a.ProfileOpen || a.Search != "keep while editing" || !tt.Focused("添加连接") {
		t.Fatal("modal close lost opener focus or cleared search too early")
	}
	tt.Key(0, u.KeyEscape)
	if a.Search != "" {
		t.Fatal("Escape did not clear search after modal closed")
	}
}

func TestChineseCompositionAndNestedDismissalKeepDraft(t *testing.T) {
	a := newTestApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	if err := tt.Click("添加连接"); err != nil {
		t.Fatal(err)
	}
	tt.Command("selectAll")
	tt.Compose("办公网络", 4)
	tt.Key(0, u.KeyEnter)
	if !a.ProfileOpen || a.Busy || len(a.State.Profiles) != 0 {
		t.Fatal("composition return submitted the form")
	}
	if a.ProfileDraft.Name != "办公网络" {
		t.Fatal("Chinese input did not commit", a.ProfileDraft.Name)
	}
	tt.Key(u.Cmd, u.KeyN)
	tt.Key(u.Cmd|u.Alt, u.KeyI)
	if a.TunnelOpen || a.CompactInspector || !a.ProfileOpen {
		t.Fatal("window shortcut escaped modal scope")
	}
	tt.Key(0, u.KeyEscape)
	if !a.DraftCloseOpen {
		t.Fatal("dirty form closed without a choice")
	}
	tt.Key(0, u.KeyEscape)
	if a.DraftCloseOpen || !a.ProfileOpen || a.ProfileDraft.Name != "办公网络" {
		t.Fatal("Escape did not dismiss only the top dialog")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("放弃修改"); err != nil {
		t.Fatal(err)
	}
	if a.ProfileOpen || a.HasUnsavedChanges() {
		t.Fatal("discard failed")
	}
	if !tt.Focused("添加连接") {
		t.Fatal("focus did not return after nested dialog")
	}
}

func TestCoreTaskWithoutWindowDoesNotLaunchWorker(t *testing.T) {
	a := newTestApp(t)
	a.CoreTask("test", func(context.Context) error { t.Error("windowless worker launched"); return nil }, nil)
	if a.CoreBusy || a.Notice == "" {
		t.Fatal("unavailable window was not reported")
	}
}
