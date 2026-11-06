package ui

import (
	"context"
	"fmt"
	"frp-desktop/internal/app"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
)

func (a *App) entryMenu(m *u.Menu) {
	disabled := a.Busy || a.Selection.Len() == 0
	if m.Item("移动所选到连接…").Disabled(disabled).Chosen() {
		a.MoveOpen = true
	}
	if m.Item("删除所选…").Disabled(disabled).Chosen() {
		a.DeleteSelected()
	}
}
func (a *App) selectedIDs() []string {
	var ids []string
	for id := range a.Selection.All() {
		ids = append(ids, id)
	}
	return ids
}
func (a *App) DeleteSelected() {
	ids := a.selectedIDs()
	r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageWarning, Message: fmt.Sprintf("删除 %d 个所选条目并应用？", len(ids)), Detail: "会影响所选条目所在连接的对应隧道或访问者。", Buttons: []string{"取消", "删除并应用"}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1}})
	if err != nil || r.Button != 1 {
		return
	}
	a.transform(ids, "", true)
}
func (a *App) transform(ids []string, destination string, remove bool) {
	cmd, err := app.EntryCommand(a.State, ids, destination, remove)
	if err != nil {
		a.FormError = err.Error()
		return
	}
	var result app.CommandResult
	a.DoResult("保存并应用批量变更", func(ctx context.Context) error {
		var err error
		result, err = a.Controller.Execute(ctx, cmd)
		return err
	}, func(err error) {
		if result.Persisted || err == nil {
			a.MoveOpen = false
			a.Selection.Clear()
			a.SelectedRow = -1
		}
	})
}
func (a *App) moveModal(c *u.Context) {
	u.Modal(c, &a.MoveOpen, func() {
		u.Column(c).Width(440).Gap(12).Children(func() {
			u.Text(c, "移动所选条目").FontSize(18).Bold()
			u.Text(c, "保存后会应用受影响连接。目标连接中不能存在相同名称。").Wrap()
			choices := []string{}
			ids := map[string]string{}
			for _, p := range a.State.Profiles {
				if p.Mode != "managed" {
					continue
				}
				label := p.Name + " · " + p.ID[len(p.ID)-6:]
				choices = append(choices, label)
				ids[label] = p.ID
			}
			u.Select(c, &a.MoveDestination, choices).Label("目标连接")
			a.formError(c)
			u.Row(c).Gap(8).Justify(u.End).Children(func() {
				u.Button(c, "取消").Disabled(a.Busy).OnClick(func() { a.MoveOpen = false })
				u.PrimaryButton(c, "移动并应用").Disabled(a.Busy || ids[a.MoveDestination] == "").OnClick(func() { a.transform(a.selectedIDs(), ids[a.MoveDestination], false) })
			})
		})
	})
}
