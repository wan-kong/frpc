package ui

// uiDispatch captures a view lifetime on the UI thread. Workers only enqueue;
// the lifetime check and state writes both happen on the UI thread.
func (a *App) uiDispatch() func(func()) {
	if a.disposed {
		return nil
	}
	dispatch := a.dispatch
	if dispatch == nil && a.Window != nil {
		dispatch = a.Window.Update
	}
	if dispatch == nil {
		return nil
	}
	generation := a.viewGeneration
	return func(fn func()) {
		dispatch(func() {
			if !a.disposed && a.viewGeneration == generation {
				fn()
			}
		})
	}
}

// Dispose cancels view-owned work after the native window is destroyed. Hiding
// the main window deliberately keeps its drafts and connection control alive.
func (a *App) Dispose() {
	if a.disposed {
		return
	}
	a.disposed = true
	if a.Controller != nil {
		visible := a.SettingsWindow != nil && !a.SettingsWindow.IsDestroyed() && a.SettingsWindow.IsVisible()
		a.Controller.Runtime.SetObservationScope("", visible)
	}
	a.viewGeneration++
	if a.commandCancel != nil {
		a.commandCancel()
		a.commandCancel = nil
	}
	if a.CoreCancel != nil {
		a.CoreCancel()
		a.CoreCancel = nil
	}
	if a.UpdateCancel != nil {
		a.UpdateCancel()
		a.UpdateCancel = nil
	}
	if a.updateCheckCancel != nil {
		a.updateCheckCancel()
		a.updateCheckCancel = nil
	}
	a.CancelPendingUI()
	a.Busy, a.CoreBusy = false, false
}

// UpdateObservationScope runs on the UI thread. A visible settings window keeps
// background observations at five seconds while the hidden main window has no
// selected foreground connection.
func (a *App) UpdateObservationScope() {
	if a.disposed || a.Controller == nil {
		return
	}
	mainVisible := a.Window == nil || !a.Window.IsDestroyed() && a.Window.IsVisible()
	visible := mainVisible || a.SettingsWindow != nil && !a.SettingsWindow.IsDestroyed() && a.SettingsWindow.IsVisible()
	active := ""
	if mainVisible {
		active = a.Selected
	}
	a.Controller.Runtime.SetObservationScope(active, visible)
}
