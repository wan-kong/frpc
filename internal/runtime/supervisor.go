package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"frp-desktop/internal/config"
	"frp-desktop/internal/core"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	"frp-desktop/internal/storage"
)

type entry struct {
	observationTimer *time.Timer
	observationEpoch uint64
	sessionFailed    bool
	startupPortInUse bool
	activePath       string
	op               sync.Mutex
	snapshot         domain.Runtime
	profile          domain.Profile
	core             domain.Core
	admin            *AdminClient
	cmd              *exec.Cmd
	control          *os.File
	done             chan struct{}
	cancel           context.CancelFunc
	log              *logging.Buffer
	desired          bool
	failureTimes     []time.Time
}
type Supervisor struct {
	chooseAdminPort    func() (int, error)
	observationActive  string
	observationVisible bool
	observationChanged chan struct{}

	pending    atomic.Bool
	mu         sync.Mutex
	coreMu     sync.RWMutex
	entries    map[string]*entry
	Dir        string
	Secrets    storage.Secrets
	Vault      *storage.Vault
	Executable string
	OnChange   func()
	closed     bool
}

func New(dir string, secrets storage.Secrets, executable string) *Supervisor {
	return &Supervisor{observationVisible: true, observationChanged: make(chan struct{}), Dir: dir, Secrets: secrets, Vault: &storage.Vault{Dir: dir, Secrets: secrets}, Executable: executable, entries: map[string]*entry{}}
}
func (s *Supervisor) get(id string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil {
		e = &entry{snapshot: domain.Runtime{ProfileID: id, Process: "stopped", Session: "unknown"}, log: logging.New(filepath.Join(s.Dir, "logs", id), nil)}
		e.log.Notify = s.notify
		e.log.OnLine = func(line logging.Line, raw string) { s.observeLog(e, line, raw) }
		s.entries[id] = e
	}
	return e
}
func (s *Supervisor) notify() {
	if s.OnChange != nil && s.pending.CompareAndSwap(false, true) {
		time.AfterFunc(75*time.Millisecond, func() { s.pending.Store(false); s.OnChange() })
	}
}
func (s *Supervisor) Snapshot() map[string]domain.Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]domain.Runtime{}
	for id, e := range s.entries {
		r := e.snapshot
		r.Proxies = append([]domain.ProxyStatus(nil), r.Proxies...)
		out[id] = r
	}
	return out
}
func (s *Supervisor) Logs(id string) ([]logging.Line, error) {
	if !domain.ValidID(id) {
		return nil, fmt.Errorf("无效连接")
	}
	return s.get(id).log.Snapshot()
}
func (s *Supervisor) set(e *entry, fn func(*domain.Runtime)) {
	s.mu.Lock()
	fn(&e.snapshot)
	s.mu.Unlock()
	s.notify()
}
func (s *Supervisor) Verify(ctx context.Context, p domain.Profile, c domain.Core) ([]byte, *AdminClient, error) {
	if !domain.ValidID(p.ID) {
		return nil, nil, fmt.Errorf("无效连接标识")
	}
	if err := domain.Validate(p); err != nil {
		return nil, nil, err
	}
	hash, err := core.Hash(c.Path)
	if err != nil {
		return nil, nil, domain.Failure(domain.BinaryMissing, "无法读取已选择内核，请检查路径和权限", "coreId", true, err)
	}
	if hash != c.SHA256 {
		return nil, nil, domain.Failure(domain.ChecksumMismatch, "内核文件已改变，请重新导入", "coreId", false, nil)
	}
	if p.Mode == "managed" && !c.Certified {
		return nil, nil, fmt.Errorf("此内核未通过可视化配置认证，请选择认证版本")
	}
	var data []byte
	var admin *AdminClient
	if p.Mode == "raw" {
		data, err = s.Vault.Read(p.RawRef)
		if err == nil {
			err = config.ValidateSource(data)
		}
	} else {
		pick := s.chooseAdminPort
		if pick == nil {
			pick = freeAdminPort
		}
		port, e := pick()
		if e != nil {
			return nil, nil, e
		}
		a := config.Admin{Addr: "127.0.0.1", Port: port, User: "frp-desktop", Password: domain.NewID()}
		admin = NewAdmin(a)
		data, err = config.Compile(p, s.Secrets, &a, true)
	}
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(s.Dir, "runtime", p.ID, ".verify-"+domain.NewID()+".toml")
	defer os.Remove(path)
	if err = storage.AtomicWrite(path, data, 0600); err != nil {
		return nil, nil, err
	}
	if err = verifyFile(ctx, c.Path, path, config.SecretValues(data)); err != nil {
		_ = os.Remove(path)
		return nil, nil, err
	}
	return data, admin, nil
}

type limitedOutput struct{ b []byte }

func (w *limitedOutput) Write(p []byte) (int, error) {
	if len(w.b) < 64<<10 {
		w.b = append(w.b, p[:min(len(p), (64<<10)-len(w.b))]...)
	}
	return len(p), nil
}
func verifyFile(ctx context.Context, binary, path string, secrets []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "verify", "-c", path)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	out := &limitedOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return domain.Failure(domain.Validation, fmt.Sprintf("配置校验失败：%s", logging.Redact(string(out.b), secrets)), "", false, err)
	}
	return nil
}

func (s *Supervisor) Start(ctx context.Context, p domain.Profile, c domain.Core) error {
	e := s.get(p.ID)
	e.op.Lock()
	defer e.op.Unlock()
	s.mu.Lock()
	running := e.cmd != nil
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("应用正在退出")
	}
	if running {
		return nil
	}
	if err := s.CheckResidual(p.ID); err != nil {
		return err
	}
	return s.start(ctx, e, p, c, true)
}
func (s *Supervisor) startAttempt(ctx context.Context, e *entry, p domain.Profile, c domain.Core, reset bool) error {
	s.set(e, func(r *domain.Runtime) { r.Process = "starting"; r.Error = ""; r.Operation = "校验配置" })
	data, admin, err := s.Verify(ctx, p, c)
	if err != nil {
		s.set(e, func(r *domain.Runtime) { r.Process = "failed"; r.Error = err.Error(); r.Operation = "" })
		return err
	}
	if err = checkAdminPort(admin); err != nil {
		s.set(e, func(r *domain.Runtime) { r.Process = "failed"; r.Operation = ""; r.Error = err.Error() })
		return err
	}
	active := filepath.Join(s.Dir, "runtime", p.ID, "active-"+domain.NewID()+".toml")
	if err = storage.AtomicWrite(active, data, 0600); err != nil {
		return err
	}
	adopted := false
	defer func() {
		if !adopted {
			_ = os.Remove(active)
		}
	}()
	e.log.SetSecrets(config.SecretValues(data))
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.Command(s.Executable, "--frpc-session", c.Path, active)
	cmd.Stdin = read
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	cmd.Dir = filepath.Dir(active)
	processGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		read.Close()
		write.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		read.Close()
		write.Close()
		return err
	}
	handshake, ready, err := os.Pipe()
	if err != nil {
		read.Close()
		write.Close()
		return err
	}
	defer handshake.Close()
	cmd.ExtraFiles = []*os.File{ready}
	if err = cmd.Start(); err != nil {
		handshake.Close()
		ready.Close()
		read.Close()
		write.Close()
		s.set(e, func(r *domain.Runtime) { r.Process = "failed"; r.Operation = ""; r.Error = err.Error() })
		return err
	}
	_ = ready.Close()
	started := make(chan int, 1)
	go func() {
		line, _ := bufio.NewReader(io.LimitReader(handshake, 4096)).ReadString('\n')
		pid, _ := strconv.Atoi(strings.TrimSpace(line))
		started <- pid
	}()
	actualPID := 0
	select {
	case actualPID = <-started:
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
	}
	if actualPID <= 0 {
		_ = write.Close()
		_ = killGroup(cmd)
		_ = cmd.Wait()
		_ = read.Close()
		s.set(e, func(r *domain.Runtime) {
			r.Process = "failed"
			r.Operation = ""
			r.Error = "frpc 启动失败，请查看内核路径与运行权限"
		})
		return fmt.Errorf("frpc 启动握手失败")
	}
	_ = read.Close()
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	e.profile = p
	e.activePath = active
	e.core = c
	e.admin = admin
	e.cmd = cmd
	e.control = write
	e.done = done
	e.cancel = cancel
	e.desired = false
	if reset {
		e.failureTimes = nil
	}
	e.snapshot.Generation++
	gen := e.snapshot.Generation
	e.snapshot.Process = "starting"
	e.snapshot.Session = "unknown"
	e.snapshot.SessionError = ""
	e.snapshot.SessionObservedAt = time.Time{}
	e.sessionFailed = false
	e.startupPortInUse = false
	adopted = true
	e.snapshot.PID = actualPID
	e.snapshot.StartedAt = time.Now()
	e.snapshot.ObservedAt = time.Time{}
	e.snapshot.AppliedRevision = 0
	e.snapshot.Operation = ""
	e.snapshot.Error = ""
	s.mu.Unlock()
	s.notify()
	var streams sync.WaitGroup
	streams.Add(2)
	go func() { defer streams.Done(); e.log.Read(stdout) }()
	go func() { defer streams.Done(); e.log.Read(stderr) }()
	go func() {
		streams.Wait()
		err := cmd.Wait()
		cancel()
		_ = write.Close()
		s.mu.Lock()
		e.cmd = nil
		e.control = nil
		e.snapshot.PID = 0
		e.snapshot.Process = "stopped"
		s.cancelObservationLocked(e)
		e.snapshot.Session = "unknown"
		e.snapshot.Proxies = nil
		retry := e.desired && !s.closed
		if err != nil && retry {
			e.snapshot.Process = "failed"
			e.snapshot.Error = "frpc 异常退出，请查看日志"
		}
		for _, name := range []string{"active.toml", "previous.toml", "candidate.toml"} {
			_ = os.Remove(filepath.Join(s.Dir, "runtime", p.ID, name))
		}
		_ = os.Remove(active)
		_ = os.Remove(s.ownerPath(p.ID))
		close(done)
		s.mu.Unlock()
		s.notify()
		if retry {
			s.restart(e, gen)
		}
	}()
	startupErr := waitReady(ctx, admin, done)
	if startupErr != nil {
		s.mu.Lock()
		conflict := e.startupPortInUse
		s.mu.Unlock()
		if stopErr := s.stop(e); stopErr != nil {
			return domain.Failure(domain.StartFailed, "初始化失败且未能回收此进程："+stopErr.Error(), "", false, stopErr)
		}
		if conflict {
			startupErr = adminPortConflict(startupErr)
		}
		s.set(e, func(r *domain.Runtime) { r.Process = "failed"; r.Operation = ""; r.Error = startupErr.Error() })
		return startupErr
	}
	s.mu.Lock()
	alive := e.cmd != nil
	conflict := e.startupPortInUse
	if alive {
		e.desired = true
		e.snapshot.Process = "running"
		e.snapshot.AppliedRevision = p.Revision
		s.beginObservationLocked(e, time.Now())
	}
	s.mu.Unlock()
	if !alive {
		if conflict {
			return adminPortConflict(nil)
		}
		return fmt.Errorf("frpc 在初始化期间退出")
	}
	if err = s.writeOwner(p, c, actualPID, active); err != nil {
		_ = s.stop(e)
		return fmt.Errorf("无法保存运行身份记录：%w", err)
	}
	if admin != nil {
		go s.poll(runCtx, e, admin, gen)
	}
	s.notify()
	return nil
}

// recordFailure is called under s.mu and retains only the rolling five-minute window.
func (e *entry) recordFailure(now time.Time) int {
	keep := e.failureTimes[:0]
	for _, at := range e.failureTimes {
		if now.Sub(at) < 5*time.Minute {
			keep = append(keep, at)
		}
	}
	e.failureTimes = append(keep, now)
	return len(e.failureTimes)
}
func (s *Supervisor) restart(e *entry, gen uint64) {
	s.mu.Lock()
	if s.closed || !e.desired || e.cmd != nil || e.snapshot.Generation != gen {
		s.mu.Unlock()
		return
	}
	attempt := e.recordFailure(time.Now())
	if attempt >= 5 {
		e.snapshot.Error = "5 分钟内反复退出，已停止自动重启；请修正配置后重试"
		s.mu.Unlock()
		s.notify()
		return
	}
	s.mu.Unlock()
	delay := time.Duration(1<<(attempt-1))*time.Second + time.Duration(time.Now().UnixNano()%250)*time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	e.op.Lock()
	defer e.op.Unlock()
	s.mu.Lock()
	ok := !s.closed && e.desired && e.cmd == nil && e.snapshot.Generation == gen
	p, c := e.profile, e.core
	s.mu.Unlock()
	if ok {
		_ = s.start(context.Background(), e, p, c, false)
	}
}
func (s *Supervisor) Stop(id string) error {
	e := s.get(id)
	e.op.Lock()
	defer e.op.Unlock()
	return s.stop(e)
}
func (s *Supervisor) stop(e *entry) error {
	s.mu.Lock()
	e.desired = false
	s.cancelObservationLocked(e)
	e.snapshot.Error = ""
	e.snapshot.SessionError = ""
	cmd, control, done := e.cmd, e.control, e.done
	if cmd == nil {
		e.snapshot.Process = "stopped"
		e.snapshot.Operation = ""
		s.mu.Unlock()
		s.notify()
		return nil
	}
	e.snapshot.Process = "stopping"
	e.snapshot.Operation = "停止连接"
	s.mu.Unlock()
	s.notify()
	if control != nil {
		_ = control.Close()
	}
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		_ = killGroup(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return fmt.Errorf("进程停止超时，请检查日志")
		}
	}
	return nil
}
func (s *Supervisor) Apply(ctx context.Context, p domain.Profile, c domain.Core) error {
	e := s.get(p.ID)
	e.op.Lock()
	defer e.op.Unlock()
	s.mu.Lock()
	running := e.cmd != nil
	old, oldCore, admin := e.profile, e.core, e.admin
	activePath := e.activePath
	s.mu.Unlock()
	if !running {
		return nil
	}
	if config.NeedsRestart(old, p) || admin == nil {
		if err := s.stop(e); err != nil {
			return err
		}
		if err := s.start(ctx, e, p, c, true); err != nil {
			restore := s.start(context.Background(), e, old, oldCore, true)
			return fmt.Errorf("应用失败：%v；恢复结果：%v", err, restore)
		}
		return nil
	}
	data, err := config.Compile(p, s.Secrets, &admin.Config, true)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Dir, "runtime", p.ID)
	candidate := filepath.Join(dir, "candidate.toml")
	defer os.Remove(candidate)
	if err = storage.AtomicWrite(candidate, data, 0600); err != nil {
		return err
	}
	if err = verifyFile(ctx, c.Path, candidate, config.SecretValues(data)); err != nil {
		return err
	}
	active := activePath
	previous, err := os.ReadFile(active)
	if err != nil {
		return err
	}
	if err = storage.AtomicWrite(filepath.Join(dir, "previous.toml"), previous, 0600); err != nil {
		return err
	}
	if err = storage.AtomicWrite(active, data, 0600); err != nil {
		return err
	}
	known := append(config.SecretValues(previous), config.SecretValues(data)...)
	e.log.SetSecrets(known)
	s.set(e, func(r *domain.Runtime) { r.Operation = "正在应用隧道配置" })
	if err = admin.Reload(ctx); err != nil {
		restore := storage.AtomicWrite(active, previous, 0600)
		if restore == nil {
			restore = admin.Reload(ctx)
		}
		s.set(e, func(r *domain.Runtime) {
			r.Operation = ""
			r.Error = fmt.Sprintf("应用失败：%v；恢复结果：%v", err, restore)
		})
		return err
	}
	s.mu.Lock()
	e.profile = p
	e.core = c
	e.snapshot.AppliedRevision = p.Revision
	s.beginObservationLocked(e, time.Now())
	e.snapshot.Operation = ""
	e.snapshot.ObservedAt = time.Time{}
	e.snapshot.Proxies = nil
	s.mu.Unlock()
	s.notify()
	return nil
}
func (s *Supervisor) Close() error {
	s.mu.Lock()
	s.closed = true
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Stop(id); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			s.get(id).log.Close()
		}()
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err != nil {
		s.mu.Lock()
		s.closed = false
		s.mu.Unlock()
	}
	return err
}
func SecretRefs(p domain.Profile) []string {
	out := []string{p.TokenRef, p.ProxyURLRef}
	for _, ref := range p.MetadataRefs {
		out = append(out, ref)
	}
	for _, t := range p.Tunnels {
		out = append(out, t.SecretRef, t.HTTPPasswordRef)
	}
	for _, v := range p.Visitors {
		out = append(out, v.SecretRef)
	}
	var clean []string
	for _, v := range out {
		if v != "" {
			clean = append(clean, v)
		}
	}
	return clean
}

var _ io.Writer = (*limitedOutput)(nil)

func (s *Supervisor) SecretReferences() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var refs []string
	for _, e := range s.entries {
		if e.cmd != nil {
			refs = append(refs, domain.SecretRefs(e.profile)...)
		}
	}
	return refs
}

func (s *Supervisor) AppliedProfile(id string) (domain.Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil || e.cmd == nil {
		return domain.Profile{}, false
	}
	return e.profile, true
}

func (s *Supervisor) AcknowledgeDisplay(p domain.Profile) {
	e := s.get(p.ID)
	e.op.Lock()
	defer e.op.Unlock()
	s.mu.Lock()
	if e.cmd != nil && config.RuntimeEquivalent(e.profile, p) {
		e.profile = p
		e.snapshot.AppliedRevision = p.Revision
	}
	s.mu.Unlock()
	s.notify()
}

func waitReady(ctx context.Context, admin *AdminClient, done <-chan struct{}) error {
	wait := 3 * time.Second
	if admin == nil {
		wait = 500 * time.Millisecond
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(75 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return fmt.Errorf("frpc 初始化失败，请查看日志（例如管理端口占用）")
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if admin == nil {
				return nil
			}
			return fmt.Errorf("frpc 管理 API 未在启动时限内就绪")
		case <-tick.C:
			if admin != nil {
				if _, err := admin.Status(ctx); err == nil {
					return nil
				}
			}
		}
	}
}
