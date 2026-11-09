package ui

import (
	"fmt"
	"github.com/egoist/mygo"
	u "github.com/egoist/mygo/ui"
	"strings"
)

func (a *App) profileField(c *u.Context, label string, value *string, path string) {
	err := ""
	if a.FormField == path {
		err = a.FormError
	}
	u.Field(c, label, func() { u.TextInput(c, value).FillWidth() }).Error(err)
}
func (a *App) CopyLogs() {
	if err := a.logFilterError(); err != nil {
		a.Notice = err.Error()
		return
	}
	lines := a.filteredLogs()
	if len(lines) == 0 {
		a.Notice = "没有可复制的日志"
		return
	}
	var b strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&b, "%s [%s] %s\n", line.Time.Format("15:04:05"), line.Level, line.Text)
	}
	mygo.Clipboard.WriteText(b.String())
	a.Notice = "已复制当前筛选的脱敏日志"
}
