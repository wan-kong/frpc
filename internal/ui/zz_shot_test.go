package ui

import (
	"image/png"
	"os"
	"testing"

	u "github.com/egoist/mygo/ui"
)

func TestShotSidebar(t *testing.T) {
	a := newTestApp(t)
	tt := u.NewTester(a.View, 1120, 760)
	f, _ := os.Create("/tmp/sidebar_empty.png")
	_ = png.Encode(f, tt.Image())
	f.Close()
}
