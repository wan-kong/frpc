package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFirstHideKeepsDraftAndDoesNotBlockRunningCommand(t *testing.T) {
	a := newTestApp(t)
	a.EditProfile("")
	a.ProfileDraft.Name = "unsaved draft"
	queue := make(chan func(), 4)
	a.dispatch = func(fn func()) { queue <- fn }
	a.Busy = true // A connection command may already be running when Cmd-W hides its window.
	shown, hidden := 0, 0
	show := func() error { shown++; return nil }
	hide := func() { hidden++ }
	a.hideWithNotice(show, hide)
	a.hideWithNotice(show, hide)
	if shown != 1 || hidden != 2 || !a.ProfileOpen || a.ProfileDraft.Name != "unsaved draft" || !a.Busy {
		t.Fatal("hide changed ongoing work or draft", shown, hidden)
	}
	select {
	case fn := <-queue:
		fn()
	case <-time.After(time.Second):
		t.Fatal("acknowledgement completion missing")
	}
	if !a.State.Settings.HideNoticeAcknowledged || !a.ProfileOpen {
		t.Fatal("hide acknowledgement lost")
	}
	a.hideWithNotice(show, hide)
	if shown != 1 || hidden != 3 {
		t.Fatal("notice repeated")
	}
	b := New(a.Controller)
	b.dispatch = a.dispatch
	b.hideWithNotice(show, hide)
	if shown != 1 || hidden != 4 {
		t.Fatal("persisted acknowledgement ignored")
	}
}
func TestHidePreferenceWriteFailureStillHidesOncePerSession(t *testing.T) {
	a := newTestApp(t)
	queue := make(chan func(), 4)
	a.dispatch = func(fn func()) { queue <- fn }
	blocker := filepath.Join(a.Controller.Store.Dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	a.Controller.Store.Dir = blocker
	shown, hidden := 0, 0
	show := func() error { shown++; return nil }
	hide := func() { hidden++ }
	a.hideWithNotice(show, hide)
	select {
	case fn := <-queue:
		fn()
	case <-time.After(time.Second):
		t.Fatal("failure completion missing")
	}
	if hidden != 1 || !strings.Contains(a.Notice, "记录失败") || a.Controller.Store.Snapshot().Settings.HideNoticeAcknowledged {
		t.Fatal("failed preference blocked hiding or looked saved", a.Notice)
	}
	a.hideWithNotice(show, hide)
	if shown != 1 || hidden != 2 {
		t.Fatal("notice repeated within failed-persistence session")
	}
}
func TestHideNoticeDisplayFailureUsesNonblockingFallback(t *testing.T) {
	a := newTestApp(t)
	hidden := false
	a.hideWithNotice(func() error { return errors.New("dialog unavailable") }, func() { hidden = true })
	if !hidden || !strings.Contains(a.Notice, "提示未能显示") || a.State.Settings.HideNoticeAcknowledged {
		t.Fatal("dialog failure blocked hide or was persisted")
	}
}
