package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	u "github.com/egoist/mygo/ui"
)

func paneRect(t *testing.T, tt *u.Tester, label string) u.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatal("missing pane", label)
	}
	return r
}
func closeSize(got, want float32) bool { return math.Abs(float64(got-want)) < 1 }
func paneApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	x := domain.NewTunnel()
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	a.State.Profiles = []domain.Profile{p}
	a.Selected = p.ID
	a.ShowLogs = true
	return a
}
func TestSidebarDragAndKeyboardStayInApprovedRange(t *testing.T) {
	a := paneApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	sidebar := paneRect(t, tt, "连接侧栏")
	if !closeSize(sidebar.W, sidebarDefault) {
		t.Fatal("default sidebar", sidebar)
	}
	y := sidebar.Y + sidebar.H/2
	tt.Press(sidebar.X+sidebar.W+0.5, y)
	tt.Move(700, y)
	tt.Release(700, y)
	if !closeSize(a.SidebarWidth, sidebarMaximum) || !closeSize(paneRect(t, tt, "连接侧栏").W, sidebarMaximum) {
		t.Fatal("sidebar exceeded maximum", a.SidebarWidth)
	}
	sidebar = paneRect(t, tt, "连接侧栏")
	tt.Press(sidebar.X+sidebar.W+0.5, y)
	tt.Move(40, y)
	tt.Release(40, y)
	if !closeSize(a.SidebarWidth, sidebarMinimum) {
		t.Fatal("sidebar exceeded minimum", a.SidebarWidth)
	}
	tt.Key(0, u.KeyLeft)
	if a.SidebarWidth != sidebarMinimum {
		t.Fatal("keyboard escaped minimum")
	}
	tt.Key(0, u.KeyRight)
	if a.SidebarWidth <= sidebarMinimum || a.SidebarWidth > sidebarMaximum {
		t.Fatal("keyboard could not resize sidebar")
	}
}
func TestLogPaneDragKeyboardResizeAndReopenPreserveHeight(t *testing.T) {
	a := paneApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	tt.Advance(200 * time.Millisecond)
	logPane := paneRect(t, tt, "日志面板")
	if !closeSize(logPane.H, logDefaultHeight) {
		t.Fatal("default log height", logPane, "body", paneRect(t, tt, "连接工作区"), "top", a.logTopHeight, "region", a.logRegionHeight, "table", paneRect(t, tt, "隧道工作区"))
	}
	if !closeSize(paneRect(t, tt, "隧道检查器").W, inspectorWidth) {
		t.Fatal("inspector width")
	}
	x, y := logPane.X+30, logPane.Y-0.5
	tt.Press(x, y)
	tt.Move(x, y-50)
	tt.Release(x, y-50)
	if !closeSize(a.LogHeight, logDefaultHeight+50) || !closeSize(paneRect(t, tt, "日志面板").H, a.LogHeight) {
		t.Fatal("log divider did not resize", a.LogHeight, paneRect(t, tt, "日志面板"))
	}
	tt.Key(0, u.KeyDown)
	if !closeSize(a.LogHeight, logDefaultHeight+40) {
		t.Fatal("keyboard did not resize log pane", a.LogHeight)
	}
	preferred := a.LogHeight
	if err := tt.Click("日志"); err != nil {
		t.Fatal(err)
	}
	if a.ShowLogs {
		t.Fatal("logs did not close")
	}
	if err := tt.Click("日志"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(200 * time.Millisecond)
	if !closeSize(paneRect(t, tt, "日志面板").H, preferred) {
		t.Fatal("log height lost on reopen")
	}
	tt.SetSize(1120, 900)
	tt.Advance(200 * time.Millisecond)
	if !closeSize(paneRect(t, tt, "日志面板").H, preferred) {
		t.Fatal("window resize changed preferred log height")
	}
	if tt.FramePending() {
		t.Fatal("stable panes keep requesting frames")
	}
}
func TestPanesFitCompactDarkZoomAndLongText(t *testing.T) {
	for _, width := range []int{900, 1120, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", width, dark), func(t *testing.T) {
				a := paneApp(t)
				a.State.Profiles[0].Name = "办公室网络与用于校验布局的较长连接名称"
				a.State.Profiles[0].ServerAddr = "a-very-long-development-hostname.example.net"
				tt := u.NewTester(a.View, width, 600)
				tt.SetDark(dark)
				tt.Advance(200 * time.Millisecond)
				for _, zoom := range []float32{1, 1.25} {
					tt.SetZoom(zoom)
					tt.Advance(200 * time.Millisecond)
					tt.Frame()
					logPane, table := paneRect(t, tt, "日志面板"), paneRect(t, tt, "隧道工作区")
					if logPane.H < 40 || table.H < 40 || logPane.Y+logPane.H > 600+1 || table.Y+table.H > logPane.Y+1 {
						t.Fatalf("panes overflow: zoom=%v logs=%+v table=%+v", zoom, logPane, table)
					}
					if tt.FramePending() {
						t.Fatal("layout did not settle")
					}
				}
			})
		}
	}
}

func TestRawProfileCanShowLogsWithoutVisualTunnelControls(t *testing.T) {
	a := paneApp(t)
	a.State.Profiles[0].Mode = "raw"
	a.State.Profiles[0].RawRef = domain.NewID()
	tt := u.NewTester(a.View, 1120, 760)
	tt.Advance(time.Millisecond)
	if !tt.HasText("此连接使用原文配置") {
		t.Fatal("raw description missing")
	}
	if _, ok := tt.Find("日志面板"); !ok {
		t.Fatal("raw profile logs are hidden despite the enabled log view")
	}
	if _, ok := tt.Find("隧道工作区"); ok {
		t.Fatal("raw profile exposed managed tunnel controls")
	}
}
func TestLogLayoutDoesNotCreateAnIdleFrameLoop(t *testing.T) {
	a := paneApp(t)
	builds := 0
	tt := u.NewTester(func(c *u.Context) { builds++; a.View(c) }, 1120, 760)
	tt.Advance(200 * time.Millisecond)
	initial := builds
	tt.Advance(time.Second)
	if builds != initial {
		t.Fatal("stable layout kept scheduling frames", initial, builds)
	}
}
