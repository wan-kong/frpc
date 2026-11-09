package ui

import (
	"frp-desktop/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestUnappliedDesiredStateNeverLooksApplied(t *testing.T) {
	now := time.Now()
	p := domain.NewProfile()
	p.Revision = 2
	x := domain.NewTunnel()
	x.Name = "ssh"
	rt := domain.Runtime{Process: "running", AppliedRevision: 1, ObservedAt: now, Proxies: []domain.ProxyStatus{{Name: "ssh", Status: "running"}}}
	for _, enabled := range []bool{true, false} {
		x.Enabled = enabled
		if r := tunnelRow(p, x, rt, now); r.Status != "待应用" {
			t.Fatal(r)
		}
	}
	rt.AppliedRevision = 2
	x.Enabled = true
	if r := tunnelRow(p, x, rt, now); r.Status != "已注册" {
		t.Fatal(r)
	}
	rt.ObservedAt = now.Add(-11 * time.Second)
	if r := tunnelRow(p, x, rt, now); r.Status != "状态已过期" {
		t.Fatal(r)
	}
}

func TestRegistrationSummaryDoesNotHidePartialOrPendingResults(t *testing.T) {
	p := domain.NewProfile()
	p.Revision = 1
	a, b := domain.NewTunnel(), domain.NewTunnel()
	a.Name = "ok"
	b.Name = "failed"
	p.Tunnels = []domain.Tunnel{a, b}
	now := time.Now()
	rt := domain.Runtime{Process: "running", AppliedRevision: 1, ObservedAt: now, Proxies: []domain.ProxyStatus{{Name: "ok", Status: "running"}, {Name: "failed", Status: "start error", Err: "occupied"}}}
	if text := RegistrationSummary(p, rt, now); !strings.Contains(text, "条失败") {
		t.Fatal(text)
	}
	rt.Proxies = rt.Proxies[:1]
	if text := RegistrationSummary(p, rt, now); !strings.Contains(text, "条确认中") {
		t.Fatal(text)
	}
	rt.ObservedAt = now.Add(-11 * time.Second)
	if text := RegistrationSummary(p, rt, now); !strings.Contains(text, "正在刷新服务器状态") {
		t.Fatal(text)
	}
	p.Revision++
	if text := RegistrationSummary(p, rt, now); !strings.Contains(text, "尚未应用") {
		t.Fatal(text)
	}
}

func TestObservationDeadlineDoesNotRevokeSuccessOrConflateStaleEvidence(t *testing.T) {
	start := time.Now()
	p := domain.NewProfile()
	p.Revision = 1
	good, pending := domain.NewTunnel(), domain.NewTunnel()
	good.Name = "good"
	pending.Name = "pending"
	p.Tunnels = []domain.Tunnel{good, pending}
	rt := domain.Runtime{Process: "running", AppliedRevision: 1, ObservationStartedAt: start, ObservedAt: start.Add(14 * time.Second), Proxies: []domain.ProxyStatus{{Name: "good", Status: "running"}, {Name: "pending", Status: "wait start"}}}
	before := start.Add(15*time.Second - time.Nanosecond)
	deadline := start.Add(15 * time.Second)
	if r := tunnelRow(p, pending, rt, before); r.Status != "注册中" {
		t.Fatal("early timeout", r)
	}
	if r := tunnelRow(p, pending, rt, deadline); r.Status != "确认超时" || r.Err == "" {
		t.Fatal("pending entry did not expire", r)
	}
	if r := tunnelRow(p, good, rt, deadline); r.Status != "已注册" {
		t.Fatal("timeout revoked a successful entry", r)
	}
	if text := RegistrationSummary(p, rt, deadline); !strings.Contains(text, "1 条已注册") || !strings.Contains(text, "待确认") {
		t.Fatal(text)
	}
	rt.Proxies[1].Status = "running"
	if r := tunnelRow(p, pending, rt, deadline); r.Status != "已注册" {
		t.Fatal("late success ignored", r)
	}
	rt.ObservedAt = start
	if r := tunnelRow(p, good, rt, deadline); r.Status != "状态已过期" {
		t.Fatal("old success was treated as current", r)
	}
	rt.ObservedAt = time.Time{}
	rt.Proxies = nil
	if text := RegistrationSummary(p, rt, deadline); !strings.Contains(text, "会继续查询") {
		t.Fatal(text)
	}
}

func TestObservationTimeoutDoesNotReplaceExplicitRegistrationError(t *testing.T) {
	p := domain.NewProfile()
	p.Revision = 1
	x := domain.NewTunnel()
	x.Name = "failed"
	x.LocalPort = 8080
	p.Tunnels = []domain.Tunnel{x}
	now := time.Now()
	rt := domain.Runtime{Process: "running", AppliedRevision: 1, ObservedAt: now, ObservationStartedAt: now.Add(-20 * time.Second), Proxies: []domain.ProxyStatus{{Name: x.Name, Status: "start error", Err: "remote port occupied", LocalAddr: "127.0.0.1:9000"}}}
	r := tunnelRow(p, x, rt, now)
	if r.Status != "注册失败" || r.Err != "remote port occupied" {
		t.Fatal("explicit failure replaced by pending timeout", r)
	}
}
