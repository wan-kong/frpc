package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
	run "frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"
	view "frp-desktop/internal/ui"

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
	dir, err := platform.DataDir()
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
	controller := app.New(store, store, executable)
	logDir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		log.Fatal("无法确定日志目录：", err)
	}
	if err := controller.Runtime.SetLogDirectory(logDir); err != nil {
		log.Fatal("无法初始化日志目录：", err)
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
			detail := fmt.Sprintf("将断开 %d 个运行中的连接。", len(ids))
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
			err := controller.CloseForQuit(relaunch, ids)
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
	viewAction := func(action func(*view.App)) {
		if shell == nil {
			return
		}
		show()
		action(shell)
		shell.Window.Invalidate()
	}
	checkUpdates := func(*mygo.MenuItem, *mygo.Window) {
		if shell != nil {
			show()
			shell.CheckUpdates()
		}
	}
	settings := func(*mygo.MenuItem, *mygo.Window) {
		if shell != nil {
			shell.OpenSettings()
		}
	}
	quit := func(*mygo.MenuItem, *mygo.Window) { requestQuit(false) }
	fileMenu := &mygo.MenuItem{Label: "文件", Submenu: []*mygo.MenuItem{
		{Label: "新建隧道", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) {
			if shell != nil {
				show()
				shell.EditTunnel(-1, false)
			}
		}},
		{Label: "新建连接", Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) {
			if shell != nil {
				show()
				shell.EditProfile("")
			}
		}},
		{Label: "导入配置", Click: func(*mygo.MenuItem, *mygo.Window) {
			if shell != nil {
				show()
				shell.ImportDialog()
			}
		}}, {Label: "导出当前连接", Click: func(*mygo.MenuItem, *mygo.Window) {
			if shell != nil && !shell.HasModal() && !shell.Busy {
				show()
				shell.ExportDialog()
			}
		}}, {Role: mygo.RoleClose},
	}}
	var menus []*mygo.MenuItem
	if runtime.GOOS == "darwin" {
		menus = append(menus, &mygo.MenuItem{Label: "frp-desktop", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			{Label: "检查更新", Click: checkUpdates}, mygo.Separator(),
			{Label: "设置", Accelerator: "CmdOrCtrl+,", Click: settings}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			{Label: "退出 frp-desktop", Accelerator: "CmdOrCtrl+Q", Click: quit},
		}})
	} else {
		fileMenu.Submenu = append(fileMenu.Submenu, mygo.Separator(),
			&mygo.MenuItem{Label: "设置", Accelerator: "CmdOrCtrl+,", Click: settings},
			&mygo.MenuItem{Label: "退出 frp-desktop", Accelerator: "CmdOrCtrl+Q", Click: quit})
	}
	menus = append(menus, fileMenu, &mygo.MenuItem{Role: mygo.RoleEditMenu})
	sidebarAccel := "Cmd+Ctrl+S"
	if runtime.GOOS != "darwin" {
		sidebarAccel = "CmdOrCtrl+Alt+S"
	}
	menus = append(menus, &mygo.MenuItem{Label: "视图", Submenu: []*mygo.MenuItem{
		{Label: "切换侧栏", Accelerator: sidebarAccel, Click: func(*mygo.MenuItem, *mygo.Window) { viewAction((*view.App).ToggleSidebar) }},
		{Label: "切换检查器", Accelerator: "CmdOrCtrl+Alt+I", Click: func(*mygo.MenuItem, *mygo.Window) {
			viewAction(func(a *view.App) { width, _ := a.Window.ContentSize(); a.ToggleInspector(float32(width)) })
		}},
		{Label: "切换日志", Accelerator: "CmdOrCtrl+Alt+L", Click: func(*mygo.MenuItem, *mygo.Window) { viewAction((*view.App).ToggleLogs) }},
	}}, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	if runtime.GOOS != "darwin" {
		menus = append(menus, &mygo.MenuItem{Label: "帮助", Submenu: []*mygo.MenuItem{{Label: "检查更新", Click: checkUpdates}}})
	}
	mygo.App.SetMenu(mygo.NewMenu(menus))
	mygo.App.OnActivate(func(bool) { show() })
	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.OnWindowAllClosed(func() {})
	var refreshTray func()
	mygo.App.WhenReady(func() {
		shell = view.New(controller)
		shell.RequestQuit = requestQuit
		updateDark := func() {
			appearance := store.Snapshot().Settings.Appearance
			dark := appearance == "dark" || appearance != "light" && mygo.Theme.IsDark()
			mygo.RunOnMain(func() {
				if shell == nil || shell.Dark == dark {
					return
				}
				shell.Dark = dark
				if win := shell.Window; win != nil {
					win.Invalidate()
				}
				if shell.SettingsWindow != nil {
					shell.SettingsWindow.Invalidate()
				}
			})
		}
		mygo.Theme.OnUpdated(updateDark)
		updateDark()
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
				items = append(items, &mygo.MenuItem{Label: p.Name + "（" + view.ProcessLabel(r) + "）", Submenu: []*mygo.MenuItem{
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
			tray.SetToolTip(fmt.Sprintf("frp-desktop，%d 个运行连接，%d 个需关注", running, attention))
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
