package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
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
	"frp-desktop/internal/update"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

type App struct {
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
	Updates          *update.Manager
	UpdateOpen       bool
	UpdateState      update.Snapshot
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
	Logs             []logging.Line
	ProfileOpen      bool

	FormError  string
	TunnelOpen bool

	RawOpen bool

	BackupOpen     bool
	BackupRestore  bool
	BackupPassword string
	SettingsOpen   bool
	Settings       domain.Settings
	DownloadProxy  string
	CoreVersion    string
	BackupData     []byte
	SettingsWindow *mygo.Window
}

func New(c *app.Controller) *App {
	a := &App{Controller: c, SidebarWidth: sidebarDefault, LogHeight: logDefaultHeight, ShowInspector: true, SelectedRow: -1, LogLevel: "全部", CoreVersion: domain.CertifiedCore}
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
	c.OnShortcut(u.Cmd|u.Alt, u.KeyL, func() { a.ShowLogs = !a.ShowLogs; a.Refresh() })
	c.OnShortcut(u.Cmd|u.Alt, u.KeyI, func() { width, _ := c.Size(); a.ToggleInspector(width) })
	u.Column(c).Fill().Children(func() {
		a.SidebarWidth = clampPane(a.SidebarWidth, sidebarMinimum, sidebarMaximum)
		u.Split(c, &a.SidebarWidth, func() { a.sidebar(c) }, func() { a.workspace(c) }).Grow(1).OnChange(func() { a.SidebarWidth = clampPane(a.SidebarWidth, sidebarMinimum, sidebarMaximum) })
		a.draftMenu(c)
		if a.UpdateState.Offer && !a.UpdateOpen {
			u.Button(c, "有可用的应用更新…").Disabled(a.HasModal() || a.Busy).OnClick(func() { a.UpdateOpen = true })
		}
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
	u.Column(c).Fill().Background(c.Theme().Surface).Padding(12, 8).Gap(4).Label("连接侧栏").Children(func() {
		u.Text(c, "连接").FontSize(11).TextColor(c.Theme().TextMuted).Padding(4, 8)
		u.Button(c, "全部连接").OnClick(func() { a.SelectScope("all") })
		u.Button(c, "需要关注").OnClick(func() { a.SelectScope("attention") })
		u.Divider(c)
		u.Scroll(c).Grow(1).Children(func() {
			group := "\x00"
			for _, p := range a.State.Profiles {
				if p.Group != group {
					group = p.Group
					label := group
					if label == "" {
						label = "我的连接"
					}
					u.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).Padding(12, 8, 4, 8)
				}
				label := p.Name
				rt := a.Runtime[p.ID]
				if rt.Running() {
					label = "●  " + label
				} else {
					label = "○  " + label
				}
				button := u.Button(c.Key(p.ID), label).FillWidth()
				if a.Selected == p.ID {
					button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
				}
				button.OnClick(func() { a.SelectScope(p.ID); a.Refresh() })
			}
		})
		u.Divider(c)
		u.Button(c, "内核").OnClick(func() { a.SelectScope("cores") })
		u.Button(c, "添加连接…").Disabled(a.Busy).OnClick(func() { a.EditProfile("") })
	})
}
func (a *App) workspace(c *u.Context) {
	u.Column(c).Fill().Children(func() {
		if a.Selected == "cores" {
			a.cores(c)
			return
		}
		p, has := a.current()
		title := "全部连接"
		if has {
			title = p.Name
		} else if a.Selected == "attention" {
			title = "需要关注"
		}
		u.Row(c).Padding(16).Gap(8).AlignItems(u.Center).Children(func() {
			u.Text(c, title).FontSize(20).Bold().Grow(1)
			if u.SearchField(c, &a.Search).Placeholder("搜索隧道").Width(210).Bind(&a.SearchHandle).Changed() {
				a.Selection.Clear()
				a.SelectedRow = -1
			}
			u.Button(c, "检查器").OnClick(func() { width, _ := c.Size(); a.ToggleInspector(width) })
			u.Button(c, "日志").Disabled(!has).OnClick(func() { a.ShowLogs = !a.ShowLogs; a.Refresh() })
		})
		if len(a.State.Profiles) == 0 {
			u.Column(c).Grow(1).Center().Gap(12).Children(func() {
				u.Text(c, "把本地服务连接到外部网络").FontSize(20).Bold()
				u.Text(c, "填写已有 frps 的地址和凭证，然后添加隧道。").TextColor(c.Theme().TextMuted)
				u.Row(c).Gap(8).Children(func() {
					u.PrimaryButton(c, "添加第一个连接…").Disabled(a.Busy).OnClick(func() { a.EditProfile("") })
					u.Button(c, "导入 frpc.toml…").Disabled(a.Busy).OnClick(a.ImportDialog)
				})
			})
			return
		}
		if has {
			rt := a.Runtime[p.ID]
			u.Row(c).Padding(0, 16, 12, 16).Gap(8).AlignItems(u.Center).Children(func() {
				u.Text(c, domain.Endpoint(p.ServerAddr, p.ServerPort)).Font("monospace").TextColor(c.Theme().TextMuted).Grow(1)
				u.Text(c, ProcessLabel(rt)).FontSize(12)
				if rt.Running() {
					u.Button(c, "停止").Disabled(a.Busy).OnClick(func() {
						a.RunProfile("正在停止连接", p, app.StopProfile{})
					})
				} else {
					u.PrimaryButton(c, "连接").Disabled(a.Busy).OnClick(func() {
						a.RunProfile("正在启动连接", p, app.StartProfile{})
					})
				}
				u.Button(c, "连接设置…").Disabled(a.Busy).OnClick(func() { a.EditProfile(p.ID) })
			})
			if summary := RegistrationSummary(p, rt, time.Now()); summary != "" {
				u.Text(c, summary).FontSize(12).TextColor(c.Theme().TextMuted).Wrap().Padding(0, 16, 8, 16)
			}
			if rt.SessionError != "" {
				u.Text(c, rt.SessionError).TextColor(c.Theme().Danger).Padding(0, 16, 8, 16).MaxHeight(70)
			}
			if rt.Error != "" {
				u.Text(c, rt.Error).TextColor(c.Theme().Danger).Padding(0, 16, 8, 16)
			}
			if rt.Running() && rt.AppliedRevision != p.Revision {
				u.Row(c).Padding(0, 16, 8, 16).Gap(8).Children(func() {
					u.Text(c, "已保存，有更改尚未应用").Grow(1)
					u.Button(c, "应用更改").Disabled(a.Busy).OnClick(func() { a.ApplyWithConfirmation(p) })
				})
			}
		}
		u.Toolbar(c, func() {
			u.Button(c, "隧道").OnClick(func() { a.VisitorTab = false; a.SelectedRow = -1; a.Selection.Clear() })
			u.Button(c, "访问者").OnClick(func() { a.VisitorTab = true; a.SelectedRow = -1; a.Selection.Clear() })
			u.Spacer(c)
			u.Button(c, "添加…").Disabled(!has || p.Mode == "raw" || a.Busy).OnClick(func() { a.EditTunnel(-1, a.VisitorTab) })
			u.Button(c, "配置预览…").Disabled(!has || a.Busy).OnClick(a.OpenRaw)
			u.Button(c, "启用所选").Disabled(a.Busy || a.Selection.Len() == 0).OnClick(func() { a.BatchToggle(true) })
			u.Button(c, "停用所选").Disabled(a.Busy || a.Selection.Len() == 0).OnClick(func() { a.BatchToggle(false) })
			u.MenuButton(c, "更多", a.entryMenu)
		}).Padding(0, 16, 8, 16).Gap(8)
		a.contentPanes(c, a.ShowLogs && has, func() {
			if has && p.Mode == "raw" {
				u.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
					u.Column(c).Padding(24).Gap(12).Children(func() {
						u.Text(c, "此连接使用原文配置").FontSize(18).Bold()
						u.Text(c, "保留未覆盖的字段与注释。单隧道开关和热重载不可用，修改后需重新连接。").Wrap()
						u.Button(c, "编辑原文…").OnClick(a.OpenRaw)
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
				status := "已停止"
				if rt.Running() {
					status = "状态未确认"
				}
				if !v.Enabled {
					status = "停用"
				}
				if rt.Running() && rt.AppliedRevision != p.Revision {
					status = "待应用"
				}
				r := row{Profile: p, Visitor: v, IsVisitor: true, Status: status}
				if _, pending := a.pendingToggles[p.ID]; pending {
					r.Status = "待应用"
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
	cols := []u.TableColumn{{Title: "名称", Width: 160, Sortable: true}, {Title: "协议", Width: 65, Sortable: true}, {Title: "本地端点", Width: 160}, {Title: "远程端点", Width: 180}, {Title: "状态", Width: 110, Sortable: true}}
	table := u.Table(c.Key("tunnels"), &a.Table, cols, len(rows), func(i, j int) {
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
			case 4:
				text = r.Status
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
			case 4:
				text = r.Status
			}
		}
		el := u.Text(c, text).SingleLine()
		if j == 2 || j == 3 {
			el.Font("monospace")
		}
		if r.Err != "" && j == 4 {
			el.TextColor(c.Theme().Danger)
		}
	}).Grow(1).FillHeight().MinHeight(0).Label("隧道列表")
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
	if len(rows) == 0 {
		u.Text(c, "暂无匹配的隧道").TextColor(c.Theme().TextMuted).Padding(24)
	}
}
func (a *App) inspector(c *u.Context) {
	u.Column(c).Width(inspectorWidth).Padding(16).Gap(12).Label("隧道检查器").Children(func() {
		rows := a.rows()
		a.sortRows(rows)
		if a.SelectedRow < 0 || a.SelectedRow >= len(rows) {
			u.Text(c, "选择隧道查看详情").TextColor(c.Theme().TextMuted)
			return
		}
		r := rows[a.SelectedRow]
		name := r.Tunnel.Name
		if r.IsVisitor {
			name = r.Visitor.Name
		}
		u.Text(c, name).FontSize(18).Bold()
		u.Text(c, "连接："+r.Profile.Name)
		u.Text(c, "状态："+r.Status)
		if r.Err != "" {
			u.Text(c, r.Err).TextColor(c.Theme().Danger).Wrap()
		}
		u.Text(c, "已注册表示 frps 接受代理，不代表外网业务已经可达。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
		u.Button(c, "编辑…").Disabled(a.Busy).OnClick(func() {
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
			u.Button(c, "复制远程地址").OnClick(func() { mygo.Clipboard.WriteText(config.Remote(r.Tunnel, r.Profile)) })
			u.Button(c, "检测本地端口").Disabled(r.Tunnel.Type == "udp" || r.Tunnel.Type == "sudp" || a.Busy).OnClick(func() { addr := domain.Endpoint(r.Tunnel.LocalIP, r.Tunnel.LocalPort); a.TestEndpoint(addr) })
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
	u.Column(c).Grow(1).FillWidth().MinHeight(0).Padding(8, 16).Gap(8).Label("日志面板").Children(func() {
		u.Row(c).Gap(8).Children(func() {
			u.Text(c, "日志").Bold()
			u.Select(c, &a.LogLevel, []string{"全部", "error", "warn", "info", "debug"}).Width(90)
			u.SearchField(c, &a.LogSearch).Placeholder("过滤日志").Grow(1).OnChange(a.queueLogSearch)
			u.Checkbox(c, &a.LogPaused, "暂停跟随")
			u.Button(c, "复制").OnClick(a.CopyLogs)
			u.Button(c, "导出…").OnClick(a.ExportLogs)
		})
		lines := a.filteredLogs()
		a.LogList.Key = func(i int) any { return lines[i].Seq }
		a.LogList.FollowEnd = !a.LogPaused
		u.List(c.Key("logs"), &a.LogList, len(lines), func(i int) {
			l := lines[i]
			u.Text(c, l.Time.Format("15:04:05")+"  "+l.Text).Font("monospace").FontSize(11).SingleLine().Selectable()
		}).Grow(1).Label("连接日志")
	})
}
func ProcessLabel(r domain.Runtime) string {
	switch r.Process {
	case "starting":
		return "启动中"
	case "stopping":
		return "停止中"
	case "failed":
		return "异常退出"
	case "running":
		if r.Session == "authentication_failed" {
			return "认证失败"
		}
		if r.Session == "reconnecting" {
			return "重连中"
		}
		if r.Session == "logged_in" && time.Since(r.SessionObservedAt) <= 10*time.Second {
			return "已登录（日志确认）"
		}
		if r.Session == "connected" {
			return "运行中 · 已确认代理注册"
		}
		return "运行中 · 连接状态未确认"
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
	a.ProfileAdvanced = false
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
func (a *App) ApplyWithConfirmation(p domain.Profile) {
	r := a.Runtime[p.ID]
	if !r.Running() {
		return
	}
	result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "应用此连接的更改？", Detail: fmt.Sprintf("连接参数变化可能需要重启 frpc，影响此连接下的 %d 条隧道。", len(p.Tunnels)), Buttons: []string{"取消", "应用更改"}, DefaultButton: 0, CancelButton: 0})
	if err != nil || result.Button != 1 {
		return
	}
	a.RunProfile("正在应用配置", p, app.ApplyProfile{})
}

func (a *App) profileModal(c *u.Context) {
	m := u.Modal(c, &a.ProfileOpen, func() {
		u.Column(c).Width(560).MaxHeight(590).Gap(12).Children(func() {
			u.Text(c, "连接设置").FontSize(20).Bold()
			if a.ProfileDraft.CoreVersion != "" && a.ProfileCore == "" {
				u.Text(c, "此连接需要安装或重新选择 frpc "+a.ProfileDraft.CoreVersion).TextColor(c.Theme().Danger)
			}
			u.Scroll(c).Grow(1).Children(func() {
				u.Form(c, func() {
					field(c, "名称", &a.ProfileDraft.Name)
					field(c, "分组", &a.ProfileDraft.Group)
					if a.ProfileDraft.Mode == "managed" {
						a.profileField(c, "frps 地址", &a.ProfileDraft.ServerAddr, "serverAddr")
						a.profileField(c, "服务端端口", &a.ProfilePort, "serverPort")
						field(c, "用户前缀", &a.ProfileDraft.User)
						u.Field(c, "Token", func() { u.TextInput(c, &a.ProfileToken).Password() })
					}
					u.Field(c, "内核", func() {
						ids := []string{}
						for _, co := range a.State.Cores {
							ids = append(ids, a.coreChoice(co.ID))
						}
						u.Select(c, &a.ProfileCore, ids)
					}).Description(a.coreDescription(a.coreChoiceID(a.ProfileCore)))
					if a.ProfileDraft.Mode == "managed" {
						u.Collapsible(c, "高级连接设置", &a.ProfileAdvanced, func() {
							u.Field(c, "传输协议", func() { u.Select(c, &a.ProfileDraft.Transport, []string{"tcp", "kcp", "quic", "websocket", "wss"}) })
							u.Field(c, "TLS", func() { u.Checkbox(c, &a.ProfileDraft.TLS, "加密连接") })
							u.Field(c, "证书校验", func() { u.Checkbox(c, &a.ProfileDraft.VerifyTLS, "使用系统 CA 验证服务端") })
							field(c, "证书名称", &a.ProfileDraft.TLSServerName)
							u.Field(c, "自定义 CA", func() {
								u.Row(c).Gap(8).Children(func() {
									u.Button(c, "导入 PEM…").Disabled(a.Busy).OnClick(a.ImportCA)
									u.Button(c, "清除").Disabled(a.ProfileDraft.TrustedCA == "").OnClick(func() { a.ProfileDraft.TrustedCA = "" })
								})
							}).Description(filepath.Base(a.ProfileDraft.TrustedCA))
							u.Field(c, "连接代理", func() { u.TextInput(c, &a.ProfileProxy).Password() }).Description("仅用于 frpc 到 frps 的连接；支持 HTTP/SOCKS5")
							u.Field(c, "Metadata JSON", func() { u.TextArea(c, &a.ProfileMetadata).Height(75).Font("monospace") })
						})
					}
					u.Field(c, "自动连接", func() { u.Checkbox(c, &a.ProfileDraft.AutoConnect, "启动应用后连接此配置") })
				})
			})
			if !a.ProfileDraft.TLS || !a.ProfileDraft.VerifyTLS {
				u.Text(c, "当前关闭了加密或服务端证书校验，请确认网络可信。").TextColor(c.Theme().Danger).Wrap()
			}
			a.formError(c)
			u.Row(c).Gap(8).Children(func() {
				if a.ProfileDraft.Revision > 0 {
					u.Button(c, "检查残留进程…").Disabled(a.Busy).OnClick(func() { a.InspectResidual(a.ProfileDraft.ID) })
					u.Button(c, "删除连接…").Disabled(a.Busy).OnClick(a.DeleteCurrent)
					u.Button(c, "复制").Disabled(a.Busy).OnClick(func() {
						cmd := profileCommand(a.ProfileDraft, app.DuplicateProfile{})
						a.Do("复制连接", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.requestEditorClose("profile") })
					})
				}
				u.Spacer(c)
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.requestEditorClose("profile") })
				u.PrimaryButton(c, "保存").Disabled(a.Busy).OnClick(func() { a.SaveProfile(false) })
				connectLabel := "保存并连接"
				if a.Runtime[a.ProfileDraft.ID].Running() {
					connectLabel = "保存并应用"
				}
				u.Button(c, connectLabel).Disabled(a.Busy).OnClick(func() { a.SaveProfile(true) })
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
		a.FormError = "端口必须是整数"
		a.FormField = "serverPort"
		return
	}
	p.ServerPort = port
	p.CoreID = a.coreChoiceID(a.ProfileCore)
	p.Metadata = map[string]string{}
	if strings.TrimSpace(a.ProfileMetadata) != "" && a.ProfileMetadata != "null" {
		if err = json.Unmarshal([]byte(a.ProfileMetadata), &p.Metadata); err != nil {
			a.FormError = "Metadata 必须为字符串键值 JSON 对象"
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
		return "请选择已安装内核"
	}
	label := "未认证，仅原文模式"
	if co.Certified {
		label = "已认证"
	}
	return "frpc " + co.Version + " · " + co.Arch + " · " + label
}

func (a *App) DeleteCurrent() {
	p := a.ProfileDraft
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "删除连接「" + p.Name + "」？", Detail: "将停止该连接的所有隧道，配置保留在回收区 7 天。", Buttons: []string{"取消", "删除连接"}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}})
	if err != nil || r.Button != 1 {
		return
	}
	cmd := profileCommand(p, app.DeleteProfile{})
	a.Do("停止并删除连接", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.finishEditor("profile", false); a.Selected = "all" })
}
func (a *App) TestEndpoint(addr string) {
	a.Do("检测本地 TCP 端口", func(ctx context.Context) error { return testEndpoint(ctx, addr) }, func() { a.Notice = "本地 TCP 端口可连接；未验证外网访问或应用协议。" })
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
			title := "配置预览（凭证已省略）"
			if a.RawProfile.Mode == "raw" {
				title = "编辑原文（包含凭证）"
			}
			u.Text(c, title).FontSize(18).Bold()
			u.TextArea(c, &a.RawText).Font("monospace").Grow(1).ReadOnly(a.RawProfile.Mode != "raw")
			a.formError(c)
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				u.Button(c, "关闭").OnClick(func() { a.requestEditorClose("raw") })
				if a.RawProfile.Mode == "raw" {
					u.PrimaryButton(c, "校验并保存").Disabled(a.Busy).OnClick(func() {
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
					u.Button(c, "转换为原文模式…").Disabled(a.Busy).OnClick(a.ConvertToRaw)
				}
				u.Button(c, "导出 TOML…").OnClick(a.ExportDialog)
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
		return
	}
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageQuestion, Message: "导出是否包含凭证？", Detail: "包含凭证的 TOML 是明文文件，请妥善保管。默认导出需重新补充凭证。", Buttons: []string{"取消", "省略凭证", "包含凭证"}, DefaultButton: 1, CancelButton: 0})
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
	p, ok := a.current()
	if !ok {
		return
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "导出脱敏日志", DefaultPath: "frp-diagnostics.log", Message: "已自动隐藏已知凭证，请检查文件后再分享。"})
	if err != nil || path == "" {
		return
	}
	cmd := profileCommand(p, app.ExportDiagnostics{Path: path})
	a.Do("导出脱敏日志", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, nil)
}

func (a *App) cores(c *u.Context) {
	u.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		u.Text(c, "frpc 内核").FontSize(20).Bold()
		u.Text(c, "各连接固定使用所选版本。下载新内核不会自动切换运行中的连接。").TextColor(c.Theme().TextMuted).Wrap()
		u.Row(c).Gap(8).Children(func() {
			if len(a.CoreReleases) > 0 {
				u.Select(c, &a.CoreVersion, a.CoreReleases).Width(150).Label("frpc 版本")
			} else {
				u.TextInput(c, &a.CoreVersion).Width(150).Label("frpc 版本")
			}
			u.Button(c, "刷新版本").Disabled(a.CoreBusy).OnClick(a.FetchCoreReleases)
			u.PrimaryButton(c, "下载官方版本").Disabled(a.CoreBusy).OnClick(a.InstallCore)
			if a.CoreBusy {
				u.Button(c, "取消下载").OnClick(func() {
					if a.CoreCancel != nil {
						a.CoreCancel()
					}
				})
			}
			u.Button(c, "选择自定义 frpc…").Disabled(a.Busy).OnClick(a.CustomCoreDialog)
		})
		u.Scroll(c).Grow(1).Children(func() {
			for _, co := range a.State.Cores {
				u.Row(c.Key(co.ID)).Padding(12, 0).Gap(12).Children(func() {
					u.Column(c).Grow(1).Gap(4).Children(func() {
						u.Text(c, "frpc "+co.Version+" · "+co.Arch).Bold()
						u.Text(c, a.coreDescription(co.ID)).FontSize(12).TextColor(c.Theme().TextMuted)
						u.Text(c, co.Path).Font("monospace").FontSize(11).SingleLine()
					})
					if co.Certified {
						u.Button(c, "设为新连接默认").Disabled(a.Busy || a.State.Settings.DefaultCoreID == co.ID).OnClick(func() {
							id := co.ID
							a.Do("设置默认内核", func(context.Context) error { return a.Controller.SetDefaultCore(id) }, nil)
						})
					}
					if !co.Bundled {
						u.Button(c, "删除内核…").Disabled(a.Busy).OnClick(func() {
							id := co.ID
							result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "删除此内核版本？", Detail: "仅删除应用内核库中的文件。连接、回收区和恢复快照引用的版本会保留。", Buttons: []string{"取消", "删除内核"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
							if err == nil && result.Button == 1 {
								a.Do("删除未引用内核", func(context.Context) error { return a.Controller.DeleteCore(id) }, nil)
							}
						})
					}
				})
				u.Divider(c)
			}
		})
		u.Text(c, "未认证版本只支持原文模式。保底内核可离线使用。").FontSize(12).TextColor(c.Theme().TextMuted)
	})
}
func (a *App) CustomCoreDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "选择自定义 frpc", Message: "用户提供的可执行文件会以当前用户权限运行。仅选择可信来源。"})
	if err != nil || len(paths) == 0 {
		return
	}
	path := paths[0]
	a.Do("检查自定义内核", func(ctx context.Context) error { return a.Controller.CustomCore(ctx, path) }, nil)
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
		a.DownloadProxy = proxy
		a.SettingsWindow = mygo.NewWindow(mygo.WindowOptions{Title: "设置", Width: 640, Height: 640, MinWidth: 560, MinHeight: 520, Content: u.View(a.SettingsView)})
		a.SettingsWindow.OnShow(a.UpdateObservationScope)
		a.SettingsWindow.OnHide(a.UpdateObservationScope)
		a.SettingsWindow.OnClosed(func() { a.SettingsWindow = nil; a.UpdateObservationScope() })
		a.UpdateObservationScope()
	})
}
func (a *App) SettingsView(c *u.Context) {
	u.Scroll(c).Fill().Padding(24).Children(func() {
		u.Column(c).Gap(16).Children(func() {
			u.Text(c, "设置").FontSize(20).Bold()
			u.Form(c, func() {
				u.Field(c, "外观", func() { u.Select(c, &a.Settings.Appearance, []string{"system", "light", "dark"}) })
				u.Field(c, "登录项", func() { u.Checkbox(c, &a.Settings.OpenAtLogin, "登录时启动，保持后台运行") })
				u.Field(c, "退出确认", func() { u.Checkbox(c, &a.Settings.ConfirmQuit, "存在运行连接时确认退出") })
				u.Field(c, "下载代理", func() { u.TextInput(c, &a.DownloadProxy).Password() }).Description("留空使用系统代理；只影响内核下载")
			})
			u.Button(c, "保存设置").Disabled(a.Busy).OnClick(a.SaveSettings)
			u.Divider(c)
			u.Text(c, "应用更新").FontSize(15).Bold()
			u.Text(c, "应用更新与 frpc 内核版本分开管理。")
			u.Button(c, "检查更新…").OnClick(a.CheckUpdates)
			auto := a.UpdateState.Preferences.AutomaticChecks
			u.Checkbox(c, &auto, "自动检查应用更新").OnClick(func() { a.saveUpdateChecks(auto) })
			downloads := a.UpdateState.Preferences.AutomaticDownloads
			u.Checkbox(c, &downloads, "自动下载并安装，下次启动生效").OnClick(func() { a.saveUpdateDownloads(downloads) })
			u.Divider(c)
			u.Text(c, "数据与备份").FontSize(15).Bold()
			u.Row(c).Gap(8).Children(func() {
				u.Button(c, "加密备份…").OnClick(func() {
					a.BackupRestore = false
					a.BackupPassword = ""
					a.BackupOpen = true
					a.Window.Show()
					a.Window.Focus()
				})
				u.Button(c, "恢复备份…").OnClick(a.RestoreDialog)
			})
			for _, deleted := range a.State.Trash {
				u.Row(c).Gap(8).Children(func() {
					u.Text(c, deleted.Profile.Name+" · 已删除").Grow(1)
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
func (a *App) SaveSettings() {
	settings := a.Settings
	proxy := a.DownloadProxy
	a.Do("保存设置", func(context.Context) error {
		if err := mygo.App.SetOpenAtLogin(settings.OpenAtLogin); err != nil {
			return err
		}
		return a.Controller.SaveSettings(settings, &proxy)
	}, func() {
		switch settings.Appearance {
		case "dark":
			mygo.Theme.SetSource(mygo.ThemeDark)
		case "light":
			mygo.Theme.SetSource(mygo.ThemeLight)
		default:
			mygo.Theme.SetSource(mygo.ThemeSystem)
		}
		a.Notice = "设置已保存"
		if a.SettingsWindow != nil {
			a.SettingsWindow.Invalidate()
		}
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
		a.BackupPassword = ""
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
			u.Text(c, "备份包含凭证。恢复会新增连接，不覆盖现有配置，也不会自动连接。").Wrap()
			u.TextInput(c, &a.BackupPassword).Password().Placeholder("至少 10 个字符的备份口令")
			a.formError(c)
			u.Row(c).Justify(u.End).Gap(8).Children(func() {
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.BackupOpen = false; a.BackupData = nil; a.BackupPassword = "" })
				u.PrimaryButton(c, label).Disabled(a.Busy).OnClick(func() {
					password := a.BackupPassword
					if a.BackupRestore {
						a.PreviewRestore()
					} else {
						path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "保存加密备份", DefaultPath: "frp-desktop.frpbackup"})
						if err != nil || path == "" {
							return
						}
						a.Do("加密备份", func(context.Context) error {
							blob, err := a.Controller.Backup(password)
							if err != nil {
								return err
							}
							return storage.AtomicWrite(path, blob, 0600)
						}, func() { a.BackupOpen = false; a.BackupPassword = "" })
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
