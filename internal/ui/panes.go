package ui

import u "github.com/egoist/mygo/ui"

const (
	sidebarDefault   float32 = 220
	sidebarMinimum   float32 = 190
	sidebarMaximum   float32 = 300
	inspectorWidth   float32 = 300
	logDefaultHeight float32 = 220
	paneMinimum      float32 = 120
)

type paneLayoutKey struct {
	Version                uint64
	Scope, Notice          string
	Width, Height, Sidebar float32
}

func clampPane(value, lo, hi float32) float32 { return min(max(value, lo), hi) }

func (a *App) workspace(c *u.Context) {
	u.Column(c).Fill().MinHeight(0).Children(func() {
		u.Column(c).Grow(1).FillWidth().MinHeight(0).Children(func() { a.workspaceContent(c) })
		u.Divider(c)
		u.Row(c).FillWidth().Height(28).Shrink(0).Padding(0, 8).AlignItems(u.Center).Background(c.Theme().Surface).Label("状态栏").Children(func() {
			u.Spacer(c)
			_, hasProfile := a.current()
			hint := "显示日志"
			tint := c.Theme().TextMuted
			if a.ShowLogs && hasProfile {
				hint = "隐藏日志"
				tint = c.Theme().Accent
			}
			u.Button(c, "").Size(24, 24).Shrink(0).Padding(0).Background(u.Transparent).BorderColor(u.Transparent).Label("切换日志").Tooltip(hint).Disabled(!hasProfile).OnClick(a.ToggleLogs).Children(func() {
				u.Icon(c, logIcon).Size(14, 14).TextColor(tint)
			})
		})
	})
}

func (a *App) tunnelPane(c *u.Context) {
	u.Row(c.Key("tunnel-pane")).Grow(1).FillWidth().MinHeight(0).AlignItems(u.Stretch).Label("隧道工作区").Children(func() {
		a.tunnelTable(c)
		width, _ := c.Size()
		if a.ShowInspector && width >= 1040 {
			u.Divider(c)
			a.inspector(c)
		}
	})
}

func (a *App) contentPanes(c *u.Context, logs bool, primary func()) {
	body := u.Column(c.Key("content-panes")).Grow(1).MinHeight(0).Label("连接工作区")
	height := body.LayoutBounds().H
	width, windowHeight := c.Size()
	key := paneLayoutKey{a.viewVersion, a.Selected, a.Notice, width, windowHeight, a.SidebarWidth}
	previous := a.logLayoutKey
	if logs && key != previous {
		// Account for window-height changes immediately. Reflow in headers (for
		// example a wrapped error) gets one measured follow-up, never an idle loop.
		if previous.Height > 0 && a.logRegionHeight > 0 {
			height += windowHeight - previous.Height
		}
		a.logLayoutKey = key
		c.After(0)
	}
	body.Children(func() {
		if !logs {
			a.logRegionHeight = 0
			a.logLayoutKey = paneLayoutKey{}
			primary()
			return
		}
		if a.LogHeight <= 0 {
			a.LogHeight = logDefaultHeight
		}
		if height <= 0 {
			// The first layout uses normal flex sizing, so logs already have their
			// requested height. A single follow-up frame knows the real content box.
			primary()
			u.Divider(c)
			u.Box(c).Height(a.LogHeight).Shrink(1).MinHeight(0).Children(func() { a.logView(c) })
			c.After(0)
			return
		}
		minimum := min(paneMinimum, max(float32(40), (height-1)/2))
		maxTop := max(minimum, height-1-minimum)
		if a.logRegionHeight != height {
			a.logRegionHeight = height
			a.logTopHeight = clampPane(height-1-a.LogHeight, minimum, maxTop)
		}
		u.SplitVertical(c.Key("logs-split"), &a.logTopHeight, func() { primary() }, func() { a.logView(c) }).Fill().MinHeight(0).OnChange(func() {
			a.logTopHeight = clampPane(a.logTopHeight, minimum, maxTop)
			a.LogHeight = max(float32(0), height-1-a.logTopHeight)
		})
	})
}
