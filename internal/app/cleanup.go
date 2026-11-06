package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"frp-desktop/internal/domain"
)

// CollectSecrets runs at startup. Backups and running versions retain their keys.
func (c *Controller) CollectSecrets() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.Store.Snapshot()
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	expired := false
	for _, p := range state.Trash {
		if p.DeletedAt.Before(cutoff) {
			expired = true
			break
		}
	}
	if expired {
		if err := c.Store.Update(func(s *domain.State) error {
			keep := s.Trash[:0]
			for _, p := range s.Trash {
				if !p.DeletedAt.Before(cutoff) {
					keep = append(keep, p)
				}
			}
			s.Trash = keep
			return nil
		}); err != nil {
			return err
		}
		state = c.Store.Snapshot()
	}
	live := map[string]bool{}
	mark := func(s domain.State) {
		for _, p := range s.Profiles {
			for _, ref := range domain.SecretRefs(p) {
				live[ref] = true
			}
		}
		for _, p := range s.Trash {
			for _, ref := range domain.SecretRefs(p.Profile) {
				live[ref] = true
			}
		}
		live[s.Settings.DownloadProxyRef] = true
	}
	mark(state)
	files, err := filepath.Glob(filepath.Join(c.Store.Dir, "backups", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(b) > 16<<20 {
			return fmt.Errorf("快照超过大小限制，暂不清理凭证")
		}
		var snapshot domain.State
		if err = json.Unmarshal(b, &snapshot); err != nil {
			return fmt.Errorf("发现损坏快照，暂不清理凭证：%w", err)
		}
		mark(snapshot)
	}
	for _, ref := range c.Runtime.SecretReferences() {
		live[ref] = true
	}
	var keep []string
	removed := false
	for _, ref := range state.SecretIDs {
		if live[ref] {
			keep = append(keep, ref)
			continue
		}
		if err := c.Secrets.Delete(ref); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return c.Store.Update(func(s *domain.State) error { s.SecretIDs = keep; return nil })
	}
	return nil
}
