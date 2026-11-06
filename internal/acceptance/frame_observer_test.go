package acceptance

import (
	"bytes"
	"testing"
	"time"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/logging"
	"frp-desktop/internal/storage"
	view "frp-desktop/internal/ui"
	u "github.com/egoist/mygo/ui"
)

// Observe construction from the first view pass through child scene recording.
// Multiple build passes before paint share one start. Replayed raster-only
// frames have no new layout/build sample. Nothing is painted by the observer.
type frameObserver struct {
	begin   time.Time
	pending bool
	samples []time.Duration
}

func (o *frameObserver) wrap(build func(*u.Context)) func(*u.Context) {
	return func(c *u.Context) {
		if !o.pending {
			o.begin, o.pending = time.Now(), true
		}
		// A trailing absolute node observes after the app's scene without
		// nesting it in a new command/focus scope or taking layout space.
		build(c)
		u.Box(c.Key("acceptance-frame-observer")).Absolute().Left(0).Top(0).Right(0).Bottom(0).IgnoreInput(true).DrawOver(func(_ *u.Painter, _ u.Rect) {
			if o.pending {
				o.samples = append(o.samples, time.Since(o.begin))
				o.pending = false
			}
		})
	}
}

func TestFrameObserverIncludesViewAndChildSceneWork(t *testing.T) {
	var observed frameObserver
	u.NewTester(observed.wrap(func(c *u.Context) {
		time.Sleep(2 * time.Millisecond)
		u.Box(c).Size(40, 40).Draw(func(_ *u.Painter, _ u.Rect) { time.Sleep(2 * time.Millisecond) })
	}), 100, 100)
	if len(observed.samples) == 0 {
		t.Fatal("observer missed construction")
	}
	for _, sample := range observed.samples {
		if sample < 4*time.Millisecond {
			t.Fatal("observer excluded view or child recording work", sample)
		}
	}
}

func measurementApp(t *testing.T) *view.App {
	t.Helper()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := app.New(store, &storage.MemorySecrets{}, "/not-used-by-measurement-test")
	t.Cleanup(func() {
		if err := controller.Runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	a := view.New(controller)
	p := domain.NewProfile()
	p.ServerAddr = "127.0.0.1"
	x := domain.NewTunnel()
	x.LocalPort = 3000
	p.Tunnels = []domain.Tunnel{x}
	a.State.Profiles = []domain.Profile{p}
	a.Selected = p.ID
	a.ShowLogs = true
	a.Logs = []logging.Line{{Seq: 1, Time: time.Unix(1600000000, 0), Level: "info", Text: "[info] 中文日志"}}
	return a
}

func TestFrameObserverPreservesGeometryAndPixels(t *testing.T) {
	a, b := measurementApp(t), measurementApp(t)
	base := u.NewTester(a.View, 1120, 760)
	var observed frameObserver
	probe := u.NewTester(observed.wrap(b.View), 1120, 760)
	base.Advance(250 * time.Millisecond)
	probe.Advance(250 * time.Millisecond)
	for _, label := range []string{"连接侧栏", "隧道工作区", "隧道检查器", "日志面板"} {
		x, ok := base.Find(label)
		y, ok2 := probe.Find(label)
		if !ok || !ok2 || x != y {
			t.Fatal("observer changed geometry", label, x, y)
		}
	}
	if !bytes.Equal(base.Image().Pix, probe.Image().Pix) {
		t.Fatal("observer changed rendered pixels")
	}
	if len(observed.samples) == 0 {
		t.Fatal("observer did not collect scene samples")
	}
	for _, tt := range []*u.Tester{base, probe} {
		tt.Key(u.Cmd, u.KeyF)
		tt.Type("probe search")
		if err := tt.Click("日志"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	if a.Search != "probe search" || b.Search != a.Search || a.ShowLogs || b.ShowLogs {
		t.Fatal("observer changed keyboard or pointer routing", a.Search, b.Search, a.ShowLogs, b.ShowLogs)
	}
}
