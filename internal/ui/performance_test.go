package ui

import (
	"fmt"
	"frp-desktop/internal/domain"
	u "github.com/egoist/mygo/ui"
	"testing"
)

func BenchmarkTunnelList(b *testing.B) {
	for _, count := range []int{500, 5000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			p := domain.NewProfile()
			p.ServerAddr = "example.net"
			for i := 0; i < count; i++ {
				t := domain.NewTunnel()
				t.Name = fmt.Sprintf("service-%04d", i)
				t.LocalPort = 3000 + i%500
				t.RemotePort = 6000 + i%500
				p.Tunnels = append(p.Tunnels, t)
			}
			a := &App{State: domain.State{Profiles: []domain.Profile{p}}, Selected: p.ID, Runtime: map[string]domain.Runtime{}, SidebarWidth: 220, SelectedRow: -1}
			tt := u.NewTester(a.View, 1120, 760)
			b.ResetTimer()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}
