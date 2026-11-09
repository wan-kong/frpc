package ui

import (
	"fmt"
	"frp-desktop/internal/domain"
	"time"
)

func tunnelRow(p domain.Profile, t domain.Tunnel, rt domain.Runtime, now time.Time) row {
	r := row{Profile: p, Tunnel: t, Status: "已停止"}
	if !t.Enabled {
		r.Status = "停用"
	}
	if !rt.Running() {
		if rt.Process == "failed" || rt.Error != "" {
			r.Status = "连接失败"
			r.Err = rt.Error
		}
		return r
	}
	if rt.AppliedRevision != p.Revision {
		r.Status = "待应用"
		return r
	}
	if !t.Enabled {
		return r
	}
	if rt.Session == "authentication_failed" {
		r.Status = "认证失败"
		r.Err = rt.SessionError
		return r
	}
	if rt.Session == "reconnecting" {
		r.Status = "重连中"
		if rt.ObservationExpired(now) {
			r.Status = "确认超时"
			r.Err = "仍在重连，15 秒内未收到确认，会继续尝试"
		}
		return r
	}
	if rt.Operation != "" {
		r.Status = "应用中"
		return r
	}
	if rt.ObservedAt.IsZero() || now.Sub(rt.ObservedAt) > 10*time.Second {
		r.Status = "状态未确认"
		if !rt.ObservedAt.IsZero() {
			r.Status = "状态已过期"
		} else if rt.ObservationExpired(now) {
			r.Status = "确认超时"
			r.Err = "15 秒内未收到确认，会继续观察。可查看日志"
		}
		return r
	}
	r.Status = "注册中"
	for _, st := range rt.Proxies {
		if st.Name != t.Name && st.Name != p.User+"."+t.Name {
			continue
		}
		r.Err = st.Err
		switch st.Status {
		case "running":
			r.Status = "已注册"
		case "start error", "closed":
			r.Status = "注册失败"
		case "new", "wait start":
			r.Status = "注册中"
		default:
			r.Status = "状态未确认"
		}
		if st.Err != "" {
			r.Status = "注册失败"
		}
		if st.Err == "" && r.Status != "注册失败" && st.LocalAddr != "" && st.LocalAddr != domain.Endpoint(t.LocalIP, t.LocalPort) {
			r.Status = "应用中"
		}
		if st.RemoteAddr != "" {
			r.Tunnel.Domains = []string{st.RemoteAddr}
		}
		break
	}
	if rt.ObservationExpired(now) && (r.Status == "注册中" || r.Status == "状态未确认" || r.Status == "应用中") {
		r.Status = "确认超时"
		r.Err = "15 秒内未收到确认，会继续观察。可查看日志"
	}
	return r
}

// visitorRow mirrors tunnelRow for the visitor tab. A running visitor proxy
// means the local listener opened; whether the shared service answers is up
// to the side that shares it.
func visitorRow(p domain.Profile, v domain.Visitor, rt domain.Runtime, now time.Time) row {
	r := row{Profile: p, Visitor: v, IsVisitor: true, Status: "已停止"}
	if !v.Enabled {
		r.Status = "停用"
	}
	if !rt.Running() {
		if rt.Process == "failed" || rt.Error != "" {
			r.Status = "连接失败"
			r.Err = rt.Error
		}
		return r
	}
	if rt.AppliedRevision != p.Revision {
		r.Status = "待应用"
		return r
	}
	if !v.Enabled {
		return r
	}
	if rt.Session == "authentication_failed" {
		r.Status = "认证失败"
		r.Err = rt.SessionError
		return r
	}
	if rt.Session == "reconnecting" {
		r.Status = "重连中"
		if rt.ObservationExpired(now) {
			r.Status = "确认超时"
			r.Err = "仍在重连，15 秒内未收到确认，会继续尝试"
		}
		return r
	}
	if rt.Operation != "" {
		r.Status = "应用中"
		return r
	}
	if rt.ObservedAt.IsZero() || now.Sub(rt.ObservedAt) > 10*time.Second {
		r.Status = "状态未确认"
		if !rt.ObservedAt.IsZero() {
			r.Status = "状态已过期"
		} else if rt.ObservationExpired(now) {
			r.Status = "确认超时"
			r.Err = "15 秒内未收到确认，会继续观察。可查看日志"
		}
		return r
	}
	r.Status = "连接中"
	for _, st := range rt.Proxies {
		if st.Name != v.Name && st.Name != p.User+"."+v.Name {
			continue
		}
		r.Err = st.Err
		switch st.Status {
		case "running":
			r.Status = "已连接"
		case "start error", "closed":
			r.Status = "连接失败"
		case "new", "wait start":
			r.Status = "连接中"
		default:
			r.Status = "状态未确认"
		}
		if st.Err != "" {
			r.Status = "连接失败"
		}
		break
	}
	if rt.ObservationExpired(now) && (r.Status == "连接中" || r.Status == "状态未确认" || r.Status == "应用中") {
		r.Status = "确认超时"
		r.Err = "15 秒内未收到确认，会继续观察。可查看日志"
	}
	return r
}

// VisitorSummary mirrors RegistrationSummary for the visitor tab.
func VisitorSummary(p domain.Profile, rt domain.Runtime, now time.Time) string {
	if !rt.Running() || p.Mode == "raw" || len(p.Visitors) == 0 {
		return ""
	}
	if rt.AppliedRevision != p.Revision {
		return "有更改尚未应用。"
	}
	connected, failed, pending, timedOut := 0, 0, 0, 0
	for _, v := range p.Visitors {
		if !v.Enabled {
			continue
		}
		r := visitorRow(p, v, rt, now)
		switch r.Status {
		case "已连接":
			connected++
		case "连接失败", "认证失败":
			failed++
		case "确认超时":
			pending++
			timedOut++
		default:
			pending++
		}
	}
	if connected+failed+pending == 0 {
		return "所有访问者均已停用。"
	}
	if rt.ObservedAt.IsZero() || now.Sub(rt.ObservedAt) > 10*time.Second {
		if rt.ObservationExpired(now) {
			return "还没有收到服务器状态，会继续查询。可查看日志。"
		}
		return "正在刷新服务器状态"
	}
	if timedOut > 0 {
		return fmt.Sprintf("%d 个已连接，%d 个失败，%d 个待确认；仍在观察。", connected, failed, pending)
	}
	if failed > 0 && connected > 0 {
		return fmt.Sprintf("%d 个已连接，%d 个失败，%d 个待确认。可查看失败原因。", connected, failed, pending)
	}
	if failed > 0 {
		return fmt.Sprintf("%d 个连接失败，%d 个待确认。可查看失败原因和日志。", failed, pending)
	}
	if pending > 0 {
		return fmt.Sprintf("%d 个已连接，%d 个确认中。", connected, pending)
	}
	return fmt.Sprintf("%d 个访问者已连接。对方服务能否访问请实际测试。", connected)
}
