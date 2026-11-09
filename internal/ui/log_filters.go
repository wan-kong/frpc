package ui

import (
	"fmt"
	"strings"
	"time"

	u "github.com/egoist/mygo/ui"
)

const logTimeFormat = "2006-01-02 15:04:05"

// logLevelKey maps the label shown in the picker back to frpc's level name.
func logLevelKey(label string) string {
	switch label {
	case "错误":
		return "error"
	case "警告":
		return "warn"
	case "信息":
		return "info"
	case "调试":
		return "debug"
	case "跟踪":
		return "trace"
	default:
		return label
	}
}

func (a *App) logFilterError() error {
	if _, _, err := a.logTimeBounds(); err != nil {
		return err
	}
	if _, valid := a.logTunnelTag(); !valid {
		return fmt.Errorf("关联对象已变更，请重新选择或重置筛选")
	}
	return nil
}

func (a *App) logTimeBounds() (time.Time, time.Time, error) {
	parse := func(value, label string) (time.Time, error) {
		if strings.TrimSpace(value) == "" {
			return time.Time{}, nil
		}
		at, err := time.ParseInLocation(logTimeFormat, strings.TrimSpace(value), time.Local)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s格式应为 2026-11-09 14:30:00", label)
		}
		return at, nil
	}
	from, err := parse(a.LogFrom, "开始时间")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	until, err := parse(a.LogUntil, "结束时间")
	if err == nil && !from.IsZero() && !until.IsZero() && until.Before(from) {
		err = fmt.Errorf("结束时间不能早于开始时间")
	}
	if err == nil && !until.IsZero() {
		until = until.Add(time.Second)
	}
	return from, until, err
}

func (a *App) logTunnelChoices() ([]string, map[string]string) {
	choices := []string{"全部日志"}
	names := map[string]string{"全部日志": ""}
	p, ok := a.current()
	if !ok {
		return choices, names
	}
	for _, t := range p.Tunnels {
		label := "隧道：" + t.Name
		choices = append(choices, label)
		names[label] = t.Name
	}
	for _, v := range p.Visitors {
		label := "访问者：" + v.Name
		choices = append(choices, label)
		names[label] = v.Name
	}
	return choices, names
}

func (a *App) logTunnelTag() (string, bool) {
	if a.LogTunnel == "" || a.LogTunnel == "全部日志" {
		return "", true
	}
	_, names := a.logTunnelChoices()
	name, ok := names[a.LogTunnel]
	if !ok {
		return "", false
	}
	return "[" + name + "]", true
}

func (a *App) logFilters(c *u.Context) {
	label := "筛选"
	if a.LogFrom != "" || a.LogUntil != "" || a.LogTunnel != "" && a.LogTunnel != "全部日志" {
		label = "筛选（已启用）"
	}
	anchor := iconButton(c.Key("log-filter-button"), label, filterIcon).OnClick(func() { a.LogFiltersOpen = !a.LogFiltersOpen })
	u.Popover(c, anchor, &a.LogFiltersOpen, func() {
		u.Column(c).Width(290).Gap(10).Padding(12).Label("日志筛选").Children(func() {
			u.Text(c, "时间与对象").Bold()
			u.Text(c, "本地时间，可留空。筛选同时用于复制和导出。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
			u.Field(c, "开始时间", func() { u.TextInput(c, &a.LogFrom).Placeholder("YYYY-MM-DD HH:MM:SS").FillWidth() })
			u.Field(c, "结束时间", func() { u.TextInput(c, &a.LogUntil).Placeholder("YYYY-MM-DD HH:MM:SS").FillWidth() })
			choices, _ := a.logTunnelChoices()
			if a.LogTunnel == "" {
				a.LogTunnel = "全部日志"
			}
			u.Field(c, "关联对象", func() { u.Select(c, &a.LogTunnel, choices).FillWidth() })
			u.Text(c, "只显示带此对象完整名称标签的日志。").FontSize(12).TextColor(c.Theme().TextMuted).Wrap()
			u.Button(c, "重置筛选").OnClick(func() { a.LogFrom, a.LogUntil, a.LogTunnel = "", "", "全部日志" })
		})
	})
}
