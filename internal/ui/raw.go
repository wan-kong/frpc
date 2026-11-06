package ui

import (
	"context"
	"frp-desktop/internal/app"
	"github.com/egoist/mygo"
)

func (a *App) ConvertToRaw() {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "转换为原文配置模式？", Detail: "将生成包含凭证的加密原文，仅包含启用的隧道和访问者。原可视化配置保留在自动快照中。此后通过文本编辑配置，可选择其他现代内核；不会自动重新连接。", Buttons: []string{"取消", "转换为原文"}, DefaultButton: 0, CancelButton: 0})
	if err != nil || result.Button != 1 {
		return
	}
	cmd := profileCommand(a.RawProfile, app.ConvertProfileToRaw{})
	a.Do("校验并转换配置模式", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.finishEditor("raw", false) })
}
