package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
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
			if hex.EncodeToString(sum[:]) != bundledSHA[arch] {
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

func TestInstallVerifiesBeforeCommitAndNeverOverwritesVersion(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "resources", "darwin-"+runtime.GOARCH, "bin", "frpc.gz"))
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
	if err = os.Chmod(co.Path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = c.installVerified(context.Background(), domain.CertifiedCore, "test", bin, nil); err == nil {
		t.Fatal("nonexecutable reused as usable core")
	}
	if err = os.Chmod(co.Path, 0700); err != nil {
		t.Fatal(err)
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
