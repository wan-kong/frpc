package config

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func profile(t *testing.T) (domain.Profile, *storage.MemorySecrets) {
	t.Helper()
	s := &storage.MemorySecrets{}
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.TLS = false
	p.VerifyTLS = false
	var err error
	p.TokenRef, err = storage.PutSecret(s, "test-sensitive-token")
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}
func TestImportDoesNotDropUnknownFields(t *testing.T) {
	p, s := profile(t)
	p.Tunnels = []domain.Tunnel{domain.NewTunnel()}
	p.Tunnels[0].Name = "ssh"
	p.Tunnels[0].LocalPort = 22
	b, err := Compile(p, s, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b = append([]byte("clientID = 'custom-client'\n"), b...)
	out, err := Import(b, "导入", s)
	if err != nil {
		t.Fatal(err)
	}
	if out.Profile.Mode != "raw" || string(out.Raw) != string(b) {
		t.Fatal("unknown fields were not preserved")
	}
}
func TestImportRoundTripAndSecrets(t *testing.T) {
	p, s := profile(t)
	tunnel := domain.NewTunnel()
	tunnel.LocalPort = 8080
	tunnel.RemotePort = 6000
	tunnel.Name = "quote\"\\中文"
	p.Tunnels = []domain.Tunnel{tunnel}
	b, err := Compile(p, s, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Import(b, "导入", s)
	if err != nil {
		t.Fatal(err)
	}
	if out.Profile.Mode != "managed" || out.Profile.Tunnels[0].Name != tunnel.Name {
		t.Fatalf("wrong round trip: %+v", out)
	}
	redacted, err := Compile(out.Profile, s, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(redacted), "test-sensitive-token") {
		t.Fatal("secret leaked")
	}
	if !strings.Contains(string(redacted), "请补充凭证") {
		t.Fatal("missing redaction notice")
	}
}

func TestMetadataRedactedByDefault(t *testing.T) {
	p, secrets := profile(t)
	p.Metadata = map[string]string{"meta_token": "metadata-secret"}
	b, err := Compile(p, secrets, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "metadata-secret") {
		t.Fatal("metadata leaked")
	}
}

func TestProtocolSwitchDoesNotEmitHiddenFields(t *testing.T) {
	p, secrets := profile(t)
	x := domain.NewTunnel()
	x.LocalPort = 8080
	x.Domains = []string{"stale.example.net"}
	x.HTTPPasswordRef = domain.NewID()
	x.SecretRef = domain.NewID()
	p.Tunnels = []domain.Tunnel{x}
	data, err := Compile(p, secrets, nil, true)
	if err != nil {
		t.Fatal("unused secrets were read", err)
	}
	for _, field := range []string{"customDomains", "secretKey", "httpPassword"} {
		if strings.Contains(string(data), field) {
			t.Fatal("hidden protocol field emitted", field)
		}
	}
}
func TestRejectExternalInputs(t *testing.T) {
	for _, extra := range []string{"includes = ['../secret.toml']", "serverAddr = '{{ .Envs.HOST }}'", "[transport.tls]\ntrustedCaFile = '/private/secret'", "[log]\nto = '/tmp/log'"} {
		t.Run(extra, func(t *testing.T) {
			if _, err := Import([]byte(extra), "测试", &storage.MemorySecrets{}); err == nil {
				t.Fatal("unsafe import accepted")
			}
		})
	}
}
func TestBatchPortsAndRestart(t *testing.T) {
	p, _ := profile(t)
	base := domain.NewTunnel()
	items, err := ExpandPorts(base, "6000-6002", "7000-7002")
	if err != nil || len(items) != 3 || items[2].LocalPort != 6002 || items[0].ID == items[1].ID {
		t.Fatal(items, err)
	}
	if _, err = ExpandPorts(base, "1-2", "3-5"); err == nil {
		t.Fatal("unequal ranges accepted")
	}
	next := p
	next.Tunnels = items
	if NeedsRestart(p, next) {
		t.Fatal("proxy change forced restart")
	}
	next.ServerPort++
	if !NeedsRestart(p, next) {
		t.Fatal("common change did not require restart")
	}
}

func TestIntegrationCertifiedProtocolsVerify(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only bundled core")
	}
	co, err := core.Bundled(context.Background(), filepath.Join("..", "..", "resources"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"tcp", "udp", "http", "https", "stcp", "sudp", "xtcp"} {
		t.Run(protocol, func(t *testing.T) {
			p, s := profile(t)
			x := domain.NewTunnel()
			x.Name = "test-" + protocol
			x.Type = protocol
			x.LocalPort = 8080
			x.RemotePort = 0
			if protocol == "http" || protocol == "https" {
				x.Domains = []string{"test.example.net"}
			}
			if protocol == "stcp" || protocol == "sudp" || protocol == "xtcp" {
				x.SecretRef, _ = storage.PutSecret(s, "private-key")
			}
			p.Tunnels = []domain.Tunnel{x}
			if x.SecretRef != "" {
				v := domain.NewVisitor()
				v.Name = "visitor"
				v.Type = protocol
				v.ServerName = x.Name
				v.SecretRef = x.SecretRef
				v.BindPort = 18080
				p.Visitors = []domain.Visitor{v}
			}
			b, err := Compile(p, s, &Admin{Addr: "127.0.0.1", Port: 17400, User: "test", Password: "test"}, true)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "frpc.toml")
			if err = storage.AtomicWrite(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(co.Path, "verify", "-c", path).CombinedOutput()
			if err != nil {
				t.Fatalf("verify %s: %v\n%s\n%s", protocol, err, out, b)
			}
		})
	}
}
