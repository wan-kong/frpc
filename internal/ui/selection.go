package ui

import (
	"context"
	"errors"
	"sort"
	"strings"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
)

func (a *App) sortRows(rows []row) {
	value := func(r row) string {
		switch a.Sort.Column {
		case "协议":
			if r.IsVisitor {
				return r.Visitor.Type
			}
			return r.Tunnel.Type
		case "状态":
			return r.Status
		default:
			if r.IsVisitor {
				return r.Visitor.Name
			}
			return r.Tunnel.Name
		}
	}
	if a.Sort.Column != "" {
		sort.SliceStable(rows, func(i, j int) bool {
			cmp := strings.Compare(value(rows[i]), value(rows[j]))
			if a.Sort.Descending {
				return cmp > 0
			}
			return cmp < 0
		})
	}
}
func (a *App) BatchToggle(enabled bool) {
	selected := map[string]bool{}
	for id := range a.Selection.All() {
		selected[id] = true
	}
	var profiles []domain.Profile
	for _, original := range a.State.Profiles {
		p := copyProfile(original)
		changed := false
		for i := range p.Tunnels {
			if selected[p.Tunnels[i].ID] {
				p.Tunnels[i].Enabled = enabled
				changed = true
			}
		}
		for i := range p.Visitors {
			if selected[p.Visitors[i].ID] {
				p.Visitors[i].Enabled = enabled
				changed = true
			}
		}
		if changed {
			profiles = append(profiles, p)
		}
	}
	commands := make([]app.Command, 0, len(profiles))
	for _, p := range profiles {
		commands = append(commands, toggleIntent(p))
	}
	a.Do("批量应用启用状态", func(ctx context.Context) error {
		var errs []error
		for _, cmd := range commands {
			if _, err := a.Controller.Execute(ctx, cmd); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}, func() { a.Selection.Clear() })
}
