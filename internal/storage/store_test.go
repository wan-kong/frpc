package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"frp-desktop/internal/domain"
)

func TestPersistenceConflictAndUndo(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	if err = s.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	p.Revision = 1
	p.Name = "changed"
	if err = s.SaveProfile(p, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProfile(p, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.Snapshot().Profile(p.ID)
	if got.Name != "changed" || got.Revision != 2 {
		t.Fatal(got)
	}
	snapshot := reopened.Snapshot()
	snapshot.Profiles[0].Name = "bad"
	got, _ = reopened.Snapshot().Profile(p.ID)
	if got.Name == "bad" {
		t.Fatal("snapshot aliases storage")
	}
	if err = reopened.DeleteProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Restore(p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.Snapshot().Profile(p.ID)
	if got.AutoConnect {
		t.Fatal("restore auto-connected")
	}
	st, _ := os.Stat(filepath.Join(dir, "state.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}
func TestFutureAndCorruptSchemaNotOverwritten(t *testing.T) {
	for _, content := range []string{`{"schemaVersion":999}`, `broken json`} {
		dir := t.TempDir()
		p := filepath.Join(dir, "state.json")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("accepted unsafe schema")
		}
		b, _ := os.ReadFile(p)
		if string(b) != content {
			t.Fatal("source overwritten")
		}
	}
}
func TestVaultTamperAndPathProtection(t *testing.T) {
	dir := t.TempDir()
	v := Vault{Dir: dir, Secrets: &MemorySecrets{}}
	id := domain.NewID()
	if err := v.Write(id, []byte("auth.token = 'sensitive'")); err != nil {
		t.Fatal(err)
	}
	b, err := v.Read(id)
	if err != nil || !strings.Contains(string(b), "sensitive") {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sources", id+".enc")
	encrypted, _ := os.ReadFile(path)
	if strings.Contains(string(encrypted), "sensitive") {
		t.Fatal("plaintext on disk")
	}
	encrypted[len(encrypted)-1] ^= 1
	_ = os.WriteFile(path, encrypted, 0600)
	if _, err = v.Read(id); err == nil {
		t.Fatal("tampering accepted")
	}
	if err = v.Write("../escape", nil); err == nil {
		t.Fatal("unsafe ID accepted")
	}
}

func TestRecoveryUsesReviewedSnapshotAndPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	p.Name = "reviewed"
	p.AutoConnect = true
	if err = store.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	reviewed := store.Snapshot()
	p, _ = reviewed.Profile(p.ID)
	p.Name = "later"
	if err = store.SaveProfile(p, p.Revision); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = RecoverSnapshot(dir, reviewed); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := recovered.Snapshot().Profile(p.ID)
	if got.Name != "reviewed" || got.AutoConnect || !reviewed.Profiles[0].AutoConnect {
		t.Fatal("recovery changed preview or used a different snapshot")
	}
	preserved, err := filepath.Glob(filepath.Join(dir, "state.before-recovery-*.json"))
	if err != nil || len(preserved) != 1 {
		t.Fatal(preserved, err)
	}
	b, err := os.ReadFile(preserved[0])
	if err != nil || string(b) != string(original) {
		t.Fatal("original not preserved", err)
	}
	reviewed.SchemaVersion++
	if err = RecoverSnapshot(dir, reviewed); err == nil {
		t.Fatal("future snapshot accepted")
	}
}
