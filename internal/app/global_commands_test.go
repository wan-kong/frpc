package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"frp-desktop/internal/domain"
)

func TestGlobalSettingsRejectStaleDraftButIgnoreUnrelatedProfileChanges(t *testing.T) {
	c := testController(t)
	old := c.Store.Snapshot().Settings
	a := old
	a.Appearance = "dark"
	cmd := GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: old.Revision, Action: SaveAppSettings{Settings: a}}
	result, err := c.ExecuteGlobal(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Settings.Revision != 1 {
		t.Fatal("editable settings revision did not advance")
	}
	_, err = c.ExecuteGlobal(context.Background(), GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: 0, Action: SaveAppSettings{Settings: old}})
	requireCommandCode(t, err, domain.Conflict)
	fresh := result.State.Settings
	fresh.ConfirmQuit = false
	_ = commandProfile(t, c) // A new connection must not invalidate a settings edit.
	if _, err = c.ExecuteGlobal(context.Background(), GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: fresh.Revision, Action: SaveAppSettings{Settings: fresh}}); err != nil {
		t.Fatal("unrelated profile change conflicted", err)
	}
	replayed, err := c.ExecuteGlobal(context.Background(), cmd)
	if err != nil || !replayed.Replayed {
		t.Fatal("settings replay failed", err)
	}
	if replayed.State.Settings.Appearance != "dark" || replayed.State.Settings.ConfirmQuit {
		t.Fatal("old receipt overwrote newer settings")
	}
}
func TestRestoreCommandReplaysWithoutCreatingMoreConnections(t *testing.T) {
	c := testController(t)
	_ = commandProfile(t, c)
	data, err := c.Backup("global-backup-password")
	if err != nil {
		t.Fatal(err)
	}
	cmd := GlobalCommand{OperationID: domain.NewID(), Action: RestoreBackup{Data: data, Password: "global-backup-password"}}
	first, err := c.ExecuteGlobal(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.IDs) != 1 || !first.Persisted || len(first.State.Profiles) != 2 {
		t.Fatal("restore result incomplete")
	}
	rev := c.Store.Revision()
	replayed, err := c.ExecuteGlobal(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.IDs[0] != first.IDs[0] || c.Store.Revision() != rev || len(replayed.State.Profiles) != 2 {
		t.Fatal("restore repeated")
	}
	changed := cmd
	changed.Action = RestoreBackup{Data: data, Password: "different"}
	_, err = c.ExecuteGlobal(context.Background(), changed)
	requireCommandCode(t, err, domain.Conflict)
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}
func bundledReleaseArchive(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "resources", "darwin-"+runtime.GOARCH, "bin", "frpc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := io.ReadAll(gz)
	gz.Close()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zip := gzip.NewWriter(&out)
	tr := tar.NewWriter(zip)
	for name, data := range map[string][]byte{"frp/frpc": binary, "frp/LICENSE": []byte("test fixture license")} {
		if err = tr.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0700, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tr.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err = zip.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestCoreDownloadPreparationDoesNotBlockProfileOrSettingsCommands(t *testing.T) {
	c := testController(t)
	p := commandProfile(t, c)
	archive := bundledReleaseArchive(t)
	filename := "frp_" + domain.CertifiedCore + "_darwin_" + runtime.GOARCH + ".tar.gz"
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	release, _ := json.Marshal(map[string]any{"assets": []map[string]string{{"name": filename, "browser_download_url": "https://github.com/fatedier/frp/releases/download/v0.72.0/" + filename, "digest": "sha256:" + digest}, {"name": "frp_sha256_checksums.txt", "browser_download_url": "https://github.com/fatedier/frp/releases/download/v0.72.0/frp_sha256_checksums.txt"}}})
	entered, resume := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	c.coreClient = func(string) (*http.Client, error) {
		return &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(r.URL.Host, "api.github.com"):
				close(entered)
				select {
				case <-resume:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return response(release), nil
			case strings.HasSuffix(r.URL.Path, ".txt"):
				return response([]byte(fmt.Sprintf("%s  %s\n", digest, filename))), nil
			default:
				return response(archive), nil
			}
		})}, nil
	}
	download := make(chan error, 1)
	go func() {
		_, err := c.ExecuteGlobal(context.Background(), GlobalCommand{OperationID: domain.NewID(), Action: InstallCore{Version: domain.CertifiedCore}})
		download <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("download never started")
	}
	controls := make(chan error, 1)
	go func() {
		_, err := c.Execute(context.Background(), Command{Meta: metaFor(p), Action: StopProfile{}})
		if err == nil {
			s := c.Store.Snapshot().Settings
			s.Appearance = "dark"
			_, err = c.ExecuteGlobal(context.Background(), GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: s.Revision, Action: SaveAppSettings{Settings: s}})
		}
		controls <- err
	}()
	select {
	case err := <-controls:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("download blocked independent control")
	}
	close(resume)
	select {
	case err := <-download:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("download did not finish")
	}
	state := c.Store.Snapshot()
	if len(state.Cores) != 1 || state.CoreRevision != 1 || state.Settings.Appearance != "dark" {
		t.Fatal("independent results were lost")
	}
}

func TestBackupExportReplayDoesNotOverwriteSubsequentFileChange(t *testing.T) {
	c := testController(t)
	_ = commandProfile(t, c)
	path := filepath.Join(t.TempDir(), "backup.frpbackup")
	cmd := GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: c.Store.Revision(), Action: ExportBackup{Path: path, Password: "export-command-password"}}
	if _, err := c.ExecuteGlobal(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("subsequent external file change"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.ExecuteGlobal(context.Background(), cmd)
	if err != nil || !result.Replayed {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "subsequent external file change" {
		t.Fatal("export replay overwrote the file", err)
	}
}
