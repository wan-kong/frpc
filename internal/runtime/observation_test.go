package runtime

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"frp-desktop/internal/config"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func statusResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tcp":[{"name":"service","status":"running"}]}`))}
}
func TestObservationFrequencyFollowsScopeWithoutOverlappingLoops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New("", &storage.MemorySecrets{}, "")
		id := domain.NewID()
		e := s.get(id)
		e.snapshot.Generation = 1
		e.snapshot.Process = "running"
		calls := make(chan time.Time, 20)
		admin := NewAdmin(config.Admin{Addr: "127.0.0.1", Port: 12345})
		admin.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls <- time.Now(); return statusResponse(), nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s.SetObservationScope(id, true)
		go s.poll(ctx, e, admin, 1)
		count := func(want int) {
			t.Helper()
			synctest.Wait()
			if len(calls) != want {
				t.Fatalf("requests: got %d want %d", len(calls), want)
			}
		}
		count(1)
		time.Sleep(1999 * time.Millisecond)
		count(1)
		time.Sleep(time.Millisecond)
		count(2)
		s.SetObservationScope("another-profile", true)
		synctest.Wait()
		time.Sleep(4999 * time.Millisecond)
		count(2)
		time.Sleep(time.Millisecond)
		count(3)
		s.SetObservationScope("", false)
		synctest.Wait()
		time.Sleep(9999 * time.Millisecond)
		count(3)
		time.Sleep(time.Millisecond)
		count(4)
		s.SetObservationScope(id, true)
		synctest.Wait()
		time.Sleep(2 * time.Second)
		count(5)
		_, same := s.observationPolicy(id)
		s.SetObservationScope(id, true)
		select {
		case <-same:
			t.Fatal("identical scope reset a running timer")
		default:
		}
		cancel()
		synctest.Wait()
		time.Sleep(time.Minute)
		count(5)
	})
}
func TestObservationRejectsResponseForRetiredRevisionGenerationOrContext(t *testing.T) {
	for _, kind := range []string{"revision", "generation", "cancel", "stopping"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := New("", &storage.MemorySecrets{}, "")
				e := s.get(domain.NewID())
				e.snapshot.Generation = 1
				e.snapshot.Process = "running"
				e.snapshot.AppliedRevision = 1
				entered, resume := make(chan struct{}), make(chan struct{})
				admin := NewAdmin(config.Admin{Addr: "127.0.0.1", Port: 12345})
				admin.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { close(entered); <-resume; return statusResponse(), nil })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				go func() { s.observeOnce(ctx, e, admin, 1); close(done) }()
				<-entered
				s.mu.Lock()
				switch kind {
				case "revision":
					e.snapshot.AppliedRevision++
				case "generation":
					e.snapshot.Generation++
				case "cancel":
					cancel()
				case "stopping":
					e.snapshot.Process = "stopping"
				}
				s.mu.Unlock()
				close(resume)
				<-done
				if !e.snapshot.ObservedAt.IsZero() || len(e.snapshot.Proxies) != 0 {
					t.Fatal("stale request wrote its result")
				}
			})
		})
	}
}

func TestObservationDeadlineNotifiesQuietViewsAndRejectsOldWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := New("", &storage.MemorySecrets{}, "")
		e := s.get(domain.NewID())
		e.cmd = &exec.Cmd{}
		e.snapshot.Generation = 1
		e.snapshot.Process = "running"
		notices := make(chan struct{}, 4)
		s.OnChange = func() { notices <- struct{}{} }
		begin := func() { s.mu.Lock(); s.beginObservationLocked(e, time.Now()); s.mu.Unlock() }
		begin()
		time.Sleep(10 * time.Second)
		begin() // A newer applied revision supersedes the first deadline.
		time.Sleep(5*time.Second + 100*time.Millisecond)
		synctest.Wait()
		if len(notices) != 0 {
			t.Fatal("old observation notified the current view")
		}
		// Display-only revision changes do not extend or remove the observation deadline.
		s.mu.Lock()
		e.snapshot.AppliedRevision++
		s.mu.Unlock()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if len(notices) != 1 {
			t.Fatal("quiet view was not notified at its deadline", len(notices))
		}
		begin()
		s.mu.Lock()
		s.cancelObservationLocked(e)
		s.mu.Unlock()
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if len(notices) != 1 {
			t.Fatal("canceled observation notified")
		}
		begin()
		s.mu.Lock()
		e.snapshot.Generation++
		s.mu.Unlock()
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if len(notices) != 1 {
			t.Fatal("old generation notified")
		}
	})
}
