package ui

import "github.com/egoist/mygo"

func (a *App) HideMainWindow() {
	if a.Window == nil || a.disposed {
		return
	}
	a.hideWithNotice(func() error {
		_, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageInfo, Message: "关闭窗口后，连接继续在后台运行", Detail: "可从菜单栏或 Dock 重新打开窗口。本次会话的草稿会保留；退出应用才会停止连接。", Buttons: []string{"知道了"}, DefaultButton: 0, CancelButton: 0})
		return err
	}, a.Window.Hide)
}
func (a *App) hideWithNotice(showNotice func() error, hide func()) {
	if a.disposed {
		return
	}
	if a.hideNoticeSeen || a.State.Settings.HideNoticeAcknowledged {
		hide()
		return
	}
	err := showNotice()
	if a.disposed {
		return
	}
	a.hideNoticeSeen = true
	hide()
	if err != nil {
		a.Notice = "窗口已隐藏，连接继续运行。首次提示未能显示：" + err.Error()
		return
	}
	dispatch := a.uiDispatch()
	if dispatch == nil {
		a.Notice = "本次已确认后台运行，但无法记录此偏好"
		return
	}
	controller := a.Controller
	go func() {
		err := controller.AcknowledgeHideNotice()
		dispatch(func() {
			if err != nil {
				a.Notice = "本次已确认后台运行，但偏好记录失败，下次启动可能再次提示：" + err.Error()
				return
			}
			a.Refresh()
		})
	}()
}
