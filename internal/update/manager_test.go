package update

import (
	"context"
	"errors"
	"testing"
)

type engine struct {
	enabled bool
	release *Release
	err     error
}

func (e engine) Enabled() bool                           { return e.enabled }
func (e engine) Check(context.Context) (*Release, error) { return e.release, e.err }
func TestDisabledDoesNotPretendCurrent(t *testing.T) {
	m, err := New(t.TempDir(), engine{})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Stage != "disabled" {
		t.Fatal(m.Snapshot())
	}
}
func TestSkippedReleaseAndInstallFailure(t *testing.T) {
	ctx := context.Background()
	calls := 0
	r := &Release{Version: "1.2.3", Install: func(context.Context, func(int64, int64)) error { calls++; return errors.New("signature mismatch") }}
	m, err := New(t.TempDir(), engine{enabled: true, release: r})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Check(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Skip(); err != nil {
		t.Fatal(err)
	}
	if err = m.Check(ctx, false); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Offer {
		t.Fatal("skipped update offered")
	}
	if err = m.Check(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().Offer {
		t.Fatal("manual check hid skipped version")
	}
	if err = m.Install(ctx); err == nil {
		t.Fatal("signature failure swallowed")
	}
	if calls != 1 || m.Snapshot().Stage != "error" {
		t.Fatal(m.Snapshot())
	}
}

func TestConcurrentPreferenceChangesPersistBothFields(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, engine{})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err = m.SetPreferences(Preferences{}); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		go func() { errs <- m.SetAutomaticChecks(true) }()
		go func() { errs <- m.SetAutomaticDownloads(true) }()
		for range 2 {
			if err = <-errs; err != nil {
				t.Fatal(err)
			}
		}
		restored, err := New(dir, engine{})
		if err != nil {
			t.Fatal(err)
		}
		p := restored.Snapshot().Preferences
		if !p.AutomaticChecks || !p.AutomaticDownloads {
			t.Fatal("concurrent preference overwritten", p)
		}
	}
}
