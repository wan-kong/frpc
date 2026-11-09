package logging

import (
	"frp-desktop/internal/domain"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var quotaMu sync.Mutex

// Prune removes only named, rotated logs inside owned profile directories.
// Current files and unrelated files are never selected.
func Prune(root string, limit int64) error {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	dirs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type old struct {
		path string
		size int64
		at   time.Time
	}
	var files []old
	var total int64
	for _, dir := range dirs {
		if !dir.IsDir() || !domain.ValidID(dir.Name()) {
			continue
		}
		for _, name := range []string{"current.log", "1.log", "2.log", "3.log", "4.log"} {
			path := filepath.Join(root, dir.Name(), name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			total += info.Size()
			if name != "current.log" {
				files = append(files, old{path, info.Size(), info.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if total <= limit {
			break
		}
		if err = os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= f.size
	}
	return nil
}
