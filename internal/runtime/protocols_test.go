package runtime

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

type protocolFixture struct {
	sup       *Supervisor
	core      domain.Core
	port      int
	httpPort  int
	httpsPort int
	secrets   *storage.MemorySecrets
}

func protocolServer(t *testing.T) *protocolFixture {
	t.Helper()
	dir := t.TempDir()
	co, err := core.Bundled(context.Background(), filepath.Join("..", "..", "resources"), dir)
	if err != nil {
		t.Fatal(err)
	}
	port, hp, hsp := availablePort(t), availablePort(t), availablePort(t)
	path := filepath.Join(dir, "frps.toml")
	data := fmt.Sprintf("bindAddr='127.0.0.1'\nproxyBindAddr='127.0.0.1'\nbindPort=%d\nvhostHTTPPort=%d\nvhostHTTPSPort=%d\nauth.token='integration-server-token'\n", port, hp, hsp)
	if err = storage.AtomicWrite(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary, _ := filepath.Abs(filepath.Join("..", "..", ".test-tools", "frps"))
	server := exec.Command(binary, "-c", path)
	server.Stdout = io.Discard
	server.Stderr = io.Discard
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	await(t, 3*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", domain.Endpoint("127.0.0.1", port), 100*time.Millisecond)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})
	exe, _ := os.Executable()
	secrets := &storage.MemorySecrets{}
	sup := New(dir, secrets, exe)
	t.Cleanup(func() { _ = sup.Close() })
	return &protocolFixture{sup, co, port, hp, hsp, secrets}
}
func (f *protocolFixture) profile() domain.Profile {
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.ServerPort = f.port
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = f.core.ID
	p.Revision = 1
	p.TokenRef, _ = storage.PutSecret(f.secrets, "integration-server-token")
	return p
}
func (f *protocolFixture) start(t *testing.T, p domain.Profile) {
	t.Helper()
	if err := f.sup.Start(context.Background(), p, f.core); err != nil {
		t.Fatal(err)
	}
}
func (f *protocolFixture) registered(t *testing.T, p domain.Profile) string {
	t.Helper()
	remote := ""
	await(t, 8*time.Second, func() bool {
		for _, st := range f.sup.Snapshot()[p.ID].Proxies {
			if st.Status == "running" {
				remote = st.RemoteAddr
				return true
			}
		}
		return false
	})
	return remote
}

func TestIntegrationUDPForwarding(t *testing.T) {
	f := protocolServer(t)
	local, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, addr, err := local.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = local.WriteTo(b[:n], addr)
		}
	}()
	p := f.profile()
	x := domain.NewTunnel()
	x.Name = "udp-echo"
	x.Type = "udp"
	x.LocalPort = local.LocalAddr().(*net.UDPAddr).Port
	p.Tunnels = []domain.Tunnel{x}
	f.start(t, p)
	remote := f.registered(t, p)
	conn, err := net.Dial("udp", remote)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = conn.Write([]byte("udp-frp")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	n, err := conn.Read(b)
	if err != nil || string(b[:n]) != "udp-frp" {
		t.Fatalf("UDP forwarding: %q %v", b[:n], err)
	}
}

func TestIntegrationAuthenticationFailureStaysVisible(t *testing.T) {
	f := protocolServer(t)
	p := f.profile()
	p.TokenRef, _ = storage.PutSecret(f.secrets, "wrong-auth-test-token")
	f.start(t, p)
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) && f.sup.Snapshot()[p.ID].Session != "authentication_failed" {
		time.Sleep(40 * time.Millisecond)
	}
	if f.sup.Snapshot()[p.ID].Session != "authentication_failed" {
		lines, _ := f.sup.Logs(p.ID)
		t.Fatalf("session=%+v logs=%+v", f.sup.Snapshot()[p.ID], lines)
	}
	time.Sleep(2200 * time.Millisecond)
	r := f.sup.Snapshot()[p.ID]
	if r.Session != "authentication_failed" || r.SessionError == "" {
		t.Fatalf("API poll hid authentication failure: %+v", r)
	}
}

func TestIntegrationHTTPAndHTTPSForwarding(t *testing.T) {
	for _, protocol := range []string{"http", "https"} {
		t.Run(protocol, func(t *testing.T) {
			f := protocolServer(t)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("frp-web")) })
			var local *httptest.Server
			host := "frp.test"
			var tlsConfig *tls.Config
			if protocol == "https" {
				local = httptest.NewTLSServer(handler)
				tlsConfig = local.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
				host = local.Certificate().DNSNames[0]
				tlsConfig.ServerName = host
			} else {
				local = httptest.NewServer(handler)
			}
			defer local.Close()
			p := f.profile()
			x := domain.NewTunnel()
			x.Name = "web"
			x.Type = protocol
			x.LocalPort = local.Listener.Addr().(*net.TCPAddr).Port
			x.Domains = []string{host}
			p.Tunnels = []domain.Tunnel{x}
			f.start(t, p)
			_ = f.registered(t, p)
			port := f.httpPort
			if protocol == "https" {
				port = f.httpsPort
			}
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", domain.Endpoint("127.0.0.1", port))
			}}}
			resp, err := client.Get(protocol + "://" + host + ":" + strconv.Itoa(port) + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "frp-web" {
				t.Fatalf("web forwarding: %q %v", body, err)
			}
		})
	}
}

func TestIntegrationSTCPVisitor(t *testing.T) {
	f := protocolServer(t)
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
	secret, err := storage.PutSecret(f.secrets, "private-integration-secret")
	if err != nil {
		t.Fatal(err)
	}
	provider := f.profile()
	x := domain.NewTunnel()
	x.Name = "private-echo"
	x.Type = "stcp"
	x.LocalPort = local.Addr().(*net.TCPAddr).Port
	x.SecretRef = secret
	provider.Tunnels = []domain.Tunnel{x}
	f.start(t, provider)
	_ = f.registered(t, provider)
	visitor := f.profile()
	v := domain.NewVisitor()
	v.Name = "local-visitor"
	v.ServerName = x.Name
	v.SecretRef = secret
	v.BindPort = availablePort(t)
	visitor.Visitors = []domain.Visitor{v}
	f.start(t, visitor)
	addr := domain.Endpoint(v.BindAddr, v.BindPort)
	await(t, 3*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write([]byte("private-frp"))
		b := make([]byte, 11)
		_, err = io.ReadFull(conn, b)
		return err == nil && string(b) == "private-frp"
	})
	wrong, _ := storage.PutSecret(f.secrets, "wrong-private-secret")
	visitor.Visitors[0].SecretRef = wrong
	visitor.Revision++
	if err = f.sup.Apply(context.Background(), visitor, f.core); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write([]byte("private-frp"))
	b := make([]byte, 11)
	_, err = io.ReadFull(conn, b)
	if err == nil && string(b) == "private-frp" {
		t.Fatal("visitor accepted wrong private key")
	}
}
