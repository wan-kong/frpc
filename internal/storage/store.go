package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"frp-desktop/internal/domain"
)

var ErrConflict = domain.Failure(domain.Conflict, "配置已被其他操作修改，请重新打开编辑器", "revision", false, nil)

type Store struct {
	writeMu sync.Mutex
	mu      sync.Mutex
	Dir     string
	state   domain.State
}

func (s *Store) Revision() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Revision }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{Dir: dir, state: domain.NewState()}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, fmt.Errorf("配置文件超过 16MiB 限制")
	}
	if err = json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("配置损坏，未覆盖原文件：%w", err)
	}
	if s.state.SchemaVersion != domain.SchemaVersion {
		return nil, fmt.Errorf("不支持的数据版本 %d，未修改原文件", s.state.SchemaVersion)
	}
	return s, nil
}
func clone(s domain.State) domain.State {
	b, _ := json.Marshal(s)
	var c domain.State
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Store) Snapshot() domain.State { s.mu.Lock(); defer s.mu.Unlock(); return clone(s.state) }
func (s *Store) Update(fn func(*domain.State) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	next := clone(s.state)
	previousRevision := s.state.Revision
	s.mu.Unlock()
	if err := fn(&next); err != nil {
		return err
	}
	next.Revision++
	known := map[string]bool{}
	for _, ref := range next.SecretIDs {
		known[ref] = true
	}
	register := func(p domain.Profile) {
		for _, ref := range domain.SecretRefs(p) {
			if !known[ref] {
				next.SecretIDs = append(next.SecretIDs, ref)
				known[ref] = true
			}
		}
	}
	for _, p := range next.Profiles {
		register(p)
	}
	for _, p := range next.Trash {
		register(p.Profile)
	}
	if ref := next.Settings.DownloadProxyRef; ref != "" && !known[ref] {
		next.SecretIDs = append(next.SecretIDs, ref)
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return fmt.Errorf("配置超过 16MiB 限制，未覆盖原文件")
	}
	path := filepath.Join(s.Dir, "state.json")
	if old, err := os.ReadFile(path); err == nil {
		dir := filepath.Join(s.Dir, "backups")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err = AtomicWrite(filepath.Join(dir, fmt.Sprintf("%020d.json", previousRevision)), old, 0600); err != nil {
			return err
		}
		entries, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(entries)
		for len(entries) > 10 {
			_ = os.Remove(entries[0])
			entries = entries[1:]
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = AtomicWrite(path, append(b, '\n'), 0600); err != nil {
		return err
	}
	s.mu.Lock()
	s.state = next
	s.mu.Unlock()
	return nil
}
func (s *Store) SaveProfile(p domain.Profile, expected uint64) error {
	if err := domain.Validate(p); err != nil {
		return err
	}
	return s.Update(func(st *domain.State) error {
		for i, old := range st.Profiles {
			if old.ID == p.ID {
				if old.Revision != expected {
					return ErrConflict
				}
				p.Revision = old.Revision + 1
				st.Profiles[i] = p
				return nil
			}
		}
		if expected != 0 {
			return ErrConflict
		}
		p.Revision = 1
		st.Profiles = append(st.Profiles, p)
		return nil
	})
}
func (s *Store) DeleteProfile(id string) error {
	return s.Update(func(st *domain.State) error {
		for i, p := range st.Profiles {
			if p.ID == id {
				st.Trash = append(st.Trash, domain.DeletedProfile{Profile: p, DeletedAt: time.Now()})
				st.Profiles = append(st.Profiles[:i], st.Profiles[i+1:]...)
				return nil
			}
		}
		return os.ErrNotExist
	})
}
func (s *Store) Restore(id string) error {
	return s.Update(func(st *domain.State) error {
		for i, p := range st.Trash {
			if p.Profile.ID == id {
				if _, ok := st.Profile(id); ok {
					return ErrConflict
				}
				p.Profile.Revision++
				p.Profile.AutoConnect = false
				st.Profiles = append(st.Profiles, p.Profile)
				st.Trash = append(st.Trash[:i], st.Trash[i+1:]...)
				return nil
			}
		}
		return os.ErrNotExist
	})
}

func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
