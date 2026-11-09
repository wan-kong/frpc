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
	githubIcon    = u.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M12 .75a11.25 11.25 0 0 0-3.558 21.923c.563.104.768-.244.768-.542 0-.267-.01-.974-.015-1.912-3.13.68-3.79-1.508-3.79-1.508-.512-1.3-1.25-1.646-1.25-1.646-1.022-.7.077-.686.077-.686 1.13.08 1.725 1.16 1.725 1.16 1.005 1.723 2.637 1.225 3.28.937.102-.728.393-1.225.715-1.507-2.498-.284-5.124-1.249-5.124-5.56 0-1.229.44-2.233 1.16-3.02-.116-.285-.503-1.43.11-2.98 0 0 .945-.302 3.094 1.154A10.78 10.78 0 0 1 12 6.185c.956.005 1.919.13 2.818.378 2.148-1.456 3.092-1.154 3.092-1.154.614 1.55.227 2.695.112 2.98.721.787 1.158 1.791 1.158 3.02 0 4.322-2.63 5.273-5.136 5.552.404.35.764 1.043.764 2.1 0 1.516-.014 2.738-.014 3.11 0 .3.202.65.774.54A11.25 11.25 0 0 0 12 .75Z"/></svg>`))
	moonIcon      = lineIcon(`<path d="M20.5 13.1A8.5 8.5 0 0 1 10.9 3.5a8.5 8.5 0 1 0 9.6 9.6Z"/>`)
	sunIcon       = lineIcon(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>`)
	settingsIcon  = lineIcon(`<path d="m9 3-.5 2-2 1.2-2-.6-2 3.4L4 10.5v3L2.5 15l2 3.4 2-.6 2 1.2.5 2h4l.5-2 2-1.2 2 .6 2-3.4-1.5-1.5v-3L20.5 9l-2-3.4-2 .6-2-1.2-.5-2Z"/><circle cx="11.5" cy="12" r="3"/>`)
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
	case "已注册", "已连接", "运行中":
		return theme.Success
	case "待应用", "应用中", "注册中", "连接中", "正在连接", "正在停止", "重连中", "确认超时":
		return theme.Warning
	case "认证失败", "注册失败", "已断开", "连接失败":
		return theme.Danger
	case "停用", "已停止", "状态未确认", "状态已过期":
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
