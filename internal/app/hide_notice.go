package app

import "frp-desktop/internal/domain"

// AcknowledgeHideNotice changes only this preference, so it cannot overwrite
// another settings window's network/login/theme edits.
func (c *Controller) AcknowledgeHideNotice() (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Store.Snapshot().Settings.HideNoticeAcknowledged {
		return nil
	}
	return c.Store.Update(func(s *domain.State) error { s.Settings.HideNoticeAcknowledged = true; return nil })
}
