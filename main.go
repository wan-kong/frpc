package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
	run "frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"
	view "frp-desktop/internal/ui"
	"frp-desktop/internal/update"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--frpc-session" {
		os.Exit(run.Session(os.Args[2:]))
	}
	mygo.App.SetName("frp-desktop")
	mygo.App.SetLocale("zh-Hans")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	store, err := storage.Open(dir)
	if err != nil {
		view.RunRecovery(dir, err)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	controller := app.New(store, storage.Keychain{}, executable)
	updates, err := update.New(dir, update.MyGoEngine{})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var shell *view.App
	var tray *mygo.Tray
	quitting, cleaned := false, false
	systemQuit := false
	show := func() {
		if shell != nil && shell.Window != nil {
			shell.Refresh()
			shell.Window.Show()
			shell.Window.Focus()
		}
	}
	requestQuit := func(relaunch bool) {
		if quitting {
			return
		}
		ids := []string{}
		for id, r := range controller.Runtime.Snapshot() {
			if r.Running() {
				ids = append(ids, id)
			}
		}
		dirty := shell != nil && shell.HasUnsavedChanges()
		if view.QuitNeedsConfirmation(len(ids), dirty, systemQuit, store.Snapshot().Settings.ConfirmQuit, relaunch) {
			label := "退出并停止连接"
			if relaunch {
				label = "重启并恢复连接"
			}
			detail := fmt.Sprintf("将中断 %d 个运行连接。", len(ids))
			if dirty {
				detail += " 未保存的编辑和本次会话保留的草稿将在退出后丢失。"
			}
			r, err := mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Message: label + "？", Detail: detail, Buttons: []string{"取消", label}, DefaultButton: 0, CancelButton: 0})
			if err != nil || r.Button != 1 {
				return
			}
		}
		if shell != nil {
			shell.CancelPendingUI()
			if shell.CoreCancel != nil {
				shell.CoreCancel()
			}
			if shell.UpdateCancel != nil {
				shell.UpdateCancel()
			}
		}
		quitting = true
		go func() {
			if relaunch {
				if err := controller.WriteResume(ids); err != nil {
					mygo.RunOnMain(func() {
						quitting = false
						if shell != nil {
							shell.Notice = "无法保存恢复记录：" + err.Error()
							shell.Window.Invalidate()
						}
					})
					return
				}
			}
			err := controller.Runtime.Close()
			mygo.RunOnMain(func() {
				if err != nil {
					quitting = false
					shell.Notice = "退出清理失败：" + err.Error()
					shell.Window.Invalidate()
					return
				}
				cleaned = true
				cancel()
				if relaunch {
					mygo.App.Relaunch()
				} else {
					mygo.App.Quit()
				}
			})
		}()
	}
	terminationSignals := make(chan os.Signal, 2)
	signal.Notify(terminationSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(terminationSignals)
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if !cleaned {
			e.PreventDefault()
			systemQuit = platform.SystemTerminationRequested()
			select {
			case <-terminationSignals:
				systemQuit = true
			default:
			}
			requestQuit(false)
			systemQuit = false
		}
	})
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "frp-desktop", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) {
				if shell != nil {
					show()
					shell.CheckUpdates()
				}
			}}, mygo.Separator(),
			{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) {
				if shell != nil {
					shell.OpenSettings()
				}
			}}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			{Label: "退出 frp-desktop", Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) { requestQuit(false) }},
		}},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "新建隧道…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) {
				if shell != nil {
					show()
					shell.EditTunnel(-1, false)
				}
			}},
			{Label: "新建连接…", Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) {
				if shell != nil {
					show()
					shell.EditProfile("")
				}
			}},
			{Label: "导入配置…", Click: func(*mygo.MenuItem, *mygo.Window) {
				if shell != nil {
					show()
					shell.ImportDialog()
				}
			}}, {Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu}, {Role: mygo.RoleWindowMenu},
	}))
	mygo.App.OnActivate(func(bool) { show() })
	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.OnWindowAllClosed(func() {})
	var refreshTray func()
	mygo.App.WhenReady(func() {
		shell = view.New(controller)
		shell.Updates = updates
		shell.UpdateState = updates.Snapshot()
		shell.RequestQuit = requestQuit
		win := mygo.NewWindow(mygo.WindowOptions{Title: "frp-desktop", Width: 1120, Height: 760, MinWidth: 900, MinHeight: 600, StateKey: "main", Hidden: mygo.App.WasOpenedAtLogin(), Content: ui.View(shell.View)})
		shell.Window = win
		win.OnClosed(shell.Dispose)
		win.OnShow(shell.Refresh)
		win.OnHide(shell.UpdateObservationScope)
		shell.UpdateObservationScope()
		win.OnClose(func(e *mygo.CloseEvent) {
			if !quitting {
				e.PreventDefault()
				shell.HideMainWindow()
			}
		})
		controller.Runtime.OnChange = func() {
			win.Update(func() {
				if win.IsVisible() {
					shell.Refresh()
				}
				if refreshTray != nil {
					refreshTray()
				}
				if shell.SettingsWindow != nil {
					shell.SettingsWindow.Invalidate()
				}
			})
		}
		updates.OnChange = func() {
			win.Update(func() {
				shell.UpdateState = updates.Snapshot()
				if shell.UpdateState.Offer && !shell.HasModal() && !shell.Busy {
					shell.UpdateOpen = true
					show()
				}
				if shell.SettingsWindow != nil {
					shell.SettingsWindow.Invalidate()
				}
			})
		}
		tray, err = mygo.NewTray(mygo.TrayOptions{ID: "frp-desktop", Icon: platform.TrayIcon(), IconIsTemplate: true, ToolTip: "frp-desktop", Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "显示 frp-desktop", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
			{Label: "停止全部连接", Click: func(*mygo.MenuItem, *mygo.Window) {
				var profiles []domain.Profile
				runtime := controller.Runtime.Snapshot()
				for _, p := range controller.Store.Snapshot().Profiles {
					if runtime[p.ID].Running() {
						profiles = append(profiles, p)
					}
				}
				shell.RunProfiles("停止全部连接", profiles, app.StopProfile{})
			}}, mygo.Separator(),
			{Label: "退出 frp-desktop", Click: func(*mygo.MenuItem, *mygo.Window) { requestQuit(false) }},
		})})
		if err != nil {
			shell.Notice = "菜单栏图标不可用：" + err.Error()
		}
		_ = tray
		refreshTray = func() {
			if tray == nil {
				return
			}
			state := controller.Store.Snapshot()
			runtime := controller.Runtime.Snapshot()
			items := []*mygo.MenuItem{{Label: "显示 frp-desktop", Click: func(*mygo.MenuItem, *mygo.Window) { show() }}, mygo.Separator()}
			running := 0
			attention := 0
			for _, p := range state.Profiles {
				p := p
				r := runtime[p.ID]
				if r.Running() {
					running++
				}
				if r.Error != "" {
					attention++
				}
				items = append(items, &mygo.MenuItem{Label: p.Name + " · " + view.ProcessLabel(r), Submenu: []*mygo.MenuItem{
					{Label: "显示连接", Click: func(*mygo.MenuItem, *mygo.Window) { shell.SelectScope(p.ID); show(); win.Invalidate() }},
					{Label: "连接", Disabled: r.Running(), Click: func(*mygo.MenuItem, *mygo.Window) {
						shell.RunProfile("启动连接", p, app.StartProfile{})
					}},
					{Label: "停止", Disabled: !r.Running(), Click: func(*mygo.MenuItem, *mygo.Window) {
						shell.RunProfile("停止连接", p, app.StopProfile{})
					}},
				}})
			}
			items = append(items, mygo.Separator(), &mygo.MenuItem{Label: "连接全部", Click: func(*mygo.MenuItem, *mygo.Window) {
				profiles := controller.Store.Snapshot().Profiles
				shell.RunProfiles("启动全部连接", profiles, app.StartProfile{})
			}}, &mygo.MenuItem{Label: "停止全部连接", Click: func(*mygo.MenuItem, *mygo.Window) {
				var profiles []domain.Profile
				runtime := controller.Runtime.Snapshot()
				for _, p := range controller.Store.Snapshot().Profiles {
					if runtime[p.ID].Running() {
						profiles = append(profiles, p)
					}
				}
				shell.RunProfiles("停止全部连接", profiles, app.StopProfile{})
			}})
			items = append(items, mygo.Separator(), &mygo.MenuItem{Label: "退出 frp-desktop", Click: func(*mygo.MenuItem, *mygo.Window) { requestQuit(false) }})
			tray.SetMenu(mygo.NewMenu(items))
			tray.SetToolTip(fmt.Sprintf("frp-desktop · %d 个运行连接 · %d 个需关注", running, attention))
		}
		refreshTray()
		resources, err := mygo.App.Path(mygo.PathResources)
		if err != nil {
			shell.Notice = err.Error()
			return
		}
		shell.Do("准备离线 frpc 内核", func(ctx context.Context) error {
			if err := controller.Bootstrap(ctx, resources); err != nil {
				return err
			}
			return controller.CollectSecrets()
		}, func() {
			shell.Refresh()
			resume := controller.ConsumeResume()
			wanted := map[string]bool{}
			for _, id := range resume {
				wanted[id] = true
			}
			var ids []string
			for _, p := range shell.State.Profiles {
				if wanted[p.ID] || p.AutoConnect {
					ids = append(ids, p.ID)
				}
			}
			go func() {
				if err := controller.StartMany(ctx, ids); err != nil {
					win.Update(func() { shell.Notice = err.Error() })
				}
			}()

		})
		go updates.Run(ctx)
		settings := store.Snapshot().Settings
		switch settings.Appearance {
		case "dark":
			mygo.Theme.SetSource(mygo.ThemeDark)
		case "light":
			mygo.Theme.SetSource(mygo.ThemeLight)
		}
	})
	if err = mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
