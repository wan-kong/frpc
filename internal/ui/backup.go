package ui

import (
	"context"
	"github.com/egoist/mygo"
)

func (a *App) PreviewRestore() {
	blob := append([]byte(nil), a.BackupData...)
	password := a.BackupPassword
	var detail string
	a.Do("解密并预览备份", func(context.Context) error {
		var err error
		detail, err = a.Controller.BackupPreview(blob, password)
		return err
	}, func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageQuestion, Message: "恢复这些连接？", Detail: detail, Buttons: []string{"取消", "恢复连接"}, DefaultButton: 1, CancelButton: 0})
		if err != nil || result.Button != 1 {
			return
		}
		a.Do("恢复备份", func(context.Context) error { return a.Controller.RestoreBackup(blob, password) }, func() { a.BackupOpen = false; a.BackupData = nil; a.BackupPassword = "" })
	})
}
