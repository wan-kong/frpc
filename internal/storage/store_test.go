package storage

import (
	"bytes"
	"encoding/json"
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
	st, _ := os.Stat(filepath.Join(dir, ConfigFile))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}
func TestSecretsLiveInConfigFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	// Binary values must survive the JSON round-trip; encoding/json stores
	// []byte as base64 rather than mangling invalid UTF-8.
	value := []byte{0xff, 0x00, 0xfe, 't', 'o', 'k'}
	if err = s.Set(id, value); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(id); err != nil || !bytes.Equal(got, value) {
		t.Fatal(got, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	var persisted domain.State
	if err = json.Unmarshal(b, &persisted); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persisted.Secrets[id], value) {
		t.Fatal("secret missing from config file")
	}
	if _, err = os.Stat(filepath.Join(dir, legacyStateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy state file still present")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.Get(id); err != nil || !bytes.Equal(got, value) {
		t.Fatal(got, err)
	}
	if err = reopened.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Get(id); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("deleted secret still readable", err)
	}
}

func TestLegacyStateFileMigratesToConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	st := domain.NewState()
	st.Profiles = []domain.Profile{p}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, legacyStateFile)
	if err = os.WriteFile(legacy, b, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Snapshot().Profile(p.ID); !ok || got.ServerAddr != "example.net" {
		t.Fatal(got, ok)
	}
	if _, err = os.Stat(filepath.Join(dir, ConfigFile)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy state file was not removed")
	}
}

func TestFutureAndCorruptSchemaNotOverwritten(t *testing.T) {
	for _, content := range []string{`{"schemaVersion":999}`, `broken json`} {
		dir := t.TempDir()
		p := filepath.Join(dir, ConfigFile)
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
	original, err := os.ReadFile(filepath.Join(dir, ConfigFile))
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
	preserved, err := filepath.Glob(filepath.Join(dir, "config.before-recovery-*.json"))
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

func TestScopedRevisionsTrackOnlyReplaceableSettingsAndCatalog(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	if err = s.SaveProfile(p, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot := s.Snapshot(); snapshot.Settings.Revision != 0 || snapshot.CoreRevision != 0 {
		t.Fatal("profile edit changed unrelated scope")
	}
	if err = s.Update(func(state *domain.State) error { state.Settings.HideNoticeAcknowledged = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Settings.Revision != 0 {
		t.Fatal("bookkeeping invalidated editable settings")
	}
	if err = s.Update(func(state *domain.State) error {
		state.Settings.Appearance = "dark"
		state.Settings.Revision = 999
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Settings.Revision != 1 {
		t.Fatal("settings version was not owned by the store")
	}
	id := domain.NewID()
	if err = s.Update(func(state *domain.State) error {
		state.Cores = append(state.Cores, domain.Core{ID: id})
		state.Settings.DefaultCoreID = id
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state := s.Snapshot()
	if state.CoreRevision != 1 || state.Settings.Revision != 1 {
		t.Fatal("catalog and settings versions leaked across scopes")
	}
	reopened, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	state = reopened.Snapshot()
	if state.CoreRevision != 1 || state.Settings.Revision != 1 {
		t.Fatal("scope revisions were not persisted")
	}
}
