package ui

func (a *App) SelectScope(id string) {
	a.Selected = id
	a.SelectedRow = -1
	a.Selection.Clear()
	a.Search = ""
	a.Logs = nil
	a.rowCacheKey = ""
	a.UpdateObservationScope()
}
