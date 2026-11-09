package ui

import (
	"testing"
	"time"

	u "github.com/egoist/mygo/ui"
)

func TestSettingsAppearanceSavesWithoutSavingOtherDrafts(t *testing.T) {
	for _, choice := range []struct {
		label string
		value string
	}{{"深色", "dark"}, {"浅色", "light"}} {
		t.Run(choice.value, func(t *testing.T) {
			a := newTestApp(t)
			a.Settings = a.State.Settings
			a.Settings.ConfirmQuit = !a.State.Settings.ConfirmQuit
			a.DownloadProxy = "http://draft.example.com:8080"
			queue := make(chan func(), 1)
			a.dispatch = func(fn func()) { queue <- fn }
			tt := u.NewTester(a.SettingsView, 640, 640)
			if err := tt.Click(choice.label); err != nil {
				t.Fatal(err)
			}
			select {
			case <-queue:
			case <-time.After(3 * time.Second):
				t.Fatal("theme selection did not trigger a save")
			}
			saved := a.Controller.Store.Snapshot().Settings
			if saved.Appearance != choice.value {
				t.Fatal("theme selection was not saved immediately", saved.Appearance)
			}
			if a.Settings.ConfirmQuit == saved.ConfirmQuit || a.DownloadProxy != "http://draft.example.com:8080" || saved.DownloadProxyRef != "" {
				t.Fatal("theme selection changed another settings draft")
			}
		})
	}
}
