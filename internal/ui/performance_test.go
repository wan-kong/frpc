package ui

import (
	"fmt"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	u "github.com/egoist/mygo/ui"
	"testing"
)

// Every iteration invalidates the sequence cache like an arriving log batch;
// this is not a benchmark of repeatedly reading an unchanged cached result.
func BenchmarkIncomingUnfilteredLogs(b *testing.B) {
	a := &App{Selected: domain.NewID(), LogLevel: "全部"}
	for i := 0; i < 10000; i++ {
		a.Logs = append(a.Logs, logging.Line{Seq: uint64(i + 1), Level: "info", Text: "中文日志：连接已注册，等待下一次请求"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a.Logs[len(a.Logs)-1].Seq++
		if len(a.filteredLogs()) != len(a.Logs) {
			b.Fatal("unfiltered logs were dropped")
		}
	}
}

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
