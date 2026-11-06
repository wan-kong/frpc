package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

// CheckUpdates asks MyGo's built-in updater for the GitHub release feed.
// The app keeps its own quit coordinator, so the updater never relaunches
// the app itself: installing an update offers a restart through
// RequestQuit, which resumes the connections that were running.
func (a *App) CheckUpdates() {
	if a.Busy || a.HasModal() || a.disposed {
		return
	}
	dispatch := a.uiDispatch()
	if dispatch == nil {
		return
	}
	a.UpdateOpen = true
	a.UpdateStage = "checking"
	a.UpdateError, a.UpdateVersion, a.UpdateNotes = "", "", ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.updateCheckCancel = cancel
	go func() {
		defer cancel()
		if !mygo.Updater.Enabled() {
			dispatch(func() {
				a.updateCheckCancel = nil
				a.UpdateStage = "disabled"
				a.UpdateError = "此运行模式不可自更新；发行版需在 mygo.json 配置 updates.publicKey 与 updates.github。"
			})
			return
		}
		up, err := mygo.Updater.Check(ctx)
		dispatch(func() {
			a.updateCheckCancel = nil
			switch {
			case err != nil:
				a.UpdateStage = "error"
				a.UpdateError = err.Error()
			case up == nil:
				a.UpdateStage = "current"
			default:
				a.UpdateStage = "available"
				a.UpdateVersion = up.Version
				a.UpdateNotes = up.Notes
				a.updateRelease = up
			}
		})
	}()
}

func (a *App) installUpdate() {
	if a.updateRelease == nil || a.UpdateStage == "installing" {
		return
	}
	dispatch := a.uiDispatch()
	if dispatch == nil {
		return
	}
	a.UpdateStage = "installing"
	a.UpdateError = ""
	a.UpdateDownloaded, a.UpdateTotal = 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	a.UpdateCancel = cancel
	release := a.updateRelease
	go func() {
		defer cancel()
		err := release.Install(ctx, func(downloaded, total int64) {
			dispatch(func() {
				a.UpdateDownloaded, a.UpdateTotal = downloaded, total
			})
		})
		dispatch(func() {
			a.UpdateCancel = nil
			if err != nil {
				a.UpdateStage = "error"
				a.UpdateError = err.Error()
				return
			}
			a.UpdateStage = "ready"
		})
	}()
}

func (a *App) updateModal(c *u.Context) {
	u.Modal(c, &a.UpdateOpen, func() {
		u.Column(c).Width(520).MaxHeight(540).Gap(16).Children(func() {
			title := "应用更新"
			switch a.UpdateStage {
			case "checking":
				title = "正在检查更新"
			case "current":
				title = "当前已是最新版本"
			case "available":
				title = "发现新版本 " + a.UpdateVersion
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
			if a.UpdateError != "" {
				u.Text(c, a.UpdateError).TextColor(c.Theme().Danger).Wrap()
			}
			if a.UpdateNotes != "" {
				u.Scroll(c).MaxHeight(270).Children(func() { u.Text(c, a.UpdateNotes).Wrap() })
			}
			if a.UpdateStage == "installing" {
				if a.UpdateTotal > 0 {
					u.Text(c, fmt.Sprintf("%.1f / %.1f MiB", float64(a.UpdateDownloaded)/(1<<20), float64(a.UpdateTotal)/(1<<20)))
				} else {
					u.Text(c, fmt.Sprintf("已下载 %.1f MiB", float64(a.UpdateDownloaded)/(1<<20)))
				}
			}
			if a.UpdateStage == "ready" {
				u.Text(c, "新版本将在重启后运行。现在重启会中断连接，确认后自动恢复此前运行的连接。").Wrap()
			}
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				switch a.UpdateStage {
				case "available":
					u.Button(c, "稍后").OnClick(func() { a.UpdateOpen = false })
					u.PrimaryButton(c, "安装更新").OnClick(a.installUpdate)
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
}
