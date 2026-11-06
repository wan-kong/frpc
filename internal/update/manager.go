package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"frp-desktop/internal/storage"
	"github.com/egoist/mygo"
)

type Release struct {
	Version string
	Notes   string
	Install func(context.Context, func(int64, int64)) error
}
type Engine interface {
	Enabled() bool
	Check(context.Context) (*Release, error)
}
type MyGoEngine struct{}

func (MyGoEngine) Enabled() bool { return mygo.Updater.Enabled() }
func (MyGoEngine) Check(ctx context.Context) (*Release, error) {
	up, err := mygo.Updater.Check(ctx)
	if err != nil || up == nil {
		return nil, err
	}
	return &Release{Version: up.Version, Notes: up.Notes, Install: up.Install}, nil
}

type Preferences struct {
	AutomaticChecks    bool
	AutomaticDownloads bool
	Skipped            string
	LastCheck          time.Time
}
type Snapshot struct {
	Stage       string
	Version     string
	Notes       string
	Error       string
	Downloaded  int64
	Total       int64
	Preferences Preferences
	Offer       bool
}
type Manager struct {
	pending  atomic.Bool
	mu       sync.Mutex
	Engine   Engine
	Dir      string
	state    Snapshot
	release  *Release
	OnChange func()
}

func New(dir string, engine Engine) (*Manager, error) {
	m := &Manager{Dir: dir, Engine: engine, state: Snapshot{Stage: "idle", Preferences: Preferences{AutomaticChecks: true}}}
	b, err := os.ReadFile(filepath.Join(dir, "updates.json"))
	if err == nil {
		if err = json.Unmarshal(b, &m.state.Preferences); err != nil {
			m.state.Stage = "error"
			m.state.Error = "更新偏好损坏，已停用自动检查；原文件未被覆盖。"
			m.state.Preferences = Preferences{}
		}
	} else if !os.IsNotExist(err) {
		m.state.Stage = "error"
		m.state.Error = "无法读取更新偏好，已停用自动检查。"
		m.state.Preferences = Preferences{}
	}
	return m, nil
}
func (m *Manager) Snapshot() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.state }
func (m *Manager) notify() {
	if m.OnChange != nil && m.pending.CompareAndSwap(false, true) {
		time.AfterFunc(50*time.Millisecond, func() { m.pending.Store(false); m.OnChange() })
	}
}
func (m *Manager) SetPreferences(p Preferences) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = storage.AtomicWrite(filepath.Join(m.Dir, "updates.json"), b, 0600); err != nil {
		return err
	}
	m.state.Preferences = p
	return nil
}

func (m *Manager) changePreferences(fn func(*Preferences)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.state.Preferences
	fn(&p)
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = storage.AtomicWrite(filepath.Join(m.Dir, "updates.json"), b, 0600); err != nil {
		return err
	}
	m.state.Preferences = p
	return nil
}
func (m *Manager) SetAutomaticChecks(value bool) error {
	return m.changePreferences(func(p *Preferences) { p.AutomaticChecks = value })
}
func (m *Manager) SetAutomaticDownloads(value bool) error {
	return m.changePreferences(func(p *Preferences) { p.AutomaticDownloads = value })
}
func (m *Manager) Check(ctx context.Context, manual bool) error {
	m.mu.Lock()
	if m.state.Stage == "checking" || m.state.Stage == "installing" || m.state.Stage == "ready" {
		m.mu.Unlock()
		return nil
	}
	m.state.Stage = "checking"
	m.state.Error = ""
	m.state.Offer = manual
	m.mu.Unlock()
	m.notify()
	if !m.Engine.Enabled() {
		m.mu.Lock()
		m.state.Stage = "disabled"
		m.state.Error = "开发模式或当前安装位置不支持自更新；发行版需配置公开仓库与更新公钥。"
		m.mu.Unlock()
		m.notify()
		return nil
	}
	up, err := m.Engine.Check(ctx)
	m.mu.Lock()
	p := m.state.Preferences
	p.LastCheck = time.Now()
	m.release = up
	auto := false
	if err != nil {
		m.state.Stage = "error"
		m.state.Error = err.Error()
	} else if up == nil {
		m.state.Stage = "current"
	} else {
		m.state.Stage = "available"
		m.state.Version = up.Version
		m.state.Notes = up.Notes
		m.state.Offer = manual || up.Version != p.Skipped
		auto = p.AutomaticDownloads && up.Version != p.Skipped
		if auto && !manual {
			m.state.Offer = false
		}
	}
	m.mu.Unlock()
	if e := m.changePreferences(func(current *Preferences) { current.LastCheck = p.LastCheck }); err == nil {
		err = e
	}
	m.notify()
	if auto {
		return m.Install(ctx)
	}
	return err
}
func (m *Manager) Install(ctx context.Context) error {
	m.mu.Lock()
	if m.release == nil || m.state.Stage == "installing" {
		m.mu.Unlock()
		return fmt.Errorf("没有待安装的更新")
	}
	release := m.release
	m.state.Stage = "installing"
	m.state.Error = ""
	m.state.Downloaded = 0
	m.state.Total = 0
	m.mu.Unlock()
	m.notify()
	err := release.Install(ctx, func(n, total int64) {
		m.mu.Lock()
		m.state.Downloaded = n
		m.state.Total = total
		m.mu.Unlock()
		m.notify()
	})
	m.mu.Lock()
	if err != nil {
		m.state.Stage = "error"
		m.state.Error = err.Error()
	} else {
		m.state.Stage = "ready"
	}
	m.mu.Unlock()
	m.notify()
	return err
}
func (m *Manager) Skip() error {
	m.mu.Lock()
	p := m.state.Preferences
	p.Skipped = m.state.Version
	m.state.Offer = false
	m.mu.Unlock()
	return m.changePreferences(func(current *Preferences) { current.Skipped = p.Skipped })
}
func (m *Manager) Dismiss() { m.mu.Lock(); m.state.Offer = false; m.mu.Unlock() }
func (m *Manager) Run(ctx context.Context) {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if m.Snapshot().Preferences.AutomaticChecks {
				checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				_ = m.Check(checkCtx, false)
				cancel()
			}
			timer.Reset(24 * time.Hour)
		}
	}
}
