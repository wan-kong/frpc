package runtime

import (
	"context"
	"net"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

func TestIntegrationSUDPVisitorAndCredentialReload(t *testing.T) {
	f := protocolServer(t)
	local, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := local.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = local.WriteTo(buf[:n], addr)
		}
	}()
	secret, err := storage.PutSecret(f.secrets, "private-udp-integration-secret")
	if err != nil {
		t.Fatal(err)
	}
	provider := f.profile()
	x := domain.NewTunnel()
	x.Name, x.Type, x.SecretRef = "private-udp-echo", "sudp", secret
	x.LocalPort = local.LocalAddr().(*net.UDPAddr).Port
	provider.Tunnels = []domain.Tunnel{x}
	f.start(t, provider)
	_ = f.registered(t, provider)
	bind, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := bind.LocalAddr().(*net.UDPAddr).Port
	_ = bind.Close()
	visitor := f.profile()
	v := domain.NewVisitor()
	v.Name, v.Type, v.ServerName, v.SecretRef, v.BindPort = "udp-visitor", "sudp", x.Name, secret, port
	visitor.Visitors = []domain.Visitor{v}
	f.start(t, visitor)
	addr := domain.Endpoint(v.BindAddr, v.BindPort)
	echo := func() bool {
		// Each probe uses a new source port and nonce, excluding an old UDP flow.
		conn, err := net.DialTimeout("udp4", addr, time.Second)
		if err != nil {
			return false
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		message := domain.NewID()
		if _, err := conn.Write([]byte(message)); err != nil {
			return false
		}
		var data [128]byte
		n, err := conn.Read(data[:])
		return err == nil && string(data[:n]) == message
	}
	await(t, 5*time.Second, echo)
	wrong, err := storage.PutSecret(f.secrets, "wrong-private-udp-secret")
	if err != nil {
		t.Fatal(err)
	}
	visitor.Visitors[0].SecretRef = wrong
	visitor.Revision++
	if err := f.sup.Apply(context.Background(), visitor, f.core); err != nil {
		t.Fatal(err)
	}
	if echo() {
		t.Fatal("SUDP visitor forwarded a new flow with the wrong secret")
	}
	if f.sup.Snapshot()[visitor.ID].Process != "running" {
		t.Fatal("wrong-key check lost its visitor process")
	}
	visitor.Visitors[0].SecretRef = secret
	visitor.Revision++
	if err := f.sup.Apply(context.Background(), visitor, f.core); err != nil {
		t.Fatal(err)
	}
	await(t, 5*time.Second, echo)
}
