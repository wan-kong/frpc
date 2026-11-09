package runtime

import (
	"context"
	"net"
	"strings"

	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
)

const maxAdminPortRetries = 3

func freeAdminPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}
func checkAdminPort(admin *AdminClient) error {
	if admin == nil {
		return nil
	}
	listener, err := net.Listen("tcp4", domain.Endpoint(admin.Config.Addr, admin.Config.Port))
	if err == nil {
		return listener.Close()
	}
	if platform.IsAddrInUse(err) {
		return adminPortConflict(err)
	}
	return domain.Failure(domain.AdminUnavailable, "无法检查本机管理端口，请检查监听权限", "webServer.port", true, err)
}
func adminPortConflict(cause error) error {
	return domain.Failure(domain.PortInUse, "本机管理端口被其他进程占用", "webServer.port", true, cause)
}

func (s *Supervisor) start(ctx context.Context, e *entry, p domain.Profile, c domain.Core, reset bool) error {
	s.coreMu.RLock()
	defer s.coreMu.RUnlock()
	for retry := 0; ; retry++ {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return domain.Failure(domain.StartFailed, "应用正在退出，未启动新的内核实例", "", false, nil)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.startAttempt(ctx, e, p, c, reset)
		if err == nil {
			return nil
		}
		info, typed := domain.ErrorInfo(err)
		if p.Mode != "managed" || !c.Certified || !typed || info.Code != domain.PortInUse {
			return err
		}
		if retry >= maxAdminPortRetries {
			return domain.Failure(domain.PortInUse, "本机管理端口冲突，重新选择并验证 3 次后仍未成功；请稍后重试", "webServer.port", true, err)
		}
	}
}

// The exact message is from the certified v0.72.0 Go listener failure. Only the
// current managed startup's generated loopback endpoint may trigger a retry.
func isAdminBindConflict(raw string, admin config.Admin) bool {
	raw = strings.TrimSpace(raw)
	endpoint := domain.Endpoint(admin.Addr, admin.Port)
	return raw == "listen tcp "+endpoint+": bind: address already in use" ||
		raw == "listen tcp4 "+endpoint+": bind: address already in use" ||
		strings.HasPrefix(raw, "listen tcp "+endpoint+": bind: ") && strings.Contains(strings.ToLower(raw), "only one usage of each socket address") ||
		strings.HasPrefix(raw, "listen tcp4 "+endpoint+": bind: ") && strings.Contains(strings.ToLower(raw), "only one usage of each socket address")
}
func (s *Supervisor) noteStartupError(e *entry, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.cmd != nil && e.core.Certified && e.profile.Mode == "managed" && e.snapshot.Process == "starting" && e.admin != nil && isAdminBindConflict(raw, e.admin.Config) {
		e.startupPortInUse = true
	}
}
