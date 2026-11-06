package logging

import (
	"frp-desktop/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestQuotaNeverRemovesCurrentOrUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, domain.NewID())
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"current.log", "1.log", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ten-bytes!"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(root, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "1.log")); !os.IsNotExist(err) {
		t.Fatal("rotated log not pruned")
	}
	for _, name := range []string{"current.log", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("protected file removed", name)
		}
	}
}
