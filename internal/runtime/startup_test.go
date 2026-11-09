package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"frp-desktop/internal/config"
	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func startupFixture(t *testing.T) (*Supervisor, domain.Profile, domain.Core) {
	t.Helper()
	dir := t.TempDir()
	co, err := core.Bundled(context.Background(), filepath.Join("..", "..", "resources"), dir)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := New(dir, &storage.MemorySecrets{}, exe)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	p.ServerPort = 1
	p.TLS = false
	p.VerifyTLS = false
	p.CoreID = co.ID
	p.Revision = 1
	return s, p, co
}
func TestIntegrationManagedAdminPortRetriesAreBoundedAndReselect(t *testing.T) {
	for _, conflicts := range []int{3, 4} {
		t.Run(fmt.Sprint(conflicts), func(t *testing.T) {
			s, p, co := startupFixture(t)
			occupied, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer occupied.Close()
			port := occupied.Addr().(*net.TCPAddr).Port
			selections := 0
			s.chooseAdminPort = func() (int, error) {
				selections++
				if selections <= conflicts {
					return port, nil
				}
				return freeAdminPort()
			}
			err = s.Start(context.Background(), p, co)
			if selections != 4 {
				t.Fatal("port selections", selections)
			}
			if conflicts == 3 {
				if err != nil {
					t.Fatal(err)
				}
				snapshot := s.Snapshot()[p.ID]
				if !snapshot.Running() || snapshot.PID == 0 || snapshot.ObservationStartedAt.IsZero() {
					t.Fatal("reselected startup not ready", snapshot)
				}
				e := s.get(p.ID)
				s.mu.Lock()
				actual := e.admin.Config.Port
				s.mu.Unlock()
				if actual == port {
					t.Fatal("conflicting port was reused")
				}
			} else {
				info, ok := domain.ErrorInfo(err)
				if !ok || info.Code != domain.PortInUse {
					t.Fatal("unclassified exhausted retry", err)
				}
				if s.Snapshot()[p.ID].PID != 0 {
					t.Fatal("failed attempt retained a process")
				}
				files, err := filepath.Glob(filepath.Join(s.Dir, "runtime", p.ID, "*.toml"))
				if err != nil || len(files) != 0 {
					t.Fatal("failed attempt retained runtime configuration", files, err)
				}
			}
			conn, err := net.Dial("tcp4", domain.Endpoint("127.0.0.1", port))
			if err != nil {
				t.Fatal("unrelated listener was affected", err)
			}
			conn.Close()
		})
	}
}
func TestIntegrationRawPortConflictDoesNotRewriteOrRetry(t *testing.T) {
	s, p, co := startupFixture(t)
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	raw := fmt.Sprintf("serverAddr='127.0.0.1'\nserverPort=1\nloginFailExit=false\n[webServer]\naddr='127.0.0.1'\nport=%d\n", port)
	p.Mode = "raw"
	p.RawRef = domain.NewID()
	if err = s.Vault.Write(p.RawRef, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	selections := 0
	s.chooseAdminPort = func() (int, error) { selections++; return freeAdminPort() }
	if err = s.Start(context.Background(), p, co); err == nil {
		t.Fatal("raw conflict accepted")
	}
	if selections != 0 || s.Snapshot()[p.ID].Generation != 1 {
		t.Fatal("raw mode was retried or chose a managed port")
	}
	data, err := s.Vault.Read(p.RawRef)
	if err != nil || string(data) != raw {
		t.Fatal("raw source was changed", err)
	}
	lines, err := s.Logs(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range lines {
		if strings.Contains(line.Text, fmt.Sprintf("listen tcp 127.0.0.1:%d: bind: address already in use", port)) {
			found = true
		}
	}
	if !found {
		t.Fatal("fixture did not exercise the actual frpc bind failure")
	}
}
func TestOnlyCurrentManagedStartupBindFailureTriggersRetry(t *testing.T) {
	admin := config.Admin{Addr: "127.0.0.1", Port: 12345}
	message := "listen tcp 127.0.0.1:12345: bind: address already in use"
	for _, tc := range []struct {
		name, message, mode, process string
		certified, want              bool
	}{
		{"managed", message, "managed", "starting", true, true},
		{"different port", "listen tcp 127.0.0.1:123456: bind: address already in use", "managed", "starting", true, false},
		{"remote listener", "listen tcp 0.0.0.0:12345: bind: address already in use", "managed", "starting", true, false},
		{"permissions", "listen tcp 127.0.0.1:12345: bind: permission denied", "managed", "starting", true, false},
		{"old failure", message, "managed", "running", true, false},
		{"raw", message, "raw", "starting", true, false},
		{"uncertified", message, "managed", "starting", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New("", &storage.MemorySecrets{}, "")
			e := s.get(domain.NewID())
			e.cmd = &exec.Cmd{}
			e.core.Certified = tc.certified
			e.profile.Mode = tc.mode
			e.snapshot.Process = tc.process
			e.admin = NewAdmin(admin)
			s.noteStartupError(e, tc.message)
			if e.startupPortInUse != tc.want {
				t.Fatal("incorrect bind failure classification")
			}
		})
	}
}
