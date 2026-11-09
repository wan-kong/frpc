package ui

import (
	"context"
	"frp-desktop/internal/app"
	"github.com/egoist/mygo"
)

func (a *App) ConvertToRaw() {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: "改为编辑原文？", Detail: "会生成包含凭证的配置文件，只包含已启用的隧道和访问者。之前的表单配置会留在快照中，之后通过文本编辑。", Buttons: []string{"取消", "改为编辑原文"}, DefaultButton: 0, CancelButton: 0})
	if err != nil || result.Button != 1 {
		return
	}
	cmd := profileCommand(a.RawProfile, app.ConvertProfileToRaw{})
	a.Do("校验并转换配置模式", func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, func() { a.finishEditor("raw", false) })
}
