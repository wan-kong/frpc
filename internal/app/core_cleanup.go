package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"frp-desktop/internal/domain"
)

var officialCoreID = regexp.MustCompile(`^official-[0-9]+\.[0-9]+\.[0-9]+-(arm64|amd64)$`)

// DeleteCore removes only a registered, unreferenced binary in its exact owned path.
func (c *Controller) DeleteCore(id string) (commandErr error) {
	defer func() { commandErr = commandError(commandErr, domain.StorageFailed) }()
	c.coresMu.Lock()
	defer c.coresMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	co, ok := c.Store.Snapshot().Core(id)
	if !ok {
		return os.ErrNotExist
	}
	return c.Runtime.WithUnusedCore(co.Path, func() error {
		var binary string
		err := c.Store.Update(func(s *domain.State) error {
			co, ok := s.Core(id)
			if !ok {
				return os.ErrNotExist
			}
			if co.Bundled {
				return fmt.Errorf("保底内核不能删除")
			}
			if !domain.ValidID(id) && !officialCoreID.MatchString(id) {
				return fmt.Errorf("无效内核标识")
			}
			binary = filepath.Join(c.Store.Dir, "cores", id, "frpc")
			if co.Path != binary {
				return fmt.Errorf("内核路径不属于此版本的受管目录，未删除")
			}
			if s.Settings.DefaultCoreID == id {
				return fmt.Errorf("请先更改新连接的默认内核")
			}
			referenced := func(state domain.State) bool {
				for _, p := range state.Profiles {
					if p.CoreID == id {
						return true
					}
				}
				for _, p := range state.Trash {
					if p.Profile.CoreID == id {
						return true
					}
				}
				return false
			}
			if referenced(*s) {
				return fmt.Errorf("内核仍被连接或回收区配置引用")
			}
			snapshots, err := filepath.Glob(filepath.Join(c.Store.Dir, "backups", "*.json"))
			if err != nil {
				return err
			}
			for _, path := range snapshots {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var snapshot domain.State
				if len(b) > 16<<20 || json.Unmarshal(b, &snapshot) != nil || snapshot.SchemaVersion != domain.SchemaVersion {
					return fmt.Errorf("无法核对快照引用，未删除内核")
				}
				if referenced(snapshot) {
					return fmt.Errorf("内核仍被保留的恢复快照引用")
				}
			}
			for _, r := range c.Runtime.Snapshot() {
				if r.Running() {
					return fmt.Errorf("存在运行连接，请先停止后删除内核")
				}
			}
			// Reject link traversal; never recursively remove the version directory.
			for _, path := range []string{filepath.Dir(filepath.Dir(binary)), filepath.Dir(binary), binary} {
				st, err := os.Lstat(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				if st.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("内核路径包含符号链接，未删除")
				}
				if path == binary && !st.Mode().IsRegular() {
					return fmt.Errorf("内核不是普通文件，未删除")
				}
			}
			for i, item := range s.Cores {
				if item.ID == id {
					s.Cores = append(s.Cores[:i], s.Cores[i+1:]...)
					break
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err = os.Remove(binary); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("内核记录已移除，但文件清理失败：%w", err)
		}
		if officialCoreID.MatchString(id) {
			if err = os.Remove(filepath.Join(filepath.Dir(binary), "LICENSE")); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("内核已删除，但许可文件清理失败：%w", err)
			}
		}
		_ = os.Remove(filepath.Dir(binary)) // Only an empty owned directory can be removed.
		return nil
	})
}
