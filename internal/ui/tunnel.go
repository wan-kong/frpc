package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

func (a *App) EditTunnel(index int, visitor bool) {
	p, ok := a.current()
	if !ok || p.Mode == "raw" || a.Busy || a.HasModal() {
		return
	}
	key := fmt.Sprintf("%s/%t/new", p.ID, visitor)
	if index >= 0 {
		if visitor && index < len(p.Visitors) {
			key = p.ID + "/" + p.Visitors[index].ID
		} else if !visitor && index < len(p.Tunnels) {
			key = p.ID + "/" + p.Tunnels[index].ID
		}
	}
	if draft, ok := a.tunnelDrafts[key]; ok {
		a.tunnelEditor = draft
		delete(a.tunnelDrafts, key)
		a.TunnelOpen = true
		return
	}
	a.tunnelKey = key
	a.batchSavedIDs = nil
	a.TunnelProfile = copyProfile(p)
	a.EditingIndex = index
	a.EditingVisitor = visitor
	a.Batch = false
	a.BatchPreview = ""
	a.FormError = ""
	a.TunnelSecret = ""
	a.HTTPPassword = ""
	a.TunnelDraft = domain.NewTunnel()
	a.VisitorDraft = domain.NewVisitor()
	if index >= 0 {
		if visitor {
			if index >= len(p.Visitors) {
				return
			}
			a.VisitorDraft = p.Visitors[index]
		} else {
			if index >= len(p.Tunnels) {
				return
			}
			a.TunnelDraft = p.Tunnels[index]
		}
	}
	t, v := a.TunnelDraft, a.VisitorDraft
	a.LocalPort = strconv.Itoa(t.LocalPort)
	a.RemotePort = strconv.Itoa(t.RemotePort)
	a.Domains = strings.Join(t.Domains, ", ")
	a.Locations = strings.Join(t.Locations, ", ")
	if visitor {
		a.LocalPort = strconv.Itoa(v.BindPort)
	}
	if index < 0 {
		a.tunnelBaseline = a.tunnelStamp()
		a.TunnelOpen = true
		return
	}
	var secret, pass string
	a.Do("读取隧道凭证", func(context.Context) error {
		ref := t.SecretRef
		if visitor {
			ref = v.SecretRef
		}
		var err error
		secret, err = storage.Secret(a.Controller.Secrets, ref)
		if err != nil {
			return err
		}
		pass, err = storage.Secret(a.Controller.Secrets, t.HTTPPasswordRef)
		return err
	}, func() {
		a.TunnelSecret, a.HTTPPassword = secret, pass
		a.tunnelBaseline = a.tunnelStamp()
		a.TunnelOpen = true
	})
}
func (a *App) tunnelModal(c *u.Context) {
	modal := u.Modal(c, &a.TunnelOpen, func() {
		u.Column(c).Width(540).MaxHeight(590).Gap(12).Children(func() {
			title := "编辑隧道"
			if a.EditingVisitor {
				title = "编辑访问者"
			}
			u.Text(c, title).FontSize(20).Bold()
			u.Scroll(c).Grow(1).Children(func() {
				u.Form(c, func() {
					if a.EditingVisitor {
						v := &a.VisitorDraft
						field(c, "名称", &v.Name)
						u.Field(c, "协议", func() { u.Select(c, &v.Type, []string{"stcp", "sudp", "xtcp"}) })
						field(c, "服务名称", &v.ServerName)
						field(c, "服务用户", &v.ServerUser)
						u.Field(c, "私密密钥", func() { u.TextInput(c, &a.TunnelSecret).Password() })
						field(c, "本地监听地址", &v.BindAddr)
						field(c, "本地监听端口", &a.LocalPort)
						u.Field(c, "启用", func() { u.Checkbox(c, &v.Enabled, "应用后启用访问者") })
					} else {
						t := &a.TunnelDraft
						field(c, "名称", &t.Name)
						u.Field(c, "协议", func() {
							u.Select(c, &t.Type, []string{"tcp", "udp", "http", "https", "stcp", "sudp", "xtcp"}).OnChange(func() {
								if t.Type != "tcp" && t.Type != "udp" {
									a.Batch = false
									a.BatchPreview = ""
								}
							})
						})
						field(c, "本地地址", &t.LocalIP)
						u.Field(c, "选择本地服务", func() {
							u.Button(c, "扫描 TCP 监听端口…").Disabled(a.Busy || t.Type == "udp" || t.Type == "sudp").OnClick(a.ScanPorts)
						})
						field(c, "本地端口", &a.LocalPort)
						switch t.Type {
						case "tcp", "udp":
							field(c, "远程端口", &a.RemotePort)
							if a.EditingIndex < 0 {
								u.Field(c, "批量端口", func() { u.Checkbox(c, &a.Batch, "填写范围，如 6000-6010") })
							}
						case "http", "https":
							field(c, "域名（逗号分隔）", &a.Domains)
							field(c, "子域名", &t.Subdomain)
							if t.Type == "http" {
								field(c, "HTTP 路径", &a.Locations)
								field(c, "HTTP 用户", &t.HTTPUser)
								u.Field(c, "HTTP 密码", func() { u.TextInput(c, &a.HTTPPassword).Password() })
							}
						default:
							u.Field(c, "私密密钥", func() { u.TextInput(c, &a.TunnelSecret).Password() })
						}
						u.Field(c, "压缩", func() { u.Checkbox(c, &t.Compression, "启用数据压缩") })
						u.Field(c, "加密", func() { u.Checkbox(c, &t.Encryption, "启用代理数据加密") })
						u.Field(c, "启用", func() { u.Checkbox(c, &t.Enabled, "应用后启用隧道") })
					}
				})
			})
			if a.Batch {
				u.Button(c, "预览批量条目").OnClick(a.PreviewBatch)
				if a.BatchPreview != "" {
					u.Text(c, a.BatchPreview).FontSize(12).Wrap()
				}
			}
			a.formError(c)
			u.Row(c).Gap(8).Children(func() {
				if a.EditingIndex >= 0 {
					u.Button(c, "删除…").Disabled(a.Busy).OnClick(a.DeleteTunnel)
				}
				u.Spacer(c)
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.requestEditorClose("tunnel") })
				u.PrimaryButton(c, "保存").Disabled(a.Busy).OnClick(func() { a.SaveTunnel(false) })
				u.Button(c, "保存并应用").Disabled(a.Busy).OnClick(func() { a.SaveTunnel(true) })
			})
		})
	})
	if modal.Dismissed() {
		a.TunnelOpen = true
		a.requestEditorClose("tunnel")
	}
}
func splitValues(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
func (a *App) DeleteTunnel() {
	p := copyProfile(a.TunnelProfile)
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "删除并应用此条目？", Detail: "运行中的对应隧道可能立即中断。", Buttons: []string{"取消", "删除并应用"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
	if err != nil || r.Button != 1 {
		return
	}
	if a.EditingVisitor {
		p.Visitors = append(p.Visitors[:a.EditingIndex], p.Visitors[a.EditingIndex+1:]...)
	} else {
		p.Tunnels = append(p.Tunnels[:a.EditingIndex], p.Tunnels[a.EditingIndex+1:]...)
	}
	persisted := false
	cmd := saveIntent(p, nil, true, "apply")
	a.DoResult("删除并应用配置", func(ctx context.Context) error {
		result, err := a.Controller.Execute(ctx, cmd)
		persisted = result.Persisted
		return err
	}, func(err error) {
		if persisted {
			a.finishEditor("tunnel", false)
			a.SelectedRow = -1
		}
	})
}
func (a *App) SaveTunnel(apply bool) {
	p := copyProfile(a.TunnelProfile)
	stamp := a.tunnelStamp()
	var saved domain.Profile
	persisted := false
	shouldSave := stamp != a.tunnelBaseline || a.EditingIndex < 0 && len(a.batchSavedIDs) == 0
	var batchIDs []string
	if len(a.batchSavedIDs) > 0 {
		remove := map[string]bool{}
		for _, id := range a.batchSavedIDs {
			remove[id] = true
		}
		keep := p.Tunnels[:0]
		for _, t := range p.Tunnels {
			if !remove[t.ID] {
				keep = append(keep, t)
			}
		}
		p.Tunnels = keep
	}
	values := map[string]string{}
	if a.EditingVisitor {
		v := a.VisitorDraft
		port, err := strconv.Atoi(a.LocalPort)
		if err != nil {
			a.FormError = "本地监听端口必须是整数"
			return
		}
		v.BindPort = port
		values["secret/"+v.ID] = a.TunnelSecret
		if a.EditingIndex < 0 {
			p.Visitors = append(p.Visitors, v)
		} else {
			p.Visitors[a.EditingIndex] = v
		}
	} else {
		t := a.TunnelDraft
		t.Domains = splitValues(a.Domains)
		t.Locations = splitValues(a.Locations)
		values["secret/"+t.ID] = a.TunnelSecret
		values["http/"+t.ID] = a.HTTPPassword
		if a.Batch {
			items, err := config.ExpandPorts(t, a.LocalPort, a.RemotePort)
			if err != nil {
				a.FormError = err.Error()
				return
			}
			if a.BatchPreview == "" || a.batchPreviewStamp != a.tunnelStamp() {
				a.FormError = "请先预览当前批量端口条目，修改后需要重新预览"
				return
			}
			p.Tunnels = append(p.Tunnels, items...)
			for _, item := range items {
				batchIDs = append(batchIDs, item.ID)
			}
		} else {
			port, err := strconv.Atoi(a.LocalPort)
			if err != nil {
				a.FormError = "本地端口必须是整数"
				return
			}
			t.LocalPort = port
			if t.Type == "tcp" || t.Type == "udp" {
				t.RemotePort, err = strconv.Atoi(a.RemotePort)
				if err != nil {
					a.FormError = "远程端口必须是整数"
					return
				}
			}
			if a.EditingIndex < 0 {
				p.Tunnels = append(p.Tunnels, t)
			} else {
				p.Tunnels[a.EditingIndex] = t
			}
		}
	}
	if !shouldSave {
		batchIDs = nil
	}
	if !shouldSave && !apply {
		a.finishEditor("tunnel", false)
		a.SelectedRow = -1
		return
	}
	after := ""
	if apply {
		after = "apply"
	}
	cmd := saveIntent(p, values, shouldSave, after)
	a.DoResult("校验并保存隧道", func(ctx context.Context) error {
		result, err := a.Controller.Execute(ctx, cmd)
		persisted = result.Persisted
		if persisted {
			saved, _ = result.State.Profile(p.ID)
		}
		return err
	}, func(err error) {
		if persisted {
			a.syncTunnelSave(saved, stamp, batchIDs)
		}
		if err == nil && a.tunnelStamp() == stamp {
			a.finishEditor("tunnel", false)
			a.SelectedRow = -1
		} else if persisted && err != nil {
			a.FormError = "配置已保存，应用未完成：" + err.Error()
		}
	})
}

func (a *App) PreviewBatch() {
	items, err := config.ExpandPorts(a.TunnelDraft, a.LocalPort, a.RemotePort)
	if err != nil {
		a.FormError = err.Error()
		a.BatchPreview = ""
		return
	}
	a.BatchPreview = fmt.Sprintf("将创建 %d 条隧道：%s → %s", len(items), items[0].Name, items[len(items)-1].Name)
	a.batchPreviewStamp = a.tunnelStamp()
	a.FormError = ""
}
