package ui

func (a *App) ToggleInspector(width float32) {
	if a.disposed || a.HasModal() && !a.CompactInspector || a.Selected == "cores" {
		return
	}
	if a.CompactInspector {
		a.CompactInspector = false
		return
	}
	if width < 1040 {
		a.CompactInspector = !a.CompactInspector
	} else {
		a.ShowInspector = !a.ShowInspector
	}
}

func (a *App) ToggleSidebar() {
	if !a.disposed && !a.HasModal() {
		a.SidebarHidden = !a.SidebarHidden
	}
}

func (a *App) ToggleLogs() {
	if a.disposed || a.HasModal() {
		return
	}
	if _, ok := a.current(); !ok {
		return
	}
	a.ShowLogs = !a.ShowLogs
	a.Refresh()
}
