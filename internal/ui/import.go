package ui

import (
	"context"
	"fmt"
	"frp-desktop/internal/app"
	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
	"github.com/egoist/mygo"
	"path/filepath"
	"strings"
)

func (a *App) previewImport(path string) {
	var data []byte
	var preview config.ImportResult
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	a.Do("解析导入预览", func(context.Context) error {
		var err error
		data, err = readBounded(path, 2<<20)
		if err != nil {
			return err
		}
		preview, err = config.Import(data, name, &storage.MemorySecrets{})
		return err
	}, func() {
		p := preview.Profile
		detail := fmt.Sprintf("名称：%s\nfrps：%s:%d\n隧道：%d\n访问者：%d\n%s\n原文件不会被覆盖，导入后不会自动连接。", p.Name, p.ServerAddr, p.ServerPort, len(p.Tunnels), len(p.Visitors), preview.Reason)
		if p.Mode == "raw" {
			detail = "此配置将以加密原文保存。\n" + detail
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageQuestion, Message: "导入此连接？", Detail: detail, Buttons: []string{"取消", "导入连接"}, DefaultButton: 1, CancelButton: 0})
		if err != nil || result.Button != 1 {
			return
		}
		cmd := app.Command{Meta: app.CommandMeta{OperationID: domain.NewID(), ProfileID: domain.NewID()}, Action: app.ImportTOML{Data: data, Name: name}}
		var imported app.CommandResult
		a.Do("校验并导入连接", func(ctx context.Context) error {
			var err error
			imported, err = a.Controller.Execute(ctx, cmd)
			return err
		}, func() { a.SelectScope(imported.ProfileID); a.Notice = imported.Note })
	})
}
