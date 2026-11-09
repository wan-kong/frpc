package runtime

import (
	"context"
	"time"
)

// SetObservationScope reports product visibility, never process intent. Hiding
// all windows slows observations without stopping frpc or discarding its state.
func (s *Supervisor) SetObservationScope(activeProfile string, visible bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.observationActive == activeProfile && s.observationVisible == visible {
		return
	}
	s.observationActive, s.observationVisible = activeProfile, visible
	if s.observationChanged != nil {
		close(s.observationChanged)
	}
	s.observationChanged = make(chan struct{})
}
func (s *Supervisor) observationPolicy(id string) (time.Duration, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	interval := 5 * time.Second
	if !s.observationVisible {
		interval = 10 * time.Second
	} else if s.observationActive == id {
		interval = 2 * time.Second
	}
	return interval, s.observationChanged
}
func (s *Supervisor) poll(ctx context.Context, e *entry, admin *AdminClient, gen uint64) {
	// Startup gets a prompt first observation. Later
	// requests follow the selected/visible/background policy, without overlap.
	s.mu.Lock()
	id := e.snapshot.ProfileID
	s.mu.Unlock()
	timer := time.NewTimer(0)
	defer timer.Stop()
	first := true
	for {
		interval, changed := s.observationPolicy(id)
		if first {
			interval = 0
			first = false
		}
		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return
		case <-changed:
			continue
		case <-timer.C:
			if !s.observeOnce(ctx, e, admin, gen) {
				return
			}
		}
	}
}
func (s *Supervisor) observeOnce(ctx context.Context, e *entry, admin *AdminClient, gen uint64) bool {
	s.mu.Lock()
	if e.snapshot.Generation != gen || e.snapshot.Process != "running" {
		s.mu.Unlock()
		return false
	}
	revision := e.snapshot.AppliedRevision
	s.mu.Unlock()
	items, err := admin.Status(ctx)
	if ctx.Err() != nil {
		return false
	}
	s.mu.Lock()
	if e.snapshot.Generation != gen || e.snapshot.Process != "running" {
		s.mu.Unlock()
		return false
	}
	if e.snapshot.AppliedRevision != revision {
		s.mu.Unlock()
		return true
	}
	if err != nil {
		if !e.sessionFailed {
			e.snapshot.Session = "unknown"
		}
		e.snapshot.Error = "本机状态暂不可用：" + err.Error()
	} else {
		e.snapshot.ObservedAt = time.Now()
		e.snapshot.Error = ""
		if e.sessionFailed {
			e.snapshot.Proxies = nil
		} else {
			e.snapshot.Proxies = items
			if e.snapshot.Session != "logged_in" || time.Since(e.snapshot.SessionObservedAt) > 10*time.Second {
				e.snapshot.Session = "unknown"
			}
			for _, p := range items {
				if p.Status == "running" {
					e.snapshot.Session = "connected"
					e.snapshot.SessionObservedAt = time.Now()
					break
				}
			}
		}
	}
	s.mu.Unlock()
	s.notify()
	return true
}

// beginObservationLocked arms a one-shot notification so a quiet view can cross
// the deadline without waiting for another HTTP poll. s.mu must be held.
func (s *Supervisor) beginObservationLocked(e *entry, at time.Time) {
	s.cancelObservationLocked(e)
	e.snapshot.ObservationStartedAt = at
	epoch, gen := e.observationEpoch, e.snapshot.Generation
	e.observationTimer = time.AfterFunc(max(time.Duration(0), time.Until(at.Add(15*time.Second))), func() {
		s.mu.Lock()
		valid := e.observationEpoch == epoch && e.snapshot.Generation == gen && e.cmd != nil && e.snapshot.Process == "running"
		s.mu.Unlock()
		if valid {
			s.notify()
		}
	})
}
func (s *Supervisor) cancelObservationLocked(e *entry) {
	e.observationEpoch++
	if e.observationTimer != nil {
		e.observationTimer.Stop()
		e.observationTimer = nil
	}
}
