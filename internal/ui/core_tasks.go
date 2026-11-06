package ui

import (
	"context"
	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"time"
)

func (a *App) CoreTask(label string, job func(context.Context) error, after func()) {
	if a.CoreBusy || a.disposed {
		return
	}
	dispatch := a.uiDispatch()
	if dispatch == nil {
		a.Notice = "当前窗口不可用"
		return
	}
	a.CoreBusy = true
	a.Notice = label
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	a.CoreCancel = cancel
	go func() {
		defer cancel()
		err := job(ctx)
		dispatch(func() {
			a.CoreBusy = false
			a.CoreCancel = nil
			a.Refresh()
			if err != nil {
				a.Notice = err.Error()
			} else {
				a.Notice = ""
				if after != nil {
					after()
				}
			}
		})
	}()
}
func (a *App) InstallCore() {
	version := a.CoreVersion
	cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: a.State.CoreRevision, Action: app.InstallCore{Version: version}}
	a.CoreTask("从 GitHub 下载并核验内核", func(ctx context.Context) error { _, err := a.Controller.ExecuteGlobal(ctx, cmd); return err }, nil)
}
