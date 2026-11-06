package ui

import (
	"context"
	"fmt"
	u "github.com/egoist/mygo/ui"
	"time"
)

func (a *App) CheckUpdates() {
	if a.Updates == nil || a.Busy || a.HasModal() || a.disposed {
		return
	}
	dispatch := a.uiDispatch()
	if dispatch == nil {
		return
	}
	a.UpdateOpen = true
	manager := a.Updates
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.updateCheckCancel = cancel
	go func() {
		defer cancel()
		_ = manager.Check(ctx, true)
		dispatch(func() { a.updateCheckCancel = nil; a.UpdateState = manager.Snapshot() })
	}()
}
func (a *App) saveUpdateChecks(value bool) {
	if a.Updates == nil {
		return
	}
	a.saveUpdateChange(func() error { return a.Updates.SetAutomaticChecks(value) })
}
func (a *App) saveUpdateDownloads(value bool) {
	if a.Updates == nil {
		return
	}
	a.saveUpdateChange(func() error { return a.Updates.SetAutomaticDownloads(value) })
}
func (a *App) saveUpdateChange(change func() error) {
	a.Do("保存更新偏好", func(context.Context) error { return change() }, func() {
		a.UpdateState = a.Updates.Snapshot()
		if a.SettingsWindow != nil {
			a.SettingsWindow.Invalidate()
		}
	})
}
func (a *App) updateModal(c *u.Context) {
	modal := u.Modal(c, &a.UpdateOpen, func() {
		s := a.UpdateState
		u.Column(c).Width(520).MaxHeight(540).Gap(16).Children(func() {
			title := "应用更新"
			switch s.Stage {
			case "checking":
				title = "正在检查更新"
			case "current":
				title = "当前已是最新版本"
			case "available":
				title = "发现新版本 " + s.Version
			case "installing":
				title = "正在下载并验证更新"
			case "ready":
				title = "更新已准备好"
			case "disabled":
				title = "此运行模式不可自更新"
			case "error":
				title = "更新未完成"
			}
			u.Text(c, title).FontSize(20).Bold()
			if s.Error != "" {
				u.Text(c, s.Error).TextColor(c.Theme().Danger).Wrap()
			}
			if s.Notes != "" {
				u.Scroll(c).MaxHeight(270).Children(func() { u.Text(c, s.Notes).Wrap() })
			}
			if s.Stage == "installing" {
				if s.Total > 0 {
					u.Text(c, fmt.Sprintf("%.1f / %.1f MiB", float64(s.Downloaded)/(1<<20), float64(s.Total)/(1<<20)))
				} else {
					u.Text(c, fmt.Sprintf("已下载 %.1f MiB", float64(s.Downloaded)/(1<<20)))
				}
			}
			if s.Stage == "ready" {
				u.Text(c, "新版本将在重启后运行。现在重启会中断连接，确认后自动恢复此前运行的连接。").Wrap()
			}
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				switch s.Stage {
				case "available":
					u.Button(c, "跳过此版本").OnClick(func() {
						manager := a.Updates
						a.Do("保存跳过版本偏好", func(context.Context) error { return manager.Skip() }, func() { a.UpdateOpen = false })
					})
					u.Button(c, "稍后提醒").OnClick(func() { a.Updates.Dismiss(); a.UpdateOpen = false })
					u.PrimaryButton(c, "安装更新").OnClick(func() {
						manager := a.Updates
						dispatch := a.uiDispatch()
						if dispatch == nil {
							return
						}
						ctx, cancel := context.WithCancel(context.Background())
						a.UpdateCancel = cancel
						go func() {
							_ = manager.Install(ctx)
							dispatch(func() { a.UpdateCancel = nil; a.UpdateState = manager.Snapshot() })
						}()
					})
				case "installing":
					u.Button(c, "取消下载").OnClick(func() {
						if a.UpdateCancel != nil {
							a.UpdateCancel()
						}
					})
				case "ready":
					u.Button(c, "下次启动生效").OnClick(func() { a.UpdateOpen = false })
					u.PrimaryButton(c, "重启并恢复连接").OnClick(func() {
						if a.RequestQuit != nil {
							a.RequestQuit(true)
						}
					})
				default:
					u.Button(c, "关闭").OnClick(func() { a.UpdateOpen = false })
				}
			})
		})
	})
	if modal.Dismissed() && a.Updates != nil {
		a.Updates.Dismiss()
	}
}
