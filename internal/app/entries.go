package app

import (
	"context"
	"fmt"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

// transformEntries atomically persists all affected profiles before callers apply them.
func (c *Controller) transformEntries(ctx context.Context, ids []string, destination string, remove bool) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.Store.Snapshot()
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	changed := map[string]domain.Profile{}
	var moved []domain.Tunnel
	var visitors []domain.Visitor
	for _, p := range state.Profiles {
		if p.Mode != "managed" {
			continue
		}
		original := p
		var tunnels []domain.Tunnel
		var vs []domain.Visitor
		for _, x := range p.Tunnels {
			if selected[x.ID] && (remove || p.ID != destination) {
				moved = append(moved, x)
			} else {
				tunnels = append(tunnels, x)
			}
		}
		for _, v := range p.Visitors {
			if selected[v.ID] && (remove || p.ID != destination) {
				visitors = append(visitors, v)
			} else {
				vs = append(vs, v)
			}
		}
		if len(tunnels) != len(p.Tunnels) || len(vs) != len(p.Visitors) {
			p.Tunnels = tunnels
			p.Visitors = vs
			changed[original.ID] = p
		}
	}
	if !remove {
		target, ok := state.Profile(destination)
		if !ok || target.Mode != "managed" {
			return nil, fmt.Errorf("请选择可视化模式的目标连接")
		}
		if p, ok := changed[destination]; ok {
			target = p
		}
		target.Tunnels = append(target.Tunnels, moved...)
		target.Visitors = append(target.Visitors, visitors...)
		changed[target.ID] = target
	}
	if len(changed) == 0 {
		return nil, fmt.Errorf("未选择可操作条目")
	}
	for _, p := range changed {
		if err := domain.Validate(p); err != nil {
			return nil, err
		}
		co, err := c.Resolve(p)
		if err != nil {
			return nil, err
		}
		if _, _, err = c.Runtime.Verify(ctx, p, co); err != nil {
			return nil, err
		}
	}
	err := c.Store.Update(func(s *domain.State) error {
		for id, next := range changed {
			found := false
			for i, old := range s.Profiles {
				if old.ID == id {
					if old.Revision != next.Revision {
						return storage.ErrConflict
					}
					next.Revision++
					s.Profiles[i] = next
					found = true
					break
				}
			}
			if !found {
				return storage.ErrConflict
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var affected []string
	for id := range changed {
		affected = append(affected, id)
	}
	return affected, nil
}
