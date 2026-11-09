package runtime

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--frpc-session" {
		os.Exit(Session(os.Args[2:]))
	}
	os.Exit(m.Run())
}
func availablePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func await(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func TestIntegrationStartReloadAndStop(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	co, err := core.Bundled(ctx, filepath.Join("..", "..", "resources"), dir)
	if err != nil {
		t.Fatal(err)
	}
	serverPath, err := filepath.Abs(filepath.Join("..", "..", ".test-tools", "frps"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(serverPath); err != nil {
		t.Skip("缺少 .test-tools/frps；真实内核集成检查已跳过")
	}
	port := availablePort(t)
	serverConfig := filepath.Join(dir, "frps.toml")
	if err = storage.AtomicWrite(serverConfig, []byte("bindAddr = '127.0.0.1'\nbindPort = "+strconv.Itoa(port)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(serverPath, "-c", serverConfig)
	server.Stdout = io.Discard
	server.Stderr = io.Discard
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	await(t, 3*time.Second, func() bool {
		c, err := net.DialTimeout("tcp", domain.Endpoint("127.0.0.1", port), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return true
		}
		return false
	})
	local, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		for {
			conn, err := local.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sup := New(dir, &storage.MemorySecrets{}, executable)
	defer sup.Close()
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.ServerPort = port
	p.CoreID = co.ID
	p.TLS = false
	p.VerifyTLS = false
	p.Revision = 1
	x := domain.NewTunnel()
	x.Name = "echo"
	x.LocalPort = local.Addr().(*net.TCPAddr).Port
	x.RemotePort = 0
	p.Tunnels = []domain.Tunnel{x}
	if err = sup.Start(ctx, p, co); err != nil {
		t.Fatal(err)
	}
	if err = sup.Start(ctx, p, co); err != nil {
		t.Fatal("duplicate start", err)
	}
	remote := ""
	await(t, 8*time.Second, func() bool {
		for _, status := range sup.Snapshot()[p.ID].Proxies {
			if status.Name == "echo" && status.Status == "running" {
				remote = status.RemoteAddr
				return remote != ""
			}
		}
		return false
	})
	conn, err := net.DialTimeout("tcp", remote, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = conn.Write([]byte("hello-frp")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 9)
	if _, err = io.ReadFull(conn, b); err != nil || string(b) != "hello-frp" {
		t.Fatalf("end-to-end echo: %q %v", b, err)
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	bad := p
	bad.Mode = "raw"
	bad.RawRef = domain.NewID()
	bad.Revision = 2
	raw := "serverAddr='127.0.0.1'\nserverPort=" + strconv.Itoa(port) + "\nloginFailExit=false\n[webServer]\naddr='127.0.0.1'\nport=" + strconv.Itoa(occupied.Addr().(*net.TCPAddr).Port) + "\n"
	if err = sup.Vault.Write(bad.RawRef, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err = sup.Apply(ctx, bad, co); err == nil {
		t.Fatal("runtime initialization failure was accepted")
	}
	if got := sup.Snapshot()[p.ID]; !got.Running() || got.AppliedRevision != p.Revision {
		t.Fatalf("previous config was not restored: %+v", got)
	}
	p.Tunnels[0].Enabled = false
	p.Revision++
	if err = sup.Apply(ctx, p, co); err != nil {
		t.Fatal(err)
	}
	await(t, 6*time.Second, func() bool { return len(sup.Snapshot()[p.ID].Proxies) == 0 })
	if err = sup.Stop(p.ID); err != nil {
		t.Fatal(err)
	}
	if sup.Snapshot()[p.ID].Running() {
		t.Fatal("still running")
	}
	for _, file := range []string{"candidate.toml"} {
		if _, err = os.Stat(filepath.Join(dir, "runtime", p.ID, file)); !os.IsNotExist(err) {
			t.Fatalf("sensitive temporary config left: %s", file)
		}
	}
}
func TestSessionRejectsRelativePaths(t *testing.T) {
	if Session([]string{"frpc", "config.toml"}) != 2 {
		t.Fatal("relative paths accepted")
	}
}
func TestVerifyRejectsChangedCore(t *testing.T) {
	dir := t.TempDir()
	sup := New(dir, &storage.MemorySecrets{}, "unused")
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	path := filepath.Join(dir, "fake")
	if err := os.WriteFile(path, []byte("fake"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, err := sup.Verify(context.Background(), p, domain.Core{Path: path, SHA256: strings.Repeat("0", 64), Certified: true})
	if err == nil {
		t.Fatal("changed binary accepted")
	}
}
