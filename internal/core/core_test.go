package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"frp-desktop/internal/domain"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func archive(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tr := tar.NewWriter(gz)
	if err := tr.WriteHeader(&tar.Header{Name: name, Size: 1, Mode: 0755, Typeflag: kind, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		_, _ = tr.Write([]byte("x"))
	}
	_ = tr.Close()
	_ = gz.Close()
	return b.Bytes()
}
func TestRejectUnsafeArchive(t *testing.T) {
	for _, name := range []string{"/tmp/frpc", "../frpc", "a/../../frpc", "a\\frpc"} {
		if _, _, err := Extract(archive(t, name, tar.TypeReg)); err == nil {
			t.Fatal(name)
		}
	}
	if _, _, err := Extract(archive(t, "frpc", tar.TypeSymlink)); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestBundledResourcesForBothArchitectures(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			f, err := os.Open(filepath.Join("..", "..", "resources", "darwin-"+arch, "bin", "frpc.gz"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			gz, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			b, err := io.ReadAll(io.LimitReader(gz, 80<<20))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != bundledSHA["darwin-"+arch] {
				t.Fatal("bundled digest mismatch")
			}
			binary, err := macho.NewFile(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			defer binary.Close()
			want := macho.CpuArm64
			if arch == "amd64" {
				want = macho.CpuAmd64
			}
			if binary.Cpu != want {
				t.Fatal("wrong architecture", binary.Cpu)
			}
		})
	}
}

func zipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtractWindowsZip(t *testing.T) {
	bin, license, err := ExtractArchive("frp_0.72.0_windows_amd64.zip", zipArchive(t, map[string]string{
		"frp_0.72.0_windows_amd64/frpc.exe": "frpc",
		"frp_0.72.0_windows_amd64/LICENSE":  "license",
	}))
	if err != nil || string(bin) != "frpc" || string(license) != "license" {
		t.Fatal(string(bin), string(license), err)
	}
	for _, name := range []string{"../frpc.exe", "/frpc.exe", "a\\frpc.exe"} {
		if _, _, err := ExtractZip(zipArchive(t, map[string]string{name: "x"})); err == nil {
			t.Fatal("unsafe zip accepted:", name)
		}
	}
	if _, _, err := ExtractZip(zipArchive(t, map[string]string{"frpc.exe": "x"})); err == nil {
		t.Fatal("zip without a license accepted")
	}
}

func TestBundledWindowsResource(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "resources", "windows-amd64", "bin", "frpc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	b, err := io.ReadAll(io.LimitReader(gz, 80<<20))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != bundledSHA["windows-amd64"] {
		t.Fatal("bundled digest mismatch")
	}
	binary, err := pe.NewFile(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	if binary.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Fatal("wrong architecture", binary.Machine)
	}
}

func TestInstallVerifiesBeforeCommitAndNeverOverwritesVersion(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "resources", runtime.GOOS+"-"+runtime.GOARCH, "bin", "frpc.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	bin, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	c := Catalog{Dir: t.TempDir()}
	co, err := c.installVerified(context.Background(), domain.CertifiedCore, "test", bin, []byte("license"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.installVerified(context.Background(), domain.CertifiedCore, "test", []byte("invalid executable"), nil); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	b, err := os.ReadFile(co.Path)
	if err != nil || !bytes.Equal(b, bin) {
		t.Fatal("invalid install overwrote version", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.installVerified(ctx, domain.CertifiedCore, "test", bin, nil); err == nil {
		t.Fatal("canceled install accepted")
	}
	if runtime.GOOS != "windows" {
		if err = os.Chmod(co.Path, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = c.installVerified(context.Background(), domain.CertifiedCore, "test", bin, nil); err == nil {
			t.Fatal("nonexecutable reused as usable core")
		}
		if err = os.Chmod(co.Path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(co.Path, []byte("changed locally"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = c.installVerified(context.Background(), domain.CertifiedCore, "test", bin, nil); err == nil {
		t.Fatal("immutable mismatch overwritten")
	}
	b, err = os.ReadFile(co.Path)
	if err != nil || string(b) != "changed locally" {
		t.Fatal("existing version modified", err)
	}
	pending, err := filepath.Glob(filepath.Join(c.Dir, "cores", ".install-*"))
	if err != nil || len(pending) != 0 {
		t.Fatal("temporary installation leaked", pending, err)
	}
}
