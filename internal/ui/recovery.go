package ui

import (
	"fmt"
	"frp-desktop/internal/storage"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
	"log"
)

func RunRecovery(dir string, cause error) {
	message := cause.Error()
	busy := false
	var win *mygo.Window
	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{Title: "frp-desktop · 数据恢复", Width: 640, Height: 360, Content: u.View(func(c *u.Context) {
			u.Column(c).Fill().Padding(28).Gap(16).Children(func() {
				u.Text(c, "暂时无法读取连接配置").FontSize(20).Bold()
				u.Text(c, "原数据已保留，未启动任何连接。").TextColor(c.Theme().TextMuted)
				u.Text(c, message).Wrap().Grow(1)
				u.Row(c).Gap(8).Children(func() {
					u.Button(c, "打开数据目录").OnClick(func() {
						if err := mygo.Shell.OpenPath(dir); err != nil {
							message = err.Error()
						}
					})
					u.Button(c, "退出").OnClick(mygo.App.Quit)
					u.PrimaryButton(c, "恢复最近快照…").Disabled(busy).OnClick(func() {
						busy = true
						go func() {
							snapshot, err := storage.LatestSnapshot(dir)
							win.Update(func() {
								busy = false
								if err != nil {
									message = err.Error()
									return
								}
								result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: win, Type: mygo.MessageWarning, Message: "恢复最近有效快照？", Detail: fmt.Sprintf("将恢复 %d 个连接；当前文件会另存保留。恢复后关闭自动连接。", len(snapshot.Profiles)), Buttons: []string{"取消", "恢复并重新启动"}, DefaultButton: 0, CancelButton: 0})
								if err != nil || result.Button != 1 {
									return
								}
								busy = true
								go func() {
									err := storage.RecoverSnapshot(dir, snapshot)
									win.Update(func() {
										busy = false
										if err != nil {
											message = err.Error()
										} else {
											mygo.App.Relaunch()
										}
									})
								}()
							})
						}()
					})
				})
			})
		})})
	})
	if err := mygo.App.Run(); err != nil {
		log.Print(err)
	}
}
