package ui

func (a *App) ToggleInspector(width float32) {
	if width < 1040 {
		a.CompactInspector = !a.CompactInspector
	} else {
		a.ShowInspector = !a.ShowInspector
	}
}
