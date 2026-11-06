package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
)

// afterUI schedules only dispatch from the timer; all state remains on the UI thread.
func (a *App) afterUI(delay time.Duration, fn func()) *time.Timer {
	dispatch := a.uiDispatch()
	if dispatch == nil {
		return nil
	}
	return time.AfterFunc(delay, func() { dispatch(fn) })
}
func (a *App) queueLogSearch() {
	if a.logSearchTimer != nil {
		a.logSearchTimer.Stop()
	}
	a.logSearchGeneration++
	generation, query := a.logSearchGeneration, a.LogSearch
	a.logSearchTimer = a.afterUI(150*time.Millisecond, func() {
		if a.logSearchGeneration != generation {
			return
		}
		a.logAppliedSearch = query
		a.logFilterKey = ""
	})
}
func (a *App) filteredLogs() []logging.Line {
	var first, last uint64
	if len(a.Logs) > 0 {
		first = a.Logs[0].Seq
		last = a.Logs[len(a.Logs)-1].Seq
	}
	tag, valid := a.logTunnelTag()
	key := fmt.Sprintf("%s/%d/%d/%d/%s/%s/%s/%s/%s/%t", a.Selected, first, last, len(a.Logs), a.LogLevel, a.logAppliedSearch, a.LogFrom, a.LogUntil, tag, valid)
	if key == a.logFilterKey {
		return a.logFiltered
	}
	query := strings.ToLower(a.logAppliedSearch)
	from, until, err := a.logTimeBounds()
	if err != nil || !valid {
		a.logFilterKey, a.logFiltered = key, nil
		return nil
	}
	allLevels := a.LogLevel == "" || a.LogLevel == "全部"
	if allLevels && query == "" && tag == "" && from.IsZero() && until.IsZero() {
		// Log snapshots are immutable on the UI thread. With no filter, reuse
		// the snapshot instead of scanning and copying up to 10,000 lines.
		a.logFilterKey, a.logFiltered = key, a.Logs
		return a.logFiltered
	}
	out := make([]logging.Line, 0, len(a.Logs))
	for _, line := range a.Logs {
		if !from.IsZero() && line.Time.Before(from) || !until.IsZero() && !line.Time.Before(until) {
			continue
		}
		if tag != "" && !strings.Contains(line.Text, tag) {
			continue
		}
		if (allLevels || a.LogLevel == line.Level) && (query == "" || strings.Contains(strings.ToLower(line.Text), query)) {
			out = append(out, line)
		}
	}
	a.logFilterKey, a.logFiltered = key, out
	return out
}

func (a *App) QueueTunnelToggle(profileID, itemID string, visitor, enabled bool) {
	if a.disposed || a.Busy && !a.toggleWaiting {
		return
	}
	p, ok := a.pendingToggles[profileID]
	if !ok {
		p, ok = a.State.Profile(profileID)
	}
	if !ok || p.Mode != "managed" {
		return
	}
	p = copyProfile(p)
	found := false
	if visitor {
		for i := range p.Visitors {
			if p.Visitors[i].ID == itemID {
				p.Visitors[i].Enabled = enabled
				found = true
			}
		}
	} else {
		for i := range p.Tunnels {
			if p.Tunnels[i].ID == itemID {
				p.Tunnels[i].Enabled = enabled
				found = true
			}
		}
	}
	if !found {
		return
	}
	if a.pendingToggles == nil {
		a.pendingToggles = map[string]domain.Profile{}
	}
	a.pendingToggles[profileID] = p
	for i := range a.State.Profiles {
		if a.State.Profiles[i].ID == profileID {
			a.State.Profiles[i] = p
		}
	}
	a.viewVersion++
	a.Busy, a.toggleWaiting = true, true
	a.Notice = "正在合并隧道开关更改…"
	if a.toggleTimer != nil {
		a.toggleTimer.Stop()
	}
	a.toggleGeneration++
	generation := a.toggleGeneration
	a.toggleTimer = a.afterUI(250*time.Millisecond, func() {
		if a.toggleGeneration == generation {
			a.flushToggles()
		}
	})
	if a.toggleTimer == nil {
		a.flushToggles()
	}
}
func (a *App) flushToggles() {
	if !a.toggleWaiting {
		return
	}
	profiles := a.pendingToggles
	a.toggleWaiting, a.Busy = false, false
	commands := map[string]app.Command{}
	for id, p := range profiles {
		commands[id] = toggleIntent(p)
	}
	a.DoResult("保存并应用隧道开关", func(ctx context.Context) error {
		ids := make([]string, 0, len(profiles))
		for id := range profiles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var errs []error
		for _, id := range ids {
			p := profiles[id]
			if _, err := a.Controller.Execute(ctx, commands[id]); err != nil {
				errs = append(errs, fmt.Errorf("连接 %s：%w", p.Name, err))
			}
		}
		return errors.Join(errs...)
	}, func(error) {
		a.pendingToggles = nil
		a.State.SchemaVersion = 0
		a.Refresh()
	})
}

// CancelPendingUI runs only after quit was confirmed. Pending unsaved toggles are discarded.
func (a *App) CancelPendingUI() {
	if a.toggleTimer != nil {
		a.toggleTimer.Stop()
	}
	if a.logSearchTimer != nil {
		a.logSearchTimer.Stop()
	}
	a.toggleGeneration++
	a.logSearchGeneration++
	a.logAppliedSearch = a.LogSearch
	a.logFilterKey = ""
	if a.toggleWaiting {
		a.toggleWaiting, a.Busy = false, false
		a.pendingToggles = nil
		a.State.SchemaVersion = 0
		a.Refresh()
	}
}
