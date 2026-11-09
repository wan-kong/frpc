package app

import (
	"os"
	"path/filepath"
	"testing"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func installedCore(t *testing.T) (*Controller, domain.Core) {
	t.Helper()
	c := testController(t)
	co := domain.Core{ID: domain.NewID(), Version: "0.72.0"}
	co.Path = filepath.Join(c.Store.Dir, "cores", co.ID, "frpc")
	if err := storage.AtomicWrite(co.Path, []byte("test binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.Update(func(s *domain.State) error { s.Cores = append(s.Cores, co); return nil }); err != nil {
		t.Fatal(err)
	}
	return c, co
}
func TestDeleteCoreRemovesOnlyOwnedUnreferencedFile(t *testing.T) {
	c, co := installedCore(t)
	other := filepath.Join(filepath.Dir(co.Path), "keep.txt")
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCore(co.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Store.Snapshot().Core(co.ID); ok {
		t.Fatal("record remained")
	}
	if _, err := os.Stat(co.Path); !os.IsNotExist(err) {
		t.Fatal("binary remained", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("unrelated file removed", err)
	}
}
func TestDeleteCoreProtectsSnapshotReferencesAndLinks(t *testing.T) {
	c, co := installedCore(t)
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	p.CoreID = co.ID
	if err := c.Store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.Update(func(s *domain.State) error { s.Profiles = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCore(co.ID); err == nil {
		t.Fatal("snapshot reference ignored")
	}
	if _, err := os.Stat(co.Path); err != nil {
		t.Fatal(err)
	}
	c2, co2 := installedCore(t)
	outside := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(outside, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(co2.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, co2.Path); err != nil {
		t.Fatal(err)
	}
	if err := c2.DeleteCore(co2.ID); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("foreign file affected", err)
	}
	if _, ok := c2.Store.Snapshot().Core(co2.ID); !ok {
		t.Fatal("refused delete removed record")
	}
}

func TestDeleteDownloadedOfficialCore(t *testing.T) {
	c, co := installedCore(t)
	oldPath := co.Path
	co.ID = "official-0.72.0-arm64"
	co.Path = filepath.Join(c.Store.Dir, "cores", co.ID, "frpc")
	if err := storage.AtomicWrite(co.Path, []byte("official test binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.Update(func(s *domain.State) error { s.Cores = []domain.Core{co}; return nil }); err != nil {
		t.Fatal(err)
	}
	license := filepath.Join(filepath.Dir(co.Path), "LICENSE")
	if err := os.WriteFile(license, []byte("official license"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteCore(co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(co.Path)); !os.IsNotExist(err) {
		t.Fatal("official directory remained", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("unrelated core removed", err)
	}
}
