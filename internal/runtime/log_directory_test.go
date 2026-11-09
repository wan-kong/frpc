package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func TestPlatformLogDirectoryPreservesLegacyFilesAndOwnsNewLogs(t *testing.T) {
	data, logs := t.TempDir(), filepath.Join(t.TempDir(), "platform-logs")
	legacy := filepath.Join(data, "logs", "history.log")
	if err := storage.AtomicWrite(legacy, []byte("old history"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(data, &storage.MemorySecrets{}, "/unused")
	defer s.Close()
	if err := s.SetLogDirectory(logs); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	s.get(id).log.Add("[info] token=private new log")
	path := filepath.Join(logs, id, "current.log")
	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 {
		t.Fatal("platform log not written", err)
	}
	for path, mode := range map[string]os.FileMode{logs: 0700, filepath.Dir(path): 0700, path: 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("log permissions", path, info, err)
		}
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != "old history" {
		t.Fatal("legacy history changed", err)
	}
	rejected := filepath.Join(t.TempDir(), "late")
	if err := s.SetLogDirectory(rejected); err == nil {
		t.Fatal("active buffers changed location")
	}
	if _, err := os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("rejected change touched filesystem", err)
	}
}

func TestLogDirectoryRejectsRelativePathAndSymlinkWithoutChangingTarget(t *testing.T) {
	s := New(t.TempDir(), &storage.MemorySecrets{}, "/unused")
	defer s.Close()
	if err := s.SetLogDirectory("relative"); err == nil {
		t.Fatal("relative log directory accepted")
	}
	root := t.TempDir()
	target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLogDirectory(link); err == nil {
		t.Fatal("symlink accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("rejected symlink changed target", err)
	}
}
