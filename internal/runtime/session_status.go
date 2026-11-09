package runtime

import (
	"frp-desktop/internal/logging"
	"regexp"
	"strings"
)

var sessionLogSource = regexp.MustCompile(`\[client/(?:service|control)\.go:\d+\]\s*(?:\[[a-z0-9_-]+\]\s*)?`)

func (s *Supervisor) observeLog(e *entry, line logging.Line, raw string) {
	s.noteStartupError(e, raw)
	msg := strings.ToLower(raw)
	if loc := sessionLogSource.FindStringIndex(msg); loc != nil {
		msg = msg[loc[1]:]
	} else {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !e.core.Certified || e.cmd == nil {
		return
	}
	switch {
	case strings.HasPrefix(msg, "connect to server error:"):
		e.sessionFailed = true
		e.snapshot.Session = "reconnecting"
		if strings.Contains(msg, "token") && strings.Contains(msg, "match") || strings.Contains(msg, "authorization") || strings.Contains(msg, "unauthorized") || strings.Contains(msg, "oidc") {
			e.snapshot.Session = "authentication_failed"
		}
		e.snapshot.SessionError = line.Text
		e.snapshot.SessionObservedAt = line.Time
		e.snapshot.ObservedAt = line.Time
		e.snapshot.Proxies = nil
	case strings.HasPrefix(msg, "login to server success, get run id"):
		if e.sessionFailed {
			s.beginObservationLocked(e, line.Time)
		}
		e.sessionFailed = false
		e.snapshot.Session = "logged_in"
		e.snapshot.SessionError = ""
		e.snapshot.SessionObservedAt = line.Time
	case strings.Contains(msg, "control is closed"), strings.HasPrefix(msg, "connection with server closed"):
		e.sessionFailed = true
		e.snapshot.Session = "reconnecting"
		e.snapshot.SessionError = "与 frps 的连接中断，正在等待内核重连"
		e.snapshot.Proxies = nil
	}
}
