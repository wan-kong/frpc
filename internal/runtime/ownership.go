package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

type ownership struct {
	PID     int
	Started string
	Binary  string
	Config  string
	Token   string
}

func identity(ctx context.Context, pid int) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "stat=", "-o", "command=")
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 7 {
		return "", "", fmt.Errorf("无法确认进程身份")
	}
	if strings.Contains(fields[5], "Z") {
		return "", "", os.ErrProcessDone
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[6:], " "), nil
}
func (s *Supervisor) ownerPath(id string) string {
	return filepath.Join(s.Dir, "runtime", id, "owner.json")
}
func (s *Supervisor) writeOwner(p domain.Profile, c domain.Core, pid int, path string) error {
	started, _, err := identity(context.Background(), pid)
	if err != nil {
		return err
	}
	record := ownership{PID: pid, Started: started, Binary: c.Path, Config: path, Token: strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "active-"), ".toml")}
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return storage.AtomicWrite(s.ownerPath(p.ID), b, 0600)
}

// CheckResidual refuses duplicate startup. It never signals a process based on a saved PID.
func (s *Supervisor) CheckResidual(id string) error {
	r, err := s.InspectResidual(context.Background(), id)
	if err != nil {
		return err
	}
	switch r.Status {
	case "none", "managed":
		return nil
	case "exited":
		return s.clearResidual(r)
	default:
		return domain.Failure(domain.StartFailed, r.Reason+"；可在连接设置中检查残留进程", "", false, nil)
	}
}

type ResidualReview struct {
	ProfileID                                                    string
	PID                                                          int
	Started, Binary, Config, Token, RecordDigest, Status, Reason string
}

func (s *Supervisor) InspectResidual(ctx context.Context, id string) (ResidualReview, error) {
	r := ResidualReview{ProfileID: id, Status: "none", Reason: "没有发现此连接的残留运行记录"}
	if !domain.ValidID(id) {
		return r, domain.Failure(domain.Validation, "无效连接标识", "profileId", false, nil)
	}
	s.mu.Lock()
	e := s.entries[id]
	if e != nil && e.cmd != nil {
		r.PID = e.snapshot.PID
		r.Binary = e.core.Path
		r.Config = e.activePath
		r.Status = "managed"
		r.Reason = "此进程由当前应用管理，请使用正常停止操作"
	}
	s.mu.Unlock()
	if r.Status == "managed" {
		return r, nil
	}
	f, err := os.Open(s.ownerPath(id))
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return r, err
	}
	sum := sha256.Sum256(b)
	r.RecordDigest = hex.EncodeToString(sum[:])
	r.Status = "unverified"
	r.Reason = "运行记录不完整，未确认进程身份，不会发送终止信号"
	var record ownership
	if len(b) > 4096 || json.Unmarshal(b, &record) != nil || record.PID <= 0 || !domain.ValidID(record.Token) {
		return r, nil
	}
	r.PID, r.Started, r.Binary, r.Config, r.Token = record.PID, record.Started, record.Binary, record.Config, record.Token
	if record.Config != filepath.Join(s.Dir, "runtime", id, "active-"+record.Token+".toml") {
		r.Reason = "运行配置路径与所有权标识不一致"
		return r, nil
	}
	root := filepath.Join(s.Dir, "cores")
	rel, err := filepath.Rel(root, record.Binary)
	if err != nil || !filepath.IsAbs(record.Binary) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		r.Reason = "可执行文件不在此应用的受管内核目录中"
		return r, nil
	}
	if err = syscall.Kill(record.PID, 0); err == syscall.ESRCH {
		r.Status = "exited"
		r.Reason = "原进程已经退出，可以清理运行记录"
		return r, nil
	} else if err != nil {
		r.Reason = "无法查询记录中的进程，未确认其身份"
		return r, nil
	}
	started, command, err := identity(ctx, record.PID)
	if err == os.ErrProcessDone {
		r.Status = "exited"
		r.Reason = "原进程已结束，等待系统回收"
		return r, nil
	}
	if err != nil {
		// A reaper may remove the process between kill(0) and ps. Confirm that
		// fact before reporting an unknown identity; never infer exit from ps alone.
		if syscall.Kill(record.PID, 0) == syscall.ESRCH {
			r.Status = "exited"
			r.Reason = "原进程已经退出，可以清理运行记录"
			return r, nil
		}
		r.Reason = "无法读取进程身份"
		return r, nil
	}
	expected := strings.Join(strings.Fields(record.Binary+" -c "+record.Config), " ")
	if started != record.Started || command != expected {
		r.Reason = "PID、启动时间或命令已不匹配；不会终止该进程"
		return r, nil
	}
	r.Status = "verified"
	r.Reason = "已确认进程启动时间、受管内核路径和独立运行配置"
	return r, nil
}
func (s *Supervisor) clearResidual(r ResidualReview) error {
	for _, path := range []string{r.Config, s.ownerPath(r.ProfileID), filepath.Join(s.Dir, "runtime", r.ProfileID, "previous.toml"), filepath.Join(s.Dir, "runtime", r.ProfileID, "candidate.toml")} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ResolveResidual requires the exact record reviewed by the user. It never kills a process group.
func (s *Supervisor) ResolveResidual(ctx context.Context, review ResidualReview, force bool) error {
	if !domain.ValidID(review.ProfileID) {
		return fmt.Errorf("无效连接")
	}
	e := s.get(review.ProfileID)
	e.op.Lock()
	defer e.op.Unlock()
	current, err := s.InspectResidual(ctx, review.ProfileID)
	if err != nil {
		return err
	}
	if current.RecordDigest == "" || current.RecordDigest != review.RecordDigest {
		return domain.Failure(domain.Conflict, "运行记录已改变，请重新检查后操作", "", false, nil)
	}
	if current.Status == "exited" {
		return s.clearResidual(current)
	}
	if current.Status != "verified" {
		return domain.Failure(domain.PermissionDenied, "进程身份未确认，未发送任何信号", "", false, nil)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err = syscall.Kill(current.PID, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return domain.Failure(domain.StartFailed, "进程尚未退出；可重新检查身份后选择强制终止", "", true, nil)
		case <-tick.C:
			now, err := s.InspectResidual(ctx, review.ProfileID)
			if err != nil {
				return err
			}
			if now.RecordDigest != review.RecordDigest {
				return domain.Failure(domain.Conflict, "运行记录已改变，已停止后续处理", "", false, nil)
			}
			if now.Status == "exited" {
				return s.clearResidual(now)
			}
			if now.Status != "verified" {
				return domain.Failure(domain.Conflict, "进程身份发生变化，未发送其他信号", "", false, nil)
			}
		}
	}
}

// WithUnusedCore guards deletion against both automatic restarts and detached processes.
// Every real start takes coreMu.RLock; the callback runs while new starts are excluded.
func (s *Supervisor) WithUnusedCore(binary string, remove func() error) error {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	s.mu.Lock()
	for _, e := range s.entries {
		if e.core.Path == binary && (e.cmd != nil || e.desired) {
			s.mu.Unlock()
			return domain.Failure(domain.Conflict, "内核仍被运行实例或待重启连接引用", "coreId", false, nil)
		}
	}
	s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.Dir, "runtime"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !domain.ValidID(entry.Name()) {
			continue
		}
		review, err := s.InspectResidual(context.Background(), entry.Name())
		if err != nil {
			return err
		}
		if review.Status == "unverified" || review.Binary == binary && (review.Status == "verified" || review.Status == "managed") {
			return domain.Failure(domain.Conflict, "存在仍在运行或身份未确认的运行记录，请先检查残留进程", "coreId", false, nil)
		}
	}
	return remove()
}
