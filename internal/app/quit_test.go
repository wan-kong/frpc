package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"frp-desktop/internal/domain"
)

func TestFailedUpdateQuitCannotLeaveAutomaticResumeIntent(t *testing.T) {
	c := testController(t)
	id := domain.NewID()
	stopFailure := errors.New("stop failed")
	err := c.closeForQuit(true, []string{id}, func() error {
		if _, err := os.Stat(filepath.Join(c.Store.Dir, "resume.json")); err != nil {
			t.Fatal("resume was not prepared", err)
		}
		return stopFailure
	})
	if !errors.Is(err, stopFailure) {
		t.Fatal("stop cause lost", err)
	}
	if ids := c.ConsumeResume(); len(ids) != 0 {
		t.Fatal("failed restart retained automatic reconnect", ids)
	}
}

func TestOrdinaryQuitClearsAnyEarlierResumeRecordBeforeClosing(t *testing.T) {
	c := testController(t)
	if err := c.WriteResume([]string{domain.NewID()}); err != nil {
		t.Fatal(err)
	}
	closed := false
	if err := c.closeForQuit(false, nil, func() error {
		closed = true
		if _, err := os.Stat(filepath.Join(c.Store.Dir, "resume.json")); !os.IsNotExist(err) {
			t.Fatal("ordinary quit left old restart intent", err)
		}
		return nil
	}); err != nil || !closed {
		t.Fatal("ordinary quit failed", err)
	}
	if len(c.ConsumeResume()) != 0 {
		t.Fatal("normal startup would resume a cancelled update")
	}
}

func TestUpdateResumeWriteFailureDoesNotStopConnections(t *testing.T) {
	c := testController(t)
	if err := os.Mkdir(filepath.Join(c.Store.Dir, "resume.json"), 0700); err != nil {
		t.Fatal(err)
	}
	closed := false
	err := c.closeForQuit(true, []string{domain.NewID()}, func() error { closed = true; return nil })
	if err == nil || closed {
		t.Fatal("resume write failure interrupted healthy connections", err)
	}
}

func TestSuccessfulUpdateQuitRetainsOneTimeResume(t *testing.T) {
	c := testController(t)
	id := domain.NewID()
	if err := c.closeForQuit(true, []string{id}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	ids := c.ConsumeResume()
	if len(ids) != 1 || ids[0] != id || len(c.ConsumeResume()) != 0 {
		t.Fatal("restart resume was lost or repeated", ids)
	}
}
