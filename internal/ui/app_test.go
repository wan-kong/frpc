package ui

import (
	"fmt"
	"strings"
	"testing"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
	"frp-desktop/internal/storage"

	u "github.com/egoist/mygo/ui"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(app.New(store, &storage.MemorySecrets{}, "/not-used-in-view-tests"))
}

func TestLargeTableBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	p := domain.NewProfile()
	p.ServerAddr = "example.net"
	for i := 0; i < 5000; i++ {
		x := domain.NewTunnel()
		x.Name = fmt.Sprintf("service-%04d", i)
		x.LocalPort = 3000
		p.Tunnels = append(p.Tunnels, x)
	}
	a.State.Profiles = []domain.Profile{p}
	a.Selected = p.ID
	tt := u.NewTester(a.View, 1120, 760)
	visible := 0
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "service-") {
			visible++
		}
	}
	if visible < 5 || visible > 100 {
		t.Fatalf("virtual table built %d rows", visible)
	}
	a.Search = "service-4999"
	tt.Frame()
	if !tt.HasText("service-4999") {
		t.Fatal("search did not invalidate row cache")
	}
}

func TestScopeChangesDoNotKeepHiddenSelection(t *testing.T) {
	a := newTestApp(t)
	a.Selection.Add("previous-profile-row")
	a.SelectedRow = 5
	a.SelectScope("all")
	if a.Selection.Len() != 0 || a.SelectedRow != -1 {
		t.Fatal("selection escaped its previous scope")
	}
}
func TestEmptyStateAndConnectionEditor(t *testing.T) {
	a := newTestApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	if !tt.HasText("先添加一个连接") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("添加连接"); err != nil {
		t.Fatal(err)
	}
	if !a.ProfileOpen || !tt.HasText("新建连接") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.ProfileOpen {
		t.Fatal("modal did not close")
	}
}
func TestTunnelAndVisitorFormsAcrossSizes(t *testing.T) {
	for _, width := range []int{900, 1120, 1440} {
		a := newTestApp(t)
		p := domain.NewProfile()
		p.ServerAddr = "example.net"
		p.Name = "中文开发网络与较长名称"
		a.State.Profiles = []domain.Profile{p}
		a.Selected = p.ID
		tt := u.NewTester(a.View, width, 760)
		if err := tt.Click("添加隧道"); err != nil {
			t.Fatal(err)
		}
		if !a.TunnelOpen || !tt.HasText("新建隧道") {
			t.Fatal(tt.Texts())
		}
		if err := tt.Click("取消"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("访问者"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("添加访问者"); err != nil {
			t.Fatal(err)
		}
		if !a.EditingVisitor || !tt.HasText("新建访问者") {
			t.Fatal(tt.Texts())
		}
	}
}
