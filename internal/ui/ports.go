package ui

import (
	"context"
	"fmt"
	"strconv"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

func (a *App) ImportCA() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "选择信任的 CA 证书", Filters: []mygo.FileFilter{{Name: "PEM 证书", Extensions: []string{"pem", "crt"}}}})
	if err != nil || len(paths) == 0 {
		return
	}
	path := paths[0]
	var managed string
	operationID := domain.NewID()
	a.Do("导入 CA 证书", func(ctx context.Context) error {
		b, err := readBounded(path, 1<<20)
		if err != nil {
			return err
		}
		result, err := a.Controller.ExecuteGlobal(ctx, app.GlobalCommand{OperationID: operationID, Action: app.ImportCA{Data: b}})
		managed = result.Note
		return err
	}, func() { a.ProfileDraft.TrustedCA = managed })
}
func (a *App) ScanPorts() {
	var ports []platform.LocalPort
	a.Do("扫描本机 TCP 监听端口", func(ctx context.Context) error { var err error; ports, err = platform.ListeningPorts(ctx); return err }, func() { a.LocalPorts = ports; a.PortsOpen = true })
}
func (a *App) portsModal(c *u.Context) {
	u.Modal(c, &a.PortsOpen, func() {
		u.Column(c).Width(430).MaxHeight(400).Gap(12).Children(func() {
			u.Text(c, "选择本地 TCP 服务").FontSize(18).Bold()
			u.Text(c, "只显示当前用户可读取的监听信息；没有结果时可以手动填写。").FontSize(12).Wrap()
			u.List(c, nil, len(a.LocalPorts), func(i int) {
				p := a.LocalPorts[i]
				u.Button(c, fmt.Sprintf("%s（%s:%d）", p.Process, p.Address, p.Port)).FillWidth().OnClick(func() { a.TunnelDraft.LocalIP = p.Address; a.LocalPort = strconv.Itoa(p.Port); a.PortsOpen = false })
			}).Height(240)
			u.Button(c, "取消").OnClick(func() { a.PortsOpen = false })
		})
	})
}
