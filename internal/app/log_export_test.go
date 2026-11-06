package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
)

func TestSelectedLogExportBindsSnapshotAndReplaysWithoutOverwriting(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	path := filepath.Join(t.TempDir(), "filtered.log")
	selected := &LogSelection{Lines: []logging.Line{{Seq: 42, Time: time.Now(), Level: "error", Text: "[ssh] token=private-value selected failure"}}}
	cmd := Command{Meta: metaFor(p), Action: ExportDiagnostics{Path: path, Selection: selected}}
	if _, err := c.Execute(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "selected failure") || strings.Contains(string(data), "private-value") {
		t.Fatalf("exported selection: %q %v", data, err)
	}
	if err := os.WriteFile(path, []byte("later user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.Execute(context.Background(), cmd)
	if err != nil || !result.Replayed {
		t.Fatal("export replay", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "later user edit" {
		t.Fatal("replay overwrote external file", err)
	}
	selected.Lines[0].Text = "different selection"
	_, err = c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Conflict)
}

func TestEmptyLogSelectionIsDifferentFromFullDiagnostics(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	path := filepath.Join(t.TempDir(), "empty.log")
	cmd := Command{Meta: metaFor(p), Action: ExportDiagnostics{Path: path, Selection: &LogSelection{}}}
	if _, err := c.Execute(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	cmd.Action = ExportDiagnostics{Path: path}
	_, err := c.Execute(context.Background(), cmd)
	requireCommandCode(t, err, domain.Conflict)
}
