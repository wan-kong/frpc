package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CloseForQuit is called by the serialized application quit coordinator.
func (c *Controller) CloseForQuit(relaunch bool, ids []string) error {
	return c.closeForQuit(relaunch, ids, c.Runtime.Close)
}

func (c *Controller) discardResume() error {
	err := os.Remove(filepath.Join(c.Store.Dir, "resume.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法清理更新恢复记录：%w", err)
	}
	return nil
}

func (c *Controller) closeForQuit(relaunch bool, ids []string, closeRuntime func() error) error {
	if relaunch {
		// Prepare before stopping: a full/read-only disk must not interrupt
		// healthy connections. A failed close compensates this intent below.
		if err := c.WriteResume(ids); err != nil {
			return fmt.Errorf("无法保存更新恢复记录：%w", err)
		}
	} else if err := c.discardResume(); err != nil {
		return err
	}
	if err := closeRuntime(); err != nil {
		if relaunch {
			return errors.Join(err, c.discardResume())
		}
		return err
	}
	return nil
}
