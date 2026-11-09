package ui

import (
	"fmt"
	"testing"
	"time"

	"frp-desktop/internal/domain"
	u "github.com/egoist/mygo/ui"
)

func TestStatusColorsDistinguishVerifiedPendingFailureAndUnknown(t *testing.T) {
	for _, theme := range []*u.Theme{u.LightTheme(), u.DarkTheme()} {
		for _, item := range []struct {
			status string
			detail bool
			want   u.Color
		}{
			{"已注册", false, theme.Success},
			{"待应用", false, theme.Warning},
			{"重连中", false, theme.Warning},
			{"确认超时", true, theme.Warning},
			{"注册失败", false, theme.Danger},
			{"认证失败", true, theme.Danger},
			{"状态已过期", false, theme.TextMuted},
			{"已停止", false, theme.TextMuted},
		} {
			if got := statusTint(theme, item.status, item.detail); got != item.want {
				t.Fatal(item.status, got)
			}
		}
	}
	r := domain.Runtime{Process: "running", Session: "connected", ObservedAt: time.Now().Add(-11 * time.Second)}
	if ProcessLabel(r) != "运行中" {
		t.Fatal("stale observation retained a green process label")
	}
	r.ObservedAt = time.Now()
	if ProcessLabel(r) != "运行中" {
		t.Fatal("fresh observation was hidden")
	}
}

func TestPaneMotionSettlesAndReducedMotionIsImmediate(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, width := range []int{900, 1120} {
			t.Run(fmt.Sprintf("%d/%t", width, reduced), func(t *testing.T) {
				a := paneApp(t)
				a.ShowLogs, a.ShowInspector = false, false
				tt := u.NewTester(a.View, width, 760)
				tt.SetPreferences(u.Preferences{ReduceMotion: reduced})
				tt.Advance(200 * time.Millisecond)
				a.ToggleLogs()
				tt.Frame()
				tt.Advance(20 * time.Millisecond)
				if tt.FramePending() == reduced {
					t.Fatal("log reveal ignored motion preference", reduced)
				}
				tt.Advance(200 * time.Millisecond)
				if tt.FramePending() {
					t.Fatal("log reveal did not settle")
				}
				a.ToggleInspector(float32(width))
				tt.Frame()
				tt.Advance(20 * time.Millisecond)
				if tt.FramePending() == reduced {
					t.Fatal("inspector reveal ignored motion preference", reduced)
				}
				tt.Advance(200 * time.Millisecond)
				if tt.FramePending() {
					t.Fatal("inspector reveal did not settle")
				}
				if a.CompactInspector {
					a.ToggleInspector(float32(width))
					tt.Frame()
					tt.Advance(200 * time.Millisecond)
				}
				// Rapidly close and reopen with persistent panel preferences.
				a.ToggleLogs()
				tt.Frame()
				a.ToggleLogs()
				tt.Frame()
				tt.Advance(200 * time.Millisecond)
				if tt.FramePending() || !closeSize(a.LogHeight, logDefaultHeight) {
					t.Fatal("reopened pane kept animating or lost height")
				}
			})
		}
	}
}

func TestCompactInspectorAdaptsWhenWindowWidens(t *testing.T) {
	a := paneApp(t)
	a.ShowLogs, a.ShowInspector = false, false
	selected := a.Selected
	tt := u.NewTester(a.View, 900, 600)
	a.ToggleInspector(900)
	tt.Frame()
	tt.Advance(20 * time.Millisecond)
	if !a.CompactInspector {
		t.Fatal("compact inspector did not open")
	}
	tt.SetSize(1120, 760)
	tt.Advance(200 * time.Millisecond)
	if a.CompactInspector || !a.ShowInspector || a.HasModal() || a.Selected != selected {
		t.Fatal("inspector did not adapt without losing selection")
	}
	count := 0
	for _, text := range tt.Texts() {
		if text == "隧道检查器" {
			count++
		}
	}
	if count != 1 || tt.FramePending() {
		t.Fatal("inspector presentation duplicated or kept animating", count)
	}
}
