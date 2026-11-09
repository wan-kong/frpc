package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"frp-desktop/internal/app"
	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	"frp-desktop/internal/platform"
	"frp-desktop/internal/runtime"
	"frp-desktop/internal/storage"

	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

type App struct {
	Dark              bool
	hideNoticeSeen    bool
	disposed          bool
	viewGeneration    uint64
	commandCancel     context.CancelFunc
	updateCheckCancel context.CancelFunc

	logSearchTimer      *time.Timer
	logSearchGeneration uint64
	logAppliedSearch    string
	logFilterKey        string
	logFiltered         []logging.Line
	toggleTimer         *time.Timer
	toggleGeneration    uint64
	toggleWaiting       bool
	pendingToggles      map[string]domain.Profile

	ResidualOpen bool
	Residual     runtime.ResidualReview
	dispatch     func(func())
	profileEditor
	tunnelEditor
	rawEditor
	profileDrafts    map[string]profileEditor
	tunnelDrafts     map[string]tunnelEditor
	rawDrafts        map[string]rawEditor
	DraftCloseOpen   bool
	DraftCloseKind   string
	ProfileAdvanced  bool
	TunnelAdvanced   bool
	FormField        string
	CoreBusy         bool
	CoreCancel       context.CancelFunc
	CoreReleases     []string
	MoveOpen         bool
	MoveDestination  string
	rowCache         []row
	rowCacheKey      string
	viewVersion      uint64
	LocalPorts       []platform.LocalPort
	PortsOpen        bool
	UpdateOpen       bool
	UpdateStage      string
	UpdateVersion    string
	UpdateNotes      string
	UpdateError      string
	UpdateDownloaded int64
	UpdateTotal      int64
	updateRelease    *mygo.Update
	RequestQuit      func(bool)
	UpdateCancel     context.CancelFunc
	Controller       *app.Controller
	Window           *mygo.Window
	State            domain.State
	Runtime          map[string]domain.Runtime
	Selected         string
	Search           string
	Notice           string
	Busy             bool
	ShowLogs         bool
	ShowInspector    bool
	CompactInspector bool
	VisitorTab       bool
	SidebarWidth     float32
	LogHeight        float32
	logTopHeight     float32
	logRegionHeight  float32
	logLayoutKey     paneLayoutKey
	SelectedRow      int
	Table            u.ListState
	Selection        u.Selection[string]
	Sort             u.SortOrder
	LogList          u.ListState
	SearchHandle     u.Handle
	LogSearch        string
	LogLevel         string
	LogPaused        bool
	LogFiltersOpen   bool
	LogFrom          string
	LogUntil         string
	LogTunnel        string
	SidebarHidden    bool
	Logs             []logging.Line
	ProfileOpen      bool

	FormError  string
	TunnelOpen bool

	RawOpen bool

	BackupOpen          bool
	BackupRestore       bool
	BackupPassword      string
	BackupPasswordAgain string
	DownloadProxySaved  string
	SettingsOpen        bool
	Settings            domain.Settings
	DownloadProxy       string
	CoreVersion         string
	BackupData          []byte
	SettingsWindow      *mygo.Window
}

func New(c *app.Controller) *App {
	a := &App{Controller: c, SidebarWidth: sidebarDefault, LogHeight: logDefaultHeight, SelectedRow: -1, LogLevel: "全部", CoreVersion: domain.CertifiedCore}
	a.Refresh()
	return a
}
func (a *App) Refresh() {
	if a.disposed {
		return
	}
	changed := false
	if a.State.SchemaVersion == 0 || a.Controller.Store.Revision() != a.State.Revision {
		a.State = a.Controller.Store.Snapshot()
		a.State.Profiles = SortedProfiles(a.State.Profiles)
		changed = true
	}
	runtime := a.Controller.Runtime.Snapshot()
	if !reflect.DeepEqual(runtime, a.Runtime) {
		a.Runtime = runtime
		changed = true
	}
	if changed {
		a.viewVersion++
	}
	if a.Selected == "" && len(a.State.Profiles) > 0 {
		a.Selected = a.State.Profiles[0].ID
	}
	if a.ShowLogs && !a.LogPaused && domain.ValidID(a.Selected) {
		lines, err := a.Controller.Runtime.Logs(a.Selected)
		a.Logs = lines
		if err != nil {
			a.Notice = "日志写入失败：" + err.Error()
		}
	}
	a.UpdateObservationScope()
}
func (a *App) PostRefresh() {
	w := a.Window
	if w != nil {
		w.Update(a.Refresh)
	}
}
func (a *App) Do(label string, job func(context.Context) error, after func()) {
	a.DoResult(label, job, func(err error) {
		if err == nil && after != nil {
			after()
		}
	})
}
func (a *App) DoResult(label string, job func(context.Context) error, complete func(error)) {
	if a.Busy || a.disposed {
		return
	}
	a.Busy = true
	a.Notice = label
	dispatch := a.uiDispatch()
	if dispatch == nil {
		a.Busy = false
		a.Notice = "当前窗口不可用"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	a.commandCancel = cancel
	go func() {
		defer cancel()
		err := job(ctx)
		dispatch(func() {
			a.commandCancel = nil
			a.Busy = false
			a.Refresh()
			if err != nil {
				a.Notice = err.Error()
				a.FormError = err.Error()
				a.FormField = ""
				if info, ok := domain.ErrorInfo(err); ok {
					a.FormField = info.FieldPath
				}
			} else {
				a.Notice = ""
				a.FormError = ""

			}
			if complete != nil {
				complete(err)
			}
			if a.SettingsWindow != nil {
				a.SettingsWindow.Invalidate()
			}
		})
	}()
}
func copyProfile(p domain.Profile) domain.Profile {
	b, _ := json.Marshal(p)
	var out domain.Profile
	_ = json.Unmarshal(b, &out)
	return out
}
func (a *App) current() (domain.Profile, bool) { return a.State.Profile(a.Selected) }
func (a *App) View(c *u.Context) {
	c.SetTheme(appTheme(a.Dark))
	width, _ := c.Size()
	if width >= 1040 && a.CompactInspector {
		a.CompactInspector, a.ShowInspector = false, true
	}
	if a.Search != "" {
		c.OnShortcut(0, u.KeyEscape, func() {
			if !a.HasModal() && a.Search != "" {
				a.Search = ""
				a.SelectedRow = -1
				a.Selection.Clear()
			}
		})
	}
	c.OnShortcut(u.Cmd, u.KeyN, func() { a.EditTunnel(-1, false) })
	c.OnShortcut(u.Cmd|u.Shift, u.KeyN, func() { a.EditProfile("") })
	c.OnShortcut(u.Cmd, u.KeyF, func() { a.SearchHandle.Focus(c) })
	c.OnShortcut(u.Cmd|u.Alt, u.KeyL, a.ToggleLogs)
	sidebarMods := u.Cmd | u.Ctrl
	if goruntime.GOOS != "darwin" {
		// Cmd is Control on Windows, where Ctrl+S would be ambiguous.
		sidebarMods = u.Cmd | u.Alt
	}
	c.OnShortcut(sidebarMods, u.KeyS, a.ToggleSidebar)
	c.OnShortcut(u.Cmd|u.Alt, u.KeyI, func() { width, _ := c.Size(); a.ToggleInspector(width) })
	u.Column(c).Fill().Children(func() {
		a.SidebarWidth = clampPane(a.SidebarWidth, sidebarMinimum, sidebarMaximum)
		if a.SidebarHidden {
			u.Column(c.Key("workspace-only")).Grow(1).Children(func() { a.workspace(c) })
		} else {
			u.Split(c, &a.SidebarWidth, func() { a.sidebar(c) }, func() { a.workspace(c) }).Grow(1).OnChange(func() { a.SidebarWidth = clampPane(a.SidebarWidth, sidebarMinimum, sidebarMaximum) })
		}
		a.draftMenu(c)
		if a.Notice != "" {
			u.Text(c, a.Notice).FontSize(12).TextColor(c.Theme().TextMuted).Padding(8, 16).MaxHeight(60)
		}
	})
	a.updateModal(c)
	a.profileModal(c)
	a.tunnelModal(c)
	a.portsModal(c)
	a.rawModal(c)
	a.backupModal(c)
	a.draftCloseModal(c)
	a.residualModal(c)
	a.moveModal(c)
	u.Modal(c, &a.CompactInspector, func() { a.inspector(c) })
}
func (a *App) sidebar(c *u.Context) {
	u.Column(c).Fill().Background(c.Theme().Surface).Padding(12, 8, 8, 8).Gap(4).Label("连接侧栏").Children(func() {
		iconButton(c, "添加连接", addIcon).Disabled(a.Busy).OnClick(func() { a.EditProfile("") })
		u.Text(c, "连接").FontSize(12).TextColor(c.Theme().TextMuted).Padding(4, 4)
		if len(a.State.Profiles) > 0 {
			a.scopeButton(c, "全部连接", "all")
			a.scopeButton(c, "需要关注", "attention")
			u.Scroll(c).Grow(1).Children(func() {
				group := "\x00"
				for _, p := range a.State.Profiles {
					if p.Group != group {
						group = p.Group
						label := group
						if label == "" {
							label = "我的连接"
						}
						u.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Padding(6, 4)
					}
					rt := a.Runtime[p.ID]
					marker := "○"
					if rt.Running() {
						marker = "●"
					}
					if rt.Process == "failed" || rt.Error != "" || rt.SessionError != "" {
						marker = "!"
					}
					label := ProcessLabel(rt)
					tint := statusTint(c.Theme(), label, rt.Error != "")
					if a.Selected == p.ID {
						tint = c.Theme().AccentText
					}
					button := u.Button(c.Key(p.ID), "").Label(p.Name+"，"+label).FillWidth().Children(func() {
						u.Text(c, marker).TextColor(tint).Width(12)
						u.Text(c, p.Name).SingleLine().Grow(1)
					}).Margin(0, 0, 4, 0)

					if a.Selected == p.ID {
						button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText).BorderColor(c.Theme().Accent)
					}
					button.OnClick(func() { a.SelectScope(p.ID); a.Refresh() })
				}
			})
		} else {
			u.Text(c, "还没有连接").TextColor(c.Theme().TextMuted).Padding(12, 8).Grow(1)
		}
		kernel := u.Button(c, "frp 内核").FillWidth()
		if a.Selected == "cores" {
			kernel.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
		}
		kernel.OnClick(func() { a.SelectScope("cores") })
		version := mygo.App.Version()
		versionLabel := "开发版"
		if version != "" {
			versionLabel = "版本 v" + strings.TrimPrefix(version, "v")
		}
		u.Row(c).FillWidth().Shrink(0).AlignItems(u.Center).Padding(6, 4).Gap(8).Children(func() {
			u.Text(c, versionLabel).Grow(1).MinWidth(0).SingleLine().FontSize(12).TextColor(c.Theme().TextMuted).Label("应用版本：" + versionLabel)
			themeLabel, themeIcon := "切换到深色", moonIcon
			if a.Dark {
				themeLabel, themeIcon = "切换到浅色", sunIcon
			}
			u.Button(c, "").Size(16, 16).Shrink(0).Padding(0).Background(u.Transparent).BorderColor(u.Transparent).Label(themeLabel).Tooltip(themeLabel).Disabled(a.Busy).OnClick(a.ToggleAppearance).Children(func() {
				u.Icon(c, themeIcon).Size(16, 16).Shrink(0)
			})
		})
	})
}
func (a *App) scopeButton(c *u.Context, label, id string) {
	button := u.Button(c, label).FillWidth()
	if a.Selected == id {
		button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
	}
	button.OnClick(func() { a.SelectScope(id) })
}
func (a *App) workspaceContent(c *u.Context) {
	u.Column(c).Fill().Children(func() {
		if a.Selected == "cores" {
			a.cores(c)
			return
		}
		p, has := a.current()
		title := "全部连接"
		if len(a.State.Profiles) == 0 {
			title = "开始使用"
		}
		if has {
			title = p.Name
		} else if a.Selected == "attention" {
			title = "需要关注"
		}
		u.Row(c).Padding(16).Gap(8).AlignItems(u.Center).Children(func() {
			u.Text(c, title).FontSize(20).Bold().Grow(1)
			if len(a.State.Profiles) > 0 {
				if u.SearchField(c, &a.Search).Placeholder("搜索名称").Width(210).Bind(&a.SearchHandle).Changed() {
					a.Selection.Clear()
					a.SelectedRow = -1
				}
				iconButton(c, "检查器", inspectorIcon).OnClick(func() { width, _ := c.Size(); a.ToggleInspector(width) })
			}
		})
		if len(a.State.Profiles) == 0 {
			u.Column(c).Grow(1).Center().Gap(12).Children(func() {
				u.Text(c, "先添加一个连接").FontSize(20).Bold()
				u.Text(c, "填写 frps 服务器地址和 Token，再把本机的服务添加为隧道。").TextColor(c.Theme().TextMuted)
				u.Text(c, "还没有服务器？先在一台有公网 IP 的电脑上运行 frps。").FontSize(12).TextColor(c.Theme().TextMuted)
				u.Row(c).Gap(8).Children(func() {
					primaryIconButton(c, "添加连接", addIcon).Disabled(a.Busy).OnClick(func() { a.EditProfile("") })
					iconButton(c, "导入 frpc.toml", importIcon).Disabled(a.Busy).OnClick(a.ImportDialog)
				})
			})
			return
		}
		if has {
			rt := a.Runtime[p.ID]
			u.Row(c).Padding(0, 16, 12, 16).Gap(8).AlignItems(u.Center).Children(func() {
				u.Text(c, domain.Endpoint(p.ServerAddr, p.ServerPort)).Font("monospace").TextColor(c.Theme().TextMuted).Grow(1)
				if rt.Running() {
					iconButton(c, "停止", stopIcon).Disabled(a.Busy).OnClick(func() {
						a.RunProfile("正在停止连接", p, app.StopProfile{})
					})
				} else {
					primaryIconButton(c, "连接", connectIcon).Disabled(a.Busy).OnClick(func() {
						a.RunProfile("正在启动连接", p, app.StartProfile{})
					})
				}
				u.Button(c, "编辑连接").Disabled(a.Busy).OnClick(func() { a.EditProfile(p.ID) })
				u.Button(c, "查看配置").Disabled(!has || a.Busy).OnClick(a.OpenRaw)
			})

			if rt.SessionError != "" {
				u.Text(c, rt.SessionError).TextColor(c.Theme().Danger).Padding(0, 16, 8, 16).MaxHeight(70)
			}
			if rt.Error != "" {
				u.Text(c, rt.Error).TextColor(c.Theme().Danger).Padding(0, 16, 8, 16)
			}
			if rt.Running() && rt.AppliedRevision != p.Revision {
				u.Row(c).Padding(0, 16, 8, 16).Gap(8).Children(func() {
					u.Text(c, "有更改尚未应用").Grow(1)
					u.Button(c, "应用更改").Disabled(a.Busy).OnClick(func() { a.RunProfile("正在应用配置", p, app.ApplyProfile{}) })
				})
			}
		}
		u.Toolbar(c, func() {
			tab := 0
			if a.VisitorTab {
				tab = 1
			}
			if u.Segmented(c, &tab, "隧道", "访问者").Label("条目类型").Changed() {
				a.VisitorTab = tab == 1
				a.SelectedRow = -1
				a.Selection.Clear()
			}
			u.Spacer(c)
			addLabel := "添加隧道"
			if a.VisitorTab {
				addLabel = "添加访问者"
			}
			u.Button(c, addLabel).Disabled(!has || p.Mode == "raw" || a.Busy).OnClick(func() { a.EditTunnel(-1, a.VisitorTab) })
			u.Button(c, "启用所选").Disabled(a.Busy || a.Selection.Len() == 0).OnClick(func() { a.BatchToggle(true) })
			u.Button(c, "停用所选").Disabled(a.Busy || a.Selection.Len() == 0).OnClick(func() { a.BatchToggle(false) })
			u.MenuButton(c, "更多", a.entryMenu)
		}).Padding(0, 16, 8, 16).Gap(8)
		a.contentPanes(c, a.ShowLogs && has, func() {
			if has && p.Mode == "raw" {
				u.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
					u.Column(c).Padding(24).Gap(12).Children(func() {
						u.Text(c, "此连接使用原文配置").FontSize(18).Bold()
						u.Text(c, "单条隧道开关和热重载不可用；修改后需重新连接。").Wrap()
						u.Button(c, "编辑原文").OnClick(a.OpenRaw)
					})
				})
			} else {
				a.tunnelPane(c)
			}
		})
	})
}

type row struct {
	Profile   domain.Profile
	Tunnel    domain.Tunnel
	Visitor   domain.Visitor
	IsVisitor bool
	Status    string
	Err       string
}

func (a *App) rows() []row {
	stale, expired := 0, 0
	for _, rt := range a.Runtime {
		if rt.Running() && rt.ObservationExpired(time.Now()) {
			expired++
		}
		if rt.Running() && (rt.ObservedAt.IsZero() || time.Since(rt.ObservedAt) > 10*time.Second) {
			stale++
		}
	}
	key := fmt.Sprintf("%d/%s/%s/%t/%d/%d", a.viewVersion, a.Selected, a.Search, a.VisitorTab, stale, expired)
	if a.rowCacheKey == key && a.rowCache != nil {
		return a.rowCache
	}
	capacity := 0
	for _, p := range a.State.Profiles {
		if a.Selected == "all" || a.Selected == "attention" || p.ID == a.Selected {
			if a.VisitorTab {
				capacity += len(p.Visitors)
			} else {
				capacity += len(p.Tunnels)
			}
		}
	}
	out := make([]row, 0, capacity)
	query := strings.ToLower(a.Search)
	for _, p := range a.State.Profiles {
		if a.Selected != "all" && a.Selected != "attention" && a.Selected != p.ID {
			continue
		}
		rt := a.Runtime[p.ID]
		if a.VisitorTab {
			for _, v := range p.Visitors {
				r := visitorRow(p, v, rt, time.Now())
				if _, pending := a.pendingToggles[p.ID]; pending {
					r.Status = "待应用"
				}
				if a.Selected == "attention" && r.Err == "" && r.Status != "待应用" && r.Status != "状态未确认" && r.Status != "状态已过期" && rt.Error == "" && rt.SessionError == "" {
					continue
				}
				if query == "" || strings.Contains(strings.ToLower(v.Name+v.Type+v.ServerName), query) {
					out = append(out, r)
				}
			}
			continue
		}
		for _, t := range p.Tunnels {
			r := tunnelRow(p, t, rt, time.Now())
			if _, pending := a.pendingToggles[p.ID]; pending {
				r.Status = "待应用"
			}
			if a.Selected == "attention" && r.Err == "" && r.Status != "待应用" && r.Status != "状态未确认" && r.Status != "状态已过期" && rt.Error == "" && rt.SessionError == "" {
				continue
			}
			if query == "" || strings.Contains(strings.ToLower(t.Name+t.Type+t.LocalIP+config.Remote(t, p)+p.Name), query) {
				out = append(out, r)
			}
		}
	}
	a.rowCacheKey = key
	a.rowCache = out
	return out
}
func (a *App) tunnelTable(c *u.Context) {
	rows := a.rows()
	if len(rows) == 0 {
		a.tunnelEmptyState(c)
		return
	}
	a.sortRows(rows)
	a.Table.Selected = &a.SelectedRow
	a.Table.Selection = &a.Selection
	a.Table.Sort = &a.Sort
	a.Table.Key = func(i int) any {
		if rows[i].IsVisitor {
			return rows[i].Visitor.ID
		}
		return rows[i].Tunnel.ID
	}
	localTitle, remoteTitle := "本地端点", "远程端点"
	if a.VisitorTab {
		localTitle, remoteTitle = "本地监听", "服务名称"
	}
	showProfile := a.Selected == "all" || a.Selected == "attention"
	cols := []u.TableColumn{
		{Title: "名称", MinWidth: 110, Sortable: true},
		{Title: "协议", Width: 64, Sortable: true},
		{Title: localTitle, Width: 150},
		{Title: remoteTitle, MinWidth: 110},
	}
	if showProfile {
		cols = append(cols, u.TableColumn{Title: "连接", Width: 100})
	}
	cols = append(cols, u.TableColumn{Title: "状态", Width: 96, Sortable: true})
	statusCol := len(cols) - 1
	tableLabel := "隧道列表"
	if a.VisitorTab {
		tableLabel = "访问者列表"
	}
	var table u.Element
	u.Themed(c, tableTheme(c.Theme().Dark)).Grow(1).FillHeight().MinHeight(0).Children(func() {
		table = u.Table(c.Key("tunnels"), &a.Table, cols, len(rows), func(i, j int) {
			r := rows[i]
			var text string
			if r.IsVisitor {
				v := r.Visitor
				switch j {
				case 0:
					text = v.Name
				case 1:
					text = strings.ToUpper(v.Type)
				case 2:
					text = domain.Endpoint(v.BindAddr, v.BindPort)
				case 3:
					text = v.ServerName
				default:
					if showProfile && j == 4 {
						text = r.Profile.Name
					} else {
						text = r.Status
					}
				}
			} else {
				t := r.Tunnel
				switch j {
				case 0:
					text = t.Name
				case 1:
					text = strings.ToUpper(t.Type)
				case 2:
					text = domain.Endpoint(t.LocalIP, t.LocalPort)
				case 3:
					text = config.Remote(t, r.Profile)
				default:
					if showProfile && j == 4 {
						text = r.Profile.Name
					} else {
						text = r.Status
					}
				}
			}
			el := u.Text(c, text).SingleLine()
			if j == 2 || j == 3 {
				el.Font("monospace")
			}
			if j == statusCol {
				el.TextColor(statusTint(c.Theme(), r.Status, r.Err != ""))
			}
		}).Grow(1).FillWidth().FillHeight().MinHeight(0).Label(tableLabel)
	})
	if table.Submitted() && a.SelectedRow >= 0 && a.SelectedRow < len(rows) {
		r := rows[a.SelectedRow]
		a.Selected = r.Profile.ID
		if r.IsVisitor {
			for i, v := range r.Profile.Visitors {
				if v.ID == r.Visitor.ID {
					a.EditTunnel(i, true)
					break
				}
			}
		} else {
			for i, t := range r.Profile.Tunnels {
				if t.ID == r.Tunnel.ID {
					a.EditTunnel(i, false)
					break
				}
			}
		}
	}
}

func (a *App) tunnelEmptyState(c *u.Context) {
	if a.Selected == "attention" {
		u.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
			u.Text(c, "没有需要关注的条目").FontSize(18).Bold()
			normal := "所有隧道都正常，或已经停用。"
			if a.VisitorTab {
				normal = "所有访问者都正常，或已经停用。"
			}
			u.Text(c, normal).TextColor(c.Theme().TextMuted)
		})
		return
	}
	if _, hasProfile := a.current(); !hasProfile && len(a.State.Profiles) > 0 {
		first := a.State.Profiles[0]
		u.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
			u.Text(c, "先选择一个连接").FontSize(18).Bold()
			u.Text(c, "在左侧选择连接后，就能查看它的隧道和访问者。").TextColor(c.Theme().TextMuted)
			u.Button(c, "打开「"+first.Name+"」").OnClick(func() { a.SelectScope(first.ID); a.Refresh() })
		})
		return
	}
	if a.Search != "" {
		u.Column(c).Grow(1).Center().Padding(24).Children(func() {
			u.Text(c, "没有匹配「"+a.Search+"」的条目").TextColor(c.Theme().TextMuted)
			u.Button(c, "清除搜索").OnClick(func() { a.Search = ""; a.SelectedRow = -1; a.Selection.Clear() })
		})
		return
	}
	u.Column(c).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		if a.VisitorTab {
			u.Text(c, "还没有访问者").FontSize(18).Bold()
			u.Text(c, "访问者用来连接别人通过私密隧道分享的服务。").TextColor(c.Theme().TextMuted)
			u.Button(c, "添加访问者").Disabled(a.Busy).OnClick(func() { a.EditTunnel(-1, true) })
			return
		}
		u.Text(c, "还没有隧道").FontSize(18).Bold()
		u.Text(c, "把本机的服务添加为隧道，连接后即可从外网访问。").TextColor(c.Theme().TextMuted)
		u.Button(c, "添加隧道").Disabled(a.Busy).OnClick(func() { a.EditTunnel(-1, false) })
	})
}
func (a *App) inspector(c *u.Context) {
	panel := u.Column(c.Key("inspector")).Width(inspectorWidth).Padding(16).Gap(12).Label("隧道检查器").Transition(paneTransition(c))
	if a.CompactInspector {
		intro := panel.Timeline("compact-inspector-appear", u.TimelineOptions{Run: 1})
		shown := intro.Tween(u.Tween{From: 0, To: 1, Duration: paneMotionDuration}, u.AtTime(0))
		panel.Opacity(shown).Translate(0, (1-shown)*4)
	}
	panel.Children(func() {
		rows := a.rows()
		a.sortRows(rows)
		if a.SelectedRow < 0 || a.SelectedRow >= len(rows) {
			label := "选择一条隧道查看详情"
			if a.VisitorTab {
				label = "选择一条访问者查看详情"
			}
			u.Text(c, label).TextColor(c.Theme().TextMuted)
			return
		}
		r := rows[a.SelectedRow]
		name := r.Tunnel.Name
		if r.IsVisitor {
			name = r.Visitor.Name
		}
		u.Text(c, name).FontSize(18).Bold()
		u.Text(c, "连接："+r.Profile.Name)
		u.Text(c, "状态："+r.Status).TextColor(statusTint(c.Theme(), r.Status, r.Err != ""))
		if r.Err != "" {
			u.Text(c, r.Err).TextColor(c.Theme().Danger).Wrap()
		}
		if !r.IsVisitor && r.Status == "已注册" {
			u.Text(c, "注册成功表示服务器已接受这条隧道；外网能否访问请实际测试。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
		}
		if r.IsVisitor && r.Status == "已连接" {
			u.Text(c, "本地端口已打开；对方服务是否在线请实际访问测试。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
		}
		u.Button(c, "编辑").Disabled(a.Busy).OnClick(func() {
			a.Selected = r.Profile.ID
			index := -1
			if r.IsVisitor {
				for i, v := range r.Profile.Visitors {
					if v.ID == r.Visitor.ID {
						index = i
					}
				}
			} else {
				for i, t := range r.Profile.Tunnels {
					if t.ID == r.Tunnel.ID {
						index = i
					}
				}
			}
			a.EditTunnel(index, r.IsVisitor)
		})
		if !r.IsVisitor {
			remote := config.Remote(r.Tunnel, r.Profile)
			if remote != "由服务端分配" && remote != "私密代理" {
				u.Button(c, "复制远程地址").OnClick(func() { mygo.Clipboard.WriteText(remote) })
			}
			if r.Tunnel.Type != "udp" && r.Tunnel.Type != "sudp" {
				u.Button(c, "检查本地端口").Disabled(a.Busy).OnClick(func() { addr := domain.Endpoint(r.Tunnel.LocalIP, r.Tunnel.LocalPort); a.TestEndpoint(addr) })
			}
		}
		enabled := r.Tunnel.Enabled
		if r.IsVisitor {
			enabled = r.Visitor.Enabled
		}
		label := "停用"
		if !enabled {
			label = "启用"
		}
		u.Button(c, label).Disabled(a.Busy && !a.toggleWaiting).OnClick(func() {
			id := r.Tunnel.ID
			if r.IsVisitor {
				id = r.Visitor.ID
			}
			a.QueueTunnelToggle(r.Profile.ID, id, r.IsVisitor, !enabled)
		})
	})
}
func (a *App) logView(c *u.Context) {
	panel := u.Column(c.Key("log-panel")).Grow(1).FillWidth().MinHeight(0).Padding(8, 16).Gap(8).Label("日志面板")
	intro := panel.Timeline("log-appear", u.TimelineOptions{Run: 1})
	shown := intro.Tween(u.Tween{From: 0, To: 1, Duration: paneMotionDuration}, u.AtTime(0))
	panel.Opacity(shown).Translate(0, (1-shown)*4)
	panel.Children(func() {
		u.Row(c).Gap(8).Children(func() {
			u.Text(c, "日志").Bold()
			u.Select(c, &a.LogLevel, []string{"全部", "错误", "警告", "信息", "调试", "跟踪"}).Width(96)
			u.SearchField(c, &a.LogSearch).Placeholder("过滤日志").Grow(1).OnChange(a.queueLogSearch)
			a.logFilters(c)
			u.Checkbox(c, &a.LogPaused, "暂停跟随")
			u.Button(c, "复制").OnClick(a.CopyLogs)
			u.Button(c, "导出").OnClick(a.ExportLogs)
		})
		lines := a.filteredLogs()
		if _, _, err := a.logTimeBounds(); err != nil {
			u.Text(c, err.Error()).TextColor(c.Theme().Danger).Wrap()
		}
		if a.LogTunnel != "" && a.LogTunnel != "全部日志" {
			if _, valid := a.logTunnelTag(); !valid {
				u.Text(c, "关联对象已变更，请重新选择或重置筛选").TextColor(c.Theme().Danger).Wrap()
			}
		}
		a.LogList.Key = func(i int) any { return lines[i].Seq }
		a.LogList.FollowEnd = !a.LogPaused
		u.List(c.Key("logs"), &a.LogList, len(lines), func(i int) {
			l := lines[i]
			u.Text(c, l.Time.Format("15:04:05")+"  "+l.Text).Font("monospace").FontSize(12).SingleLine().Selectable()
		}).Grow(1).Label("连接日志")
	})
}
func ProcessLabel(r domain.Runtime) string {
	switch r.Process {
	case "starting":
		return "正在连接"
	case "stopping":
		return "正在停止"
	case "failed":
		return "已断开"
	case "running":
		if r.Session == "authentication_failed" {
			return "认证失败"
		}
		if r.Session == "reconnecting" {
			return "重连中"
		}
		return "运行中"
	default:
		return "已停止"
	}
}

func (a *App) EditProfile(id string) {
	if a.Busy || a.HasModal() {
		return
	}
	key := id
	if key == "" {
		key = "new"
	}
	if draft, ok := a.profileDrafts[key]; ok {
		a.profileEditor = draft
		delete(a.profileDrafts, key)
		a.ProfileOpen = true
		return
	}
	a.profileKey = key
	p := domain.NewProfile()
	if id != "" {
		if x, ok := a.State.Profile(id); ok {
			p = copyProfile(x)
		}
	} else {
		for _, co := range a.State.Cores {
			if co.Certified {
				p.CoreID = co.ID
				break
			}
		}
	}
	if id == "" {
		if co, ok := a.State.Core(a.State.Settings.DefaultCoreID); ok && co.Certified {
			p.CoreID = co.ID
		}
	}
	a.ProfileDraft = p
	a.ProfilePort = strconv.Itoa(p.ServerPort)
	a.ProfileCore = a.coreChoice(p.CoreID)
	a.ProfileToken = ""
	a.ProfileProxy = ""
	b, _ := json.MarshalIndent(p.Metadata, "", "  ")
	a.ProfileMetadata = string(b)
	a.FormError = ""
	a.FormField = ""
	a.ProfileAdvanced = p.Group != "" || p.Transport != "" && p.Transport != "tcp" || !p.TLS || !p.VerifyTLS
	if id == "" {
		a.profileBaseline = a.profileStamp()
		a.ProfileOpen = true
		return
	}
	var token, proxy, metadataText string
	a.Do("读取连接凭证", func(context.Context) error {
		var err error
		token, err = storage.Secret(a.Controller.Secrets, p.TokenRef)
		if err != nil {
			return err
		}
		proxy, err = storage.Secret(a.Controller.Secrets, p.ProxyURLRef)
		if err != nil {
			return err
		}
		metadata := map[string]string{}
		for k, ref := range p.MetadataRefs {
			value, err := storage.Secret(a.Controller.Secrets, ref)
			if err != nil {
				return err
			}
			metadata[k] = value
		}
		encoded, _ := json.MarshalIndent(metadata, "", "  ")
		metadataText = string(encoded)
		return nil
	}, func() {
		a.ProfileMetadata, a.ProfileToken, a.ProfileProxy = metadataText, token, proxy
		a.profileBaseline = a.profileStamp()
		a.ProfileOpen = true
	})
}

func (a *App) profileModal(c *u.Context) {
	m := u.Modal(c, &a.ProfileOpen, func() {
		u.Column(c).Width(560).MaxHeight(590).Gap(12).Children(func() {
			title := "新建连接"
			if _, exists := a.State.Profile(a.ProfileDraft.ID); exists {
				title = "编辑连接"
			}
			u.Text(c, title).FontSize(20).Bold()
			if a.ProfileDraft.CoreVersion != "" && a.ProfileCore == "" {
				u.Text(c, "此连接需要 frpc "+a.ProfileDraft.CoreVersion+"，请先安装或重新选择版本。").TextColor(c.Theme().Danger).Wrap()
			}
			u.Scroll(c).Grow(1).Children(func() {
				u.Form(c, func() {
					field(c, "名称", &a.ProfileDraft.Name)
					if a.ProfileDraft.Mode == "managed" {
						a.profileField(c, "服务器地址", &a.ProfileDraft.ServerAddr, "serverAddr")
						a.profileField(c, "服务器端口", &a.ProfilePort, "serverPort")
						u.Field(c, "Token", func() { u.TextInput(c, &a.ProfileToken).Password() }).Description("与服务器 frps.toml 中的 token 一致")
						u.Field(c, "用户前缀", func() { u.TextInput(c, &a.ProfileDraft.User).FillWidth() }).Description("多人共用同一服务器时用于区分，可留空")
					}
					ids := []string{}
					for _, co := range a.State.Cores {
						ids = append(ids, a.coreChoice(co.ID))
					}
					coreHint := ""
					if len(ids) > 0 {
						coreHint = a.coreDescription(a.coreChoiceID(a.ProfileCore))
					}
					u.Field(c, "frpc 版本", func() {
						if len(ids) == 0 {
							u.Text(c, "正在准备内置版本").TextColor(c.Theme().TextMuted)
							return
						}
						u.Select(c, &a.ProfileCore, ids)
					}).Description(coreHint)
					if a.ProfileDraft.Mode == "managed" {
						u.Collapsible(c, "高级连接设置", &a.ProfileAdvanced, func() {
							u.Field(c, "分组", func() { u.TextInput(c, &a.ProfileDraft.Group).FillWidth() }).Description("同一分组的连接会显示在一起")
							u.Field(c, "传输方式", func() { u.Select(c, &a.ProfileDraft.Transport, []string{"tcp", "kcp", "quic", "websocket", "wss"}) })
							u.Field(c, "加密连接", func() { u.Checkbox(c, &a.ProfileDraft.TLS, "使用 TLS") })
							u.Field(c, "证书校验", func() { u.Checkbox(c, &a.ProfileDraft.VerifyTLS, "验证服务器证书") })
							field(c, "证书名称", &a.ProfileDraft.TLSServerName)
							u.Field(c, "自定义 CA", func() {
								u.Row(c).Gap(8).Children(func() {
									u.Button(c, "导入 PEM").Disabled(a.Busy).OnClick(a.ImportCA)
									u.Button(c, "清除").Disabled(a.ProfileDraft.TrustedCA == "").OnClick(func() { a.ProfileDraft.TrustedCA = "" })
								})
							}).Description(filepath.Base(a.ProfileDraft.TrustedCA))
							u.Field(c, "连接代理", func() { u.TextInput(c, &a.ProfileProxy).Password() }).Description("仅用于连接服务器，支持 HTTP 和 SOCKS5")
							u.Field(c, "Metadata JSON", func() { u.TextArea(c, &a.ProfileMetadata).Height(75).Font("monospace") })
						})
					}
					u.Field(c, "自动连接", func() { u.Checkbox(c, &a.ProfileDraft.AutoConnect, "打开应用时自动连接") })
				})
			})
			if !a.ProfileDraft.TLS {
				u.Text(c, "未加密的连接可能被窃听。").TextColor(c.Theme().Warning).Wrap()
			} else if !a.ProfileDraft.VerifyTLS {
				u.Text(c, "已关闭服务器证书校验，连接可能被冒充。").TextColor(c.Theme().Warning).Wrap()
			}
			a.formError(c)
			u.Row(c).Gap(8).Children(func() {
				if a.ProfileDraft.Revision > 0 {
					u.Button(c, "删除连接").Disabled(a.Busy).OnClick(a.DeleteCurrent).TextColor(c.Theme().Danger)
					if !a.Runtime[a.ProfileDraft.ID].Running() {
						u.Button(c, "检查后台进程").Disabled(a.Busy).OnClick(func() { a.InspectResidual(a.ProfileDraft.ID) })
					}
					u.Button(c, "复制连接").Disabled(a.Busy).OnClick(func() {
						cmd := profileCommand(a.ProfileDraft, app.DuplicateProfile{})
						a.Do("复制连接", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.requestEditorClose("profile") })
					})
				}
				u.Spacer(c)
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.requestEditorClose("profile") })
				u.Button(c, "保存").Disabled(a.Busy).OnClick(func() { a.SaveProfile(false) })
				connectLabel := "保存并连接"
				if a.Runtime[a.ProfileDraft.ID].Running() {
					connectLabel = "保存并应用"
				}
				u.PrimaryButton(c, connectLabel).Disabled(a.Busy).OnClick(func() { a.SaveProfile(true) })
			})
		})
	})
	if m.Dismissed() {
		a.ProfileOpen = true
		a.requestEditorClose("profile")
	}
}
func (a *App) SaveProfile(start bool) {
	p := copyProfile(a.ProfileDraft)
	port, err := strconv.Atoi(a.ProfilePort)
	if err != nil {
		a.FormError = "服务器端口只能填数字"
		a.FormField = "serverPort"
		return
	}
	p.ServerPort = port
	coreID := a.coreChoiceID(a.ProfileCore)
	if coreID == "" {
		a.FormError = "frpc 版本尚未准备好，请稍候再试"
		a.FormField = ""
		return
	}
	p.CoreID = coreID
	p.Metadata = map[string]string{}
	if strings.TrimSpace(a.ProfileMetadata) != "" && a.ProfileMetadata != "null" {
		if err = json.Unmarshal([]byte(a.ProfileMetadata), &p.Metadata); err != nil {
			a.FormError = "Metadata 需为 JSON 对象，例如 {\"key\": \"value\"}"
			return
		}
	}
	values := map[string]string{"token": a.ProfileToken, "proxy": a.ProfileProxy}
	submitted := a.profileEditor
	stamp := submitted.profileStamp()
	var saved domain.Profile
	persisted := false
	shouldSave := p.Revision == 0 || stamp != a.profileBaseline
	if !shouldSave && !start {
		a.finishEditor("profile", false)
		a.SelectScope(p.ID)
		return
	}
	after := ""
	if start {
		after = "connect"
	}
	cmd := saveIntent(p, values, shouldSave, after)
	a.DoResult("校验并保存连接", func(ctx context.Context) error {
		result, err := a.Controller.Execute(ctx, cmd)
		persisted = result.Persisted
		if persisted {
			saved, _ = result.State.Profile(p.ID)
		}
		return err
	}, func(err error) {
		if persisted {
			baseline := stamp
			if saved.CoreID != p.CoreID {
				// A failed switch restores the persisted core. Preserve the user's
				// choice as an edit rather than marking that rejected choice saved.
				submitted.ProfileCore = a.coreChoice(saved.CoreID)
				baseline = submitted.profileStamp()
			}
			a.syncProfileSave(saved, baseline)
		}
		if err == nil && a.profileStamp() == stamp {
			a.finishEditor("profile", false)
			a.SelectScope(p.ID)
		} else if persisted && err != nil {
			a.FormError = "配置已保存，应用未完成：" + err.Error()
		}
	})
}
func field(c *u.Context, label string, value *string) {
	u.Field(c, label, func() { u.TextInput(c, value).FillWidth() })
}
func (a *App) formError(c *u.Context) {
	if a.FormError != "" {
		u.Text(c, a.FormError).TextColor(c.Theme().Danger).Wrap().MaxHeight(85)
	}
}
func (a *App) coreDescription(id string) string {
	co, ok := a.State.Core(id)
	if !ok {
		return "请选择一个已安装的版本"
	}
	switch {
	case co.Bundled:
		return "内置版本，可离线使用，" + co.Arch
	case co.Certified:
		return "已通过校验，" + co.Arch
	default:
		return "未认证，仅支持原文模式，" + co.Arch
	}
}

func (a *App) DeleteCurrent() {
	p := a.ProfileDraft
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "删除连接「" + p.Name + "」？", Detail: "会停止并断开这个连接的隧道。7 天内可以在设置中恢复。", Buttons: []string{"取消", "删除连接"}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}})
	if err != nil || r.Button != 1 {
		return
	}
	cmd := profileCommand(p, app.DeleteProfile{})
	a.Do("停止并删除连接", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.finishEditor("profile", false); a.Selected = "all" })
}
func (a *App) TestEndpoint(addr string) {
	a.Do("检查本地端口", func(ctx context.Context) error { return testEndpoint(ctx, addr) }, func() { a.Notice = "本地端口可以连接；外网访问尚未验证。" })
}

func (a *App) ImportDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "导入 frpc.toml", Filters: []mygo.FileFilter{{Name: "TOML 配置", Extensions: []string{"toml"}}}})
	if err != nil {
		a.Notice = err.Error()
		return
	}
	if len(paths) == 0 {
		return
	}
	a.previewImport(paths[0])
}
func (a *App) OpenRaw() {
	if a.HasModal() || a.Busy {
		return
	}
	p, ok := a.current()
	if !ok {
		return
	}
	if draft, ok := a.rawDrafts[p.ID]; ok {
		a.rawEditor = draft
		delete(a.rawDrafts, p.ID)
		a.RawOpen = true
		return
	}
	var text string
	a.Do("读取配置预览", func(context.Context) error { var err error; text, err = a.Controller.Raw(p.ID); return err }, func() { a.RawProfile = p; a.RawText = text; a.rawBaseline = digest(text); a.RawOpen = true })
}
func (a *App) rawModal(c *u.Context) {
	modal := u.Modal(c, &a.RawOpen, func() {
		u.Column(c).Width(650).Height(490).Gap(12).Children(func() {
			title := "配置文件"
			if a.RawProfile.Mode == "raw" {
				title = "编辑原文"
			}
			u.Text(c, title).FontSize(18).Bold()
			if a.RawProfile.Mode != "raw" {
				u.Text(c, "此连接由表单管理，配置文件只读，且不包含凭证。").FontSize(12).TextColor(c.Theme().TextMuted)
			}
			u.TextArea(c, &a.RawText).Font("monospace").Grow(1).ReadOnly(a.RawProfile.Mode != "raw")
			a.formError(c)
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				u.Button(c, "关闭").OnClick(func() { a.requestEditorClose("raw") })
				if a.RawProfile.Mode == "raw" {
					u.PrimaryButton(c, "保存").Disabled(a.Busy).OnClick(func() {
						p, text := a.RawProfile, a.RawText
						stamp := digest(text)
						cmd := profileCommand(p, app.SaveRawProfile{Profile: p, Text: text})
						var result app.CommandResult
						a.DoResult("校验并保存原文", func(ctx context.Context) error {
							var err error
							result, err = a.Controller.Execute(ctx, cmd)
							return err
						}, func(err error) {
							if err != nil {
								return
							}
							saved, ok := result.State.Profile(p.ID)
							if ok {
								a.RawProfile = saved
							}
							a.rawBaseline = stamp
							if digest(a.RawText) == stamp {
								a.finishEditor("raw", false)
							} else {
								a.Notice = "已保存先前修改；新的编辑仍未保存"
							}
						})
					})
				}
				if a.RawProfile.Mode == "managed" {
					u.Button(c, "改为编辑原文").Disabled(a.Busy).OnClick(a.ConvertToRaw)
				}
				u.Button(c, "导出配置").OnClick(a.ExportDialog)
			})
		})
	})
	if modal.Dismissed() {
		a.RawOpen = true
		a.requestEditorClose("raw")
	}
}
func (a *App) ExportDialog() {
	p, ok := a.current()
	if !ok {
		a.Notice = "先在左侧选择一个连接，再导出配置。"
		return
	}
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageQuestion, Message: "导出配置", Detail: "含凭证的文件是明文，请妥善保管。不含凭证的文件在重新导入时需要补填。", Buttons: []string{"取消", "不含凭证", "含凭证"}, DefaultButton: 1, CancelButton: 0})
	if err != nil || r.Button < 1 {
		return
	}
	include := r.Button == 2
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "导出配置", DefaultPath: "frpc.toml"})
	if err != nil {
		a.Notice = err.Error()
		return
	}
	if path == "" {
		return
	}
	cmd := profileCommand(p, app.ExportProfile{Path: path, IncludeSecrets: include})
	a.Do("导出配置", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, nil)
}
func (a *App) ExportLogs() {
	if err := a.logFilterError(); err != nil {
		a.Notice = err.Error()
		return
	}
	p, ok := a.current()
	if !ok {
		return
	}
	if len(a.filteredLogs()) == 0 {
		a.Notice = "没有可导出的日志"
		return
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "导出脱敏日志", DefaultPath: "frp-diagnostics.log", Message: "已自动隐藏已知凭证，请检查文件后再分享。"})
	if err != nil || path == "" {
		return
	}
	selection := &app.LogSelection{Lines: append([]logging.Line(nil), a.filteredLogs()...)}
	cmd := profileCommand(p, app.ExportDiagnostics{Path: path, Selection: selection})
	a.Do("导出脱敏日志", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, nil)
}

func (a *App) cores(c *u.Context) {
	u.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		u.Text(c, "frpc 版本").FontSize(20).Bold()
		u.Text(c, "每个连接固定使用一个版本；新下载的版本不会影响正在运行的连接。").TextColor(c.Theme().TextMuted).Wrap()
		u.Row(c).Gap(8).Children(func() {
			if len(a.CoreReleases) > 0 {
				u.Select(c, &a.CoreVersion, a.CoreReleases).Width(150).Label("版本")
			} else {
				u.TextInput(c, &a.CoreVersion).Width(150).Label("版本")
			}
			u.Button(c, "刷新列表").Disabled(a.CoreBusy).OnClick(a.FetchCoreReleases)
			u.PrimaryButton(c, "下载所选版本").Disabled(a.CoreBusy).OnClick(a.InstallCore)
			if a.CoreBusy {
				u.Button(c, "取消下载").OnClick(func() {
					if a.CoreCancel != nil {
						a.CoreCancel()
					}
				})
			}
			u.Button(c, "使用自定义文件").Disabled(a.Busy || a.CoreBusy).OnClick(a.CustomCoreDialog)
		})
		u.Scroll(c).Grow(1).Children(func() {
			if len(a.State.Cores) == 0 {
				u.Text(c, "正在准备内置版本").TextColor(c.Theme().TextMuted)
			}
			for _, co := range a.State.Cores {
				u.Row(c.Key(co.ID)).Padding(12, 0).Gap(12).Children(func() {
					u.Column(c).Grow(1).Gap(4).Children(func() {
						u.Text(c, "frpc "+co.Version).Bold()
						u.Text(c, a.coreDescription(co.ID)).FontSize(12).TextColor(c.Theme().TextMuted)
						u.Text(c, co.Path).Font("monospace").FontSize(12).SingleLine().TextColor(c.Theme().TextMuted)
					})
					if co.Certified {
						if a.State.Settings.DefaultCoreID == co.ID {
							u.Text(c, "新连接默认").FontSize(12).TextColor(c.Theme().TextMuted)
						} else {
							u.Button(c, "设为新连接默认").Disabled(a.Busy).OnClick(func() {
								id := co.ID
								cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: a.State.CoreRevision, Action: app.SetDefaultCore{ID: id}}
								a.Do("设置默认版本", func(ctx context.Context) error { _, err := a.Controller.ExecuteGlobal(ctx, cmd); return err }, nil)
							})
						}
					}
					if !co.Bundled {
						u.Button(c, "删除").Disabled(a.Busy).OnClick(func() {
							id := co.ID
							result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "删除这个 frpc 版本？", Detail: "只删除应用内的文件；被连接、回收区或快照引用的版本会保留。", Buttons: []string{"取消", "删除"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
							if err == nil && result.Button == 1 {
								cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: a.State.CoreRevision, Action: app.DeleteCore{ID: id}}
								a.Do("删除版本", func(ctx context.Context) error { _, err := a.Controller.ExecuteGlobal(ctx, cmd); return err }, nil)
							}
						})
					}
				})
				u.Divider(c)
			}
		})
		u.Text(c, "内置版本可离线使用；未认证的版本只能编辑原文。").FontSize(12).TextColor(c.Theme().TextMuted)
	})
}
func (a *App) CustomCoreDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "选择 frpc 文件", Message: "自定义文件会以当前用户权限运行，请只使用可信来源。"})
	if err != nil || len(paths) == 0 {
		return
	}
	path := paths[0]
	cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: a.State.CoreRevision, Action: app.CustomCore{Path: path}}
	a.CoreTask("检查自定义内核", func(ctx context.Context) error { _, err := a.Controller.ExecuteGlobal(ctx, cmd); return err }, nil)
}

func (a *App) OpenSettings() {
	if a.SettingsWindow != nil {
		a.SettingsWindow.Show()
		a.SettingsWindow.Focus()
		return
	}
	a.Settings = a.State.Settings
	a.Settings.OpenAtLogin = mygo.App.OpenAtLogin()
	ref := a.Settings.DownloadProxyRef
	var proxy string
	a.Do("读取设置", func(context.Context) error {
		var err error
		proxy, err = storage.Secret(a.Controller.Secrets, ref)
		return err
	}, func() {
		a.DownloadProxy, a.DownloadProxySaved = proxy, proxy
		a.SettingsWindow = mygo.NewWindow(mygo.WindowOptions{Title: "设置", Width: 640, Height: 640, MinWidth: 560, MinHeight: 520, Content: u.View(a.SettingsView)})
		a.SettingsWindow.OnShow(a.UpdateObservationScope)
		a.SettingsWindow.OnHide(a.UpdateObservationScope)
		a.SettingsWindow.OnClosed(func() {
			if a.settingsDirty() {
				a.SaveSettings()
			}
			a.SettingsWindow = nil
			a.UpdateObservationScope()
		})
		a.UpdateObservationScope()
	})
}
func (a *App) SettingsView(c *u.Context) {
	c.SetTheme(appTheme(a.Dark))
	u.Scroll(c).Fill().Padding(24).Children(func() {
		u.Column(c).Gap(16).Children(func() {
			u.Text(c, "设置").FontSize(20).Bold()
			u.Form(c, func() {
				u.Field(c, "外观", func() {
					choice := 0
					switch a.Settings.Appearance {
					case "light":
						choice = 1
					case "dark":
						choice = 2
					}
					if u.Segmented(c, &choice, "跟随系统", "浅色", "深色").Changed() {
						a.Settings.Appearance = []string{"system", "light", "dark"}[choice]
					}
				})
				u.Field(c, "登录", func() { u.Checkbox(c, &a.Settings.OpenAtLogin, "登录时在后台启动") })
				u.Field(c, "退出", func() { u.Checkbox(c, &a.Settings.ConfirmQuit, "有连接运行时先确认") })
				u.Field(c, "下载代理", func() { u.TextInput(c, &a.DownloadProxy).Password() }).Description("留空则使用系统代理，仅用于下载 frpc")
			})
			if a.settingsDirty() {
				u.PrimaryButton(c, "保存设置").Disabled(a.Busy).OnClick(a.SaveSettings)
			} else {
				u.Text(c, "设置已保存").FontSize(12).TextColor(c.Theme().TextMuted)
			}
			u.Divider(c)
			u.Text(c, "应用更新").FontSize(16).Bold()
			u.Text(c, "从 GitHub 检查并安装新版本。")
			u.Button(c, "检查更新").OnClick(a.CheckUpdates)
			u.Divider(c)
			u.Text(c, "数据与备份").FontSize(16).Bold()
			u.Row(c).Gap(8).Children(func() {
				u.Button(c, "加密备份").OnClick(func() {
					a.BackupRestore = false
					a.BackupPassword, a.BackupPasswordAgain = "", ""
					a.BackupOpen = true
					a.Window.Show()
					a.Window.Focus()
				})
				u.Button(c, "恢复备份").OnClick(a.RestoreDialog)
			})
			if len(a.State.Trash) > 0 {
				u.Text(c, "最近删除的连接会保留 7 天。").FontSize(12).TextColor(c.Theme().TextMuted)
			}
			for _, deleted := range a.State.Trash {
				u.Row(c).Gap(8).Children(func() {
					u.Text(c, deleted.Profile.Name).Grow(1)
					u.Button(c, "恢复").Disabled(a.Busy).OnClick(func() {
						cmd := profileCommand(deleted.Profile, app.RestoreProfile{})
						a.Do("恢复连接", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, nil)
					})
				})
			}
			if a.Notice != "" {
				u.Text(c, a.Notice).TextColor(c.Theme().TextMuted).Wrap()
			}
		})
	})
}

func (a *App) settingsDirty() bool {
	saved := a.State.Settings
	return a.Settings.Appearance != saved.Appearance ||
		a.Settings.OpenAtLogin != saved.OpenAtLogin ||
		a.Settings.ConfirmQuit != saved.ConfirmQuit ||
		a.DownloadProxy != a.DownloadProxySaved
}
func (a *App) ToggleAppearance() {
	settings := a.State.Settings
	settings.Appearance = "dark"
	if a.Dark {
		settings.Appearance = "light"
	}
	cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: settings.Revision, Action: app.SaveAppSettings{Settings: settings}}
	var result app.CommandResult
	a.Do("切换主题", func(ctx context.Context) error {
		var err error
		result, err = a.Controller.ExecuteGlobal(ctx, cmd)
		return err
	}, func() {
		a.Settings.Appearance = settings.Appearance
		a.Settings.Revision = result.State.Settings.Revision
		a.applyAppearance(settings.Appearance)
	})
}

func (a *App) applyAppearance(appearance string) {
	switch appearance {
	case "dark":
		a.Dark = true
		mygo.Theme.SetSource(mygo.ThemeDark)
	case "light":
		a.Dark = false
		mygo.Theme.SetSource(mygo.ThemeLight)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
	if a.SettingsWindow != nil {
		a.SettingsWindow.Invalidate()
	}
}

func (a *App) SaveSettings() {
	settings := a.Settings
	proxy := a.DownloadProxy
	cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: settings.Revision, Action: app.SaveAppSettings{Settings: settings, Proxy: &proxy, ApplySystemLogin: true}}
	var result app.CommandResult
	a.Do("保存设置", func(ctx context.Context) error {
		var err error
		result, err = a.Controller.ExecuteGlobal(ctx, cmd)
		return err
	}, func() {
		a.Settings.Revision = result.State.Settings.Revision
		a.Settings.DownloadProxyRef = result.State.Settings.DownloadProxyRef
		a.DownloadProxySaved = a.DownloadProxy
		a.applyAppearance(settings.Appearance)
		a.Notice = "设置已保存"
	})
}
func (a *App) RestoreDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.SettingsWindow, Title: "选择加密备份", Filters: []mygo.FileFilter{{Name: "frp-desktop 备份", Extensions: []string{"frpbackup"}}}})
	if err != nil || len(paths) == 0 {
		return
	}
	path := paths[0]
	var data []byte
	a.Do("读取备份", func(context.Context) error { var e error; data, e = readBounded(path, 20<<20); return e }, func() {
		a.BackupData = data
		a.BackupRestore = true
		a.BackupPassword, a.BackupPasswordAgain = "", ""
		a.BackupOpen = true
		a.Window.Show()
		a.Window.Focus()
	})
}
func (a *App) backupModal(c *u.Context) {
	u.Modal(c, &a.BackupOpen, func() {
		u.Column(c).Width(420).Gap(16).Children(func() {
			label := "创建加密备份"
			if a.BackupRestore {
				label = "恢复加密备份"
			}
			u.Text(c, label).FontSize(18).Bold()
			if a.BackupRestore {
				u.Text(c, "恢复会新增连接，不覆盖现有配置，也不会自动连接。").Wrap()
			} else {
				u.Text(c, "备份包含连接和凭证，需要这个口令才能恢复。").Wrap()
			}
			u.TextInput(c, &a.BackupPassword).Password().Placeholder("备份口令，至少 10 个字符")
			if !a.BackupRestore {
				u.TextInput(c, &a.BackupPasswordAgain).Password().Placeholder("再输入一次")
			}
			a.formError(c)
			u.Row(c).Justify(u.End).Gap(8).Children(func() {
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.BackupOpen = false; a.BackupData = nil; a.BackupPassword, a.BackupPasswordAgain = "", "" })
				u.PrimaryButton(c, label).Disabled(a.Busy).OnClick(func() {
					password := a.BackupPassword
					a.FormError = ""
					if len([]rune(password)) < 10 {
						a.FormError = "备份口令至少需要 10 个字符"
						return
					}
					if a.BackupRestore {
						a.PreviewRestore()
					} else {
						if password != a.BackupPasswordAgain {
							a.FormError = "两次输入的口令不一致"
							return
						}
						path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "保存加密备份", DefaultPath: "frp-desktop.frpbackup"})
						if err != nil || path == "" {
							return
						}
						cmd := app.GlobalCommand{OperationID: domain.NewID(), ExpectedRevision: a.State.Revision, Action: app.ExportBackup{Path: path, Password: password}}
						a.Do("创建加密备份", func(ctx context.Context) error { _, err := a.Controller.ExecuteGlobal(ctx, cmd); return err }, func() { a.BackupOpen = false; a.BackupPassword, a.BackupPasswordAgain = "", "" })
					}
				})
			})
		})
	})
}

func SortedProfiles(ps []domain.Profile) []domain.Profile {
	out := append([]domain.Profile(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}
