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
			r.Status = "观察超时"
			r.Err = "连接仍在重连，15 秒内尚未确认注册结果；将继续观察"
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
			r.Status = "观察超时"
			r.Err = "15 秒内尚未确认注册结果，将继续观察；请查看日志"
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
		r.Status = "观察超时"
		r.Err = "15 秒内尚未确认注册结果，将继续观察；请查看日志"
	}
	return r
}

// RegistrationSummary reports observations, never end-to-end reachability.
func RegistrationSummary(p domain.Profile, rt domain.Runtime, now time.Time) string {
	if !rt.Running() || p.Mode == "raw" {
		return ""
	}
	if rt.AppliedRevision != p.Revision {
		return "已保存的更改尚未应用；列表展示当前期望配置。"
	}
	if len(p.Tunnels) == 0 && len(p.Visitors) == 0 {
		return "当前没有隧道，进程运行不代表业务已可用。"
	}
	if len(p.Tunnels) == 0 {
		return "访问者已交由内核运行；当前版本没有统一的访问者状态接口。"
	}
	registered, failed, pending, timedOut := 0, 0, 0, 0
	for _, tunnel := range p.Tunnels {
		if !tunnel.Enabled {
			continue
		}
		r := tunnelRow(p, tunnel, rt, now)
		switch r.Status {
		case "已注册":
			registered++
		case "注册失败", "认证失败":
			failed++
		case "观察超时":
			pending++
			timedOut++
		default:
			pending++
		}
	}
	if registered+failed+pending == 0 {
		return "所有隧道均已停用。"
	}
	if rt.ObservedAt.IsZero() || now.Sub(rt.ObservedAt) > 10*time.Second {
		if !rt.ObservedAt.IsZero() {
			return "内核状态已过期，正在继续查询；旧的注册结果不作为成功依据。"
		}
		if rt.ObservationExpired(now) {
			return "观察超时：15 秒内尚未取得有效内核状态，将继续查询；请查看日志。"
		}
		return "正在等待内核状态；旧的注册结果不作为成功依据。"
	}
	if timedOut > 0 {
		return fmt.Sprintf("观察超时：%d 条已注册，%d 条失败，%d 条待确认（其中 %d 条超过 15 秒）；继续观察，不撤销已成功条目。", registered, failed, pending, timedOut)
	}
	if failed > 0 && registered > 0 {
		return fmt.Sprintf("部分注册成功：%d 条已注册，%d 条失败，%d 条待确认；可查看失败条目和日志。", registered, failed, pending)
	}
	if failed > 0 {
		return fmt.Sprintf("%d 条注册失败，%d 条待确认；请查看失败原因和日志。", failed, pending)
	}
	if pending > 0 {
		return fmt.Sprintf("观察中：%d 条已注册，%d 条待确认；业务可达性需另行检测。", registered, pending)
	}
	return fmt.Sprintf("%d 条隧道已注册；尚未验证外网业务可达性。", registered)
}
