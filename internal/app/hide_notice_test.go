package app

import (
	"frp-desktop/internal/storage"
	"testing"
)

func TestHideNoticeAcknowledgementPersistsAndSurvivesStaleSettings(t *testing.T) {
	c := testController(t)
	stale := c.Store.Snapshot().Settings
	if err := c.AcknowledgeHideNotice(); err != nil {
		t.Fatal(err)
	}
	revision := c.Store.Revision()
	if err := c.AcknowledgeHideNotice(); err != nil {
		t.Fatal(err)
	}
	if c.Store.Revision() != revision {
		t.Fatal("duplicate acknowledgement wrote another revision")
	}
	stale.Appearance = "dark"
	if err := c.SaveSettings(stale, nil); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(c.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := reopened.Snapshot().Settings
	if !settings.HideNoticeAcknowledged || settings.Appearance != "dark" {
		t.Fatal("stale settings overwrote hide preference", settings)
	}
}
