package ui

import (
	"testing"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	u "github.com/egoist/mygo/ui"
)

func TestLogFiltersCombineTimeObjectLevelAndQuery(t *testing.T) {
	a := paneApp(t)
	a.State.Profiles[0].Tunnels[0].Name = "ssh"
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.Local)
	a.Logs = []logging.Line{
		{Seq: 1, Time: at.Add(-time.Second), Level: "error", Text: "[ssh] 连接失败"},
		{Seq: 2, Time: at, Level: "error", Text: "[ssh-copy] 连接失败"},
		{Seq: 3, Time: at.Add(500 * time.Millisecond), Level: "error", Text: "[ssh] 连接失败"},
		{Seq: 4, Time: at, Level: "info", Text: "[ssh] 连接成功"},
		{Seq: 5, Time: at.Add(time.Second), Level: "error", Text: "[ssh] 连接失败"},
	}
	a.LogFrom, a.LogUntil = "2026-11-06 12:00:00", "2026-11-06 12:00:00"
	a.LogLevel, a.LogTunnel, a.logAppliedSearch = "error", "隧道 · ssh", "失败"
	lines := a.filteredLogs()
	if len(lines) != 1 || lines[0].Seq != 3 {
		t.Fatalf("combined filters: %+v", lines)
	}
	if next := a.filteredLogs(); &next[0] != &lines[0] {
		t.Fatal("stable filter rebuilt its results")
	}
	// A renamed/deleted target must not reuse the old cache or widen to all logs.
	a.State.Profiles[0].Tunnels[0].Name = "renamed"
	if len(a.filteredLogs()) != 0 {
		t.Fatal("stale object filter reused previous results")
	}
	a.LogTunnel = "全部日志"
	if len(a.filteredLogs()) != 2 {
		t.Fatal("reset did not restore connection-level results")
	}
}

func TestMalformedLogTimeDoesNotSilentlyExportAllLogs(t *testing.T) {
	a := paneApp(t)
	a.Logs = []logging.Line{{Seq: 1, Time: time.Now(), Level: "info", Text: "visible"}}
	for _, bounds := range [][2]string{
		{"2026-02-30 12:00:00", ""},
		{"", "not a date"},
		{"2026-11-06 12:00:01", "2026-11-06 12:00:00"},
	} {
		a.LogFrom, a.LogUntil = bounds[0], bounds[1]
		if a.logFilterError() == nil {
			t.Fatal("invalid export filter accepted")
		}
		if _, _, err := a.logTimeBounds(); err == nil {
			t.Fatal("invalid range accepted", bounds)
		}
		if len(a.filteredLogs()) != 0 {
			t.Fatal("invalid range widened results", bounds)
		}
		// Both actions must refuse before accessing any native platform service.
		a.CopyLogs()
		a.ExportLogs()
		if a.Notice == "" {
			t.Fatal("invalid export has no user-facing reason")
		}
	}
	a.LogFrom, a.LogUntil = "", ""
	if len(a.filteredLogs()) != 1 {
		t.Fatal("cleared range retained invalid cache")
	}
}

func TestVisitorLogChoiceAndConnectionSwitch(t *testing.T) {
	a := paneApp(t)
	v := domain.NewVisitor()
	v.Name = "家中访问者"
	a.State.Profiles[0].Visitors = []domain.Visitor{v}
	a.LogTunnel = "访问者 · " + v.Name
	a.Logs = []logging.Line{{Seq: 1, Level: "info", Text: "[家中访问者] started"}, {Seq: 2, Level: "info", Text: "[other] started"}}
	if lines := a.filteredLogs(); len(lines) != 1 || lines[0].Seq != 1 {
		t.Fatal(lines)
	}
	a.LogFiltersOpen = true
	a.SelectScope("all")
	if a.LogTunnel != "" || a.LogFiltersOpen || len(a.Logs) != 0 {
		t.Fatal("object filter escaped connection scope")
	}
}

func TestLogFilterPopoverFitsAndEscapeDoesNotClearSearch(t *testing.T) {
	a := paneApp(t)
	a.Search = "keep search"
	tt := u.NewTester(a.View, 900, 600)
	tt.Advance(time.Millisecond)
	if err := tt.Click("筛选"); err != nil {
		t.Fatal(err)
	}
	if !a.LogFiltersOpen || !tt.HasText("时间与隧道") {
		t.Fatal("filter popover did not open", a.LogFiltersOpen, tt.Texts())
	}
	panel := paneRect(t, tt, "日志筛选")
	if panel.X < 0 || panel.Y < 0 || panel.X+panel.W > 900 || panel.Y+panel.H > 600 {
		t.Fatal("filter panel outside compact window", panel)
	}
	tt.Key(0, u.KeyEscape)
	if a.LogFiltersOpen || a.Search != "keep search" {
		t.Fatal("Escape leaked out of the filter popover", a.Search)
	}
	a.LogFrom = "invalid"
	tt.Frame()
	if !tt.HasText("开始时间请使用本地时间 YYYY-MM-DD HH:MM:SS") {
		t.Fatal("invalid filter has no visible explanation")
	}
	if err := tt.Click("筛选 · 已启用"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("重置时间与对象"); err != nil {
		t.Fatal(err)
	}
	if a.LogFrom != "" || !a.LogFiltersOpen || !tt.HasText("时间与隧道") {
		t.Fatal("filter reset lost its popover anchor")
	}
	tt.Key(0, u.KeyEscape)
}

func TestViewCommandsPreserveSidebarAndRespectSheets(t *testing.T) {
	a := paneApp(t)
	a.SidebarWidth = 260
	tt := u.NewTester(a.View, 1120, 760)
	tt.Key(u.Cmd|u.Ctrl, u.KeyS)
	if !a.SidebarHidden {
		t.Fatal("sidebar shortcut ignored")
	}
	if _, visible := tt.Find("连接侧栏"); visible {
		t.Fatal("hidden sidebar still in view")
	}
	tt.Key(u.Cmd|u.Ctrl, u.KeyS)
	if a.SidebarHidden || !closeSize(paneRect(t, tt, "连接侧栏").W, 260) {
		t.Fatal("sidebar width lost on reopen")
	}
	a.ProfileOpen = true
	a.ToggleSidebar()
	a.ToggleLogs()
	a.ToggleInspector(1120)
	if a.SidebarHidden || !a.ShowLogs || !a.ShowInspector {
		t.Fatal("view command changed layout under a sheet")
	}
	a.ProfileOpen = false
	a.ToggleInspector(900)
	a.ToggleInspector(900)
	if a.CompactInspector {
		t.Fatal("inspector command could not close its own compact panel")
	}
	a.Selected = "all"
	a.ShowLogs = false
	a.ToggleLogs()
	if a.ShowLogs {
		t.Fatal("log command opened logs without a connection")
	}
}
