package ui

import (
	"context"
	"fmt"
	"frp-desktop/internal/app"
	"frp-desktop/internal/runtime"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

func (a *App) InspectResidual(id string) {
	var review runtime.ResidualReview
	a.Do("核对运行进程身份", func(ctx context.Context) error {
		var err error
		review, err = a.Controller.Runtime.InspectResidual(ctx, id)
		return err
	}, func() { a.Residual = review; a.ResidualOpen = true })
}
func (a *App) residualModal(c *u.Context) {
	u.Modal(c, &a.ResidualOpen, func() {
		r := a.Residual
		u.Column(c).Width(580).Gap(12).Children(func() {
			u.Text(c, "检查残留进程").FontSize(18).Bold()
			u.Text(c, r.Reason).Wrap()
			if r.PID > 0 {
				u.Text(c, fmt.Sprintf("PID：%d\n启动时间：%s", r.PID, r.Started)).Selectable()
				u.Text(c, "内核："+r.Binary+"\n运行配置："+r.Config).Font("monospace").FontSize(11).Wrap().Selectable()
			}
			a.formError(c)
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				u.Button(c, "关闭").Disabled(a.Busy).OnClick(func() { a.ResidualOpen = false })
				if r.Status == "verified" {
					u.Button(c, "强制终止…").Disabled(a.Busy).OnClick(func() { a.ResolveResidual(true) })
					u.PrimaryButton(c, "停止此残留进程…").Disabled(a.Busy).OnClick(func() { a.ResolveResidual(false) })
				} else if r.Status == "exited" {
					u.Button(c, "清理已退出记录").Disabled(a.Busy).OnClick(func() { a.ResolveResidual(false) })
				}
			})
		})
	})
}
func (a *App) ResolveResidual(force bool) {
	review := a.Residual
	if review.Status == "verified" {
		message := "停止已确认的残留 frpc？"
		if force {
			message = "强制终止已确认的残留 frpc？"
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: message, Detail: fmt.Sprintf("仅操作已展示的 PID %d，会中断此进程承载的连接；执行前再次核对身份。", review.PID), Buttons: []string{"取消", "确认处理"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
		if err != nil || result.Button != 1 {
			return
		}
	}
	p, ok := a.State.Profile(review.ProfileID)
	if !ok {
		a.FormError = "连接已经不存在"
		return
	}
	cmd := profileCommand(p, app.ResolveResidual{Review: review, Force: force})
	a.DoResult("处理已确认的运行记录", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func(err error) {
		if err == nil {
			a.ResidualOpen = false
		}
	})
}
