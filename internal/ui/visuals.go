package ui

import (
	u "github.com/egoist/mygo/ui"
	"time"
)

const paneMotionDuration = 150 * time.Millisecond

// Original 24-DIP line glyphs share stroke geometry and inherit theme colors.
func lineIcon(paths string) *u.SVG {
	return u.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var (
	inspectorIcon = lineIcon(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M14 4v16"/>`)
	logIcon       = lineIcon(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 8h1m3 0h6M7 12h1m3 0h6M7 16h1m3 0h6"/>`)
	connectIcon   = lineIcon(`<path d="m9 5 10 7-10 7Z"/>`)
	stopIcon      = lineIcon(`<rect x="6" y="6" width="12" height="12" rx="1"/>`)
	addIcon       = lineIcon(`<path d="M12 5v14M5 12h14"/>`)
	importIcon    = lineIcon(`<path d="M14 3h5a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-5M3 12h12m-5-5 5 5-5 5"/>`)
	filterIcon    = lineIcon(`<path d="M3 6h18M6 12h12M10 18h4"/>`)
)

func iconButton(c *u.Context, label string, icon *u.SVG) u.Element {
	return u.Button(c, "").Label(label).Children(func() {
		u.Icon(c, icon).Size(14, 14).Shrink(0)
		u.Text(c, label).SingleLine()
	})
}

func primaryIconButton(c *u.Context, label string, icon *u.SVG) u.Element {
	return u.PrimaryButton(c, "").Label(label).Children(func() {
		u.Icon(c, icon).Size(14, 14).Shrink(0)
		u.Text(c, label).SingleLine()
	})
}

func statusTint(theme *u.Theme, status string, hasError bool) u.Color {
	switch status {
	case "已注册", "已登录（日志确认）", "运行中 · 已确认代理注册":
		return theme.Success
	case "待应用", "应用中", "注册中", "启动中", "停止中", "重连中", "观察超时":
		return theme.Warning
	case "认证失败", "注册失败", "异常退出":
		return theme.Danger
	case "停用", "已停止", "状态未确认", "状态已过期", "运行中 · 连接状态未确认":
		return theme.TextMuted
	default:
		if hasError {
			return theme.Danger
		}
		return theme.TextMuted
	}
}

func paneTransition(c *u.Context) u.ElementTransition {
	// Presence moves; divider resizing must continue following the pointer.
	motion := u.ElementTransition{Duration: paneMotionDuration, Colors: true}
	if !c.Preferences().ReduceMotion {
		motion.Enter = &u.Motion{Collapse: true, Opacity: 1}
		motion.Exit = &u.Motion{Collapse: true, Opacity: 1}
	}
	return motion
}
