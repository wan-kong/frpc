package storage

import (
	"encoding/json"
	"fmt"
	"frp-desktop/internal/domain"
	"os"
	"path/filepath"
	"sort"
)

func LatestSnapshot(dir string) (domain.State, error) {
	files, err := filepath.Glob(filepath.Join(dir, "backups", "*.json"))
	if err != nil {
		return domain.State{}, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil || len(b) > 16<<20 {
			continue
		}
		var s domain.State
		if json.Unmarshal(b, &s) != nil || s.SchemaVersion != domain.SchemaVersion {
			continue
		}
		valid := true
		for _, p := range s.Profiles {
			if domain.Validate(p) != nil {
				valid = false
				break
			}
		}
		if valid {
			return s, nil
		}
	}
	return domain.State{}, fmt.Errorf("未找到可恢复的兼容快照，请保留数据目录并从加密备份恢复")
}
func RecoverLatest(dir string) error {
	s, err := LatestSnapshot(dir)
	if err != nil {
		return err
	}
	return RecoverSnapshot(dir, s)
}

// RecoverSnapshot restores exactly the validated snapshot shown in the recovery preview.
func RecoverSnapshot(dir string, s domain.State) error {
	if s.SchemaVersion != domain.SchemaVersion {
		return fmt.Errorf("快照版本不兼容")
	}
	for _, p := range s.Profiles {
		if err := domain.Validate(p); err != nil {
			return err
		}
	}
	// Work on a copy so disabling automatic connection does not mutate the preview.
	s.Profiles = append([]domain.Profile(nil), s.Profiles...)
	path := filepath.Join(dir, ConfigFile)
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = AtomicWrite(filepath.Join(dir, "config.before-recovery-"+domain.NewID()+".json"), original, 0600); err != nil {
		return err
	}
	for i := range s.Profiles {
		s.Profiles[i].AutoConnect = false
	}
	s.Revision++
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, append(data, '\n'), 0600)
}
