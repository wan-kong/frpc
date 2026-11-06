package platform

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// TrayIcon is a small monochrome connection glyph, tinted by the system.
func TrayIcon() []byte {
	im := image.NewNRGBA(image.Rect(0, 0, 36, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 36; x++ {
			ring := func(cx, cy int) bool { d := (x-cx)*(x-cx) + (y-cy)*(y-cy); return d >= 25 && d <= 64 }
			bar := x >= 12 && x <= 23 && y >= 16 && y <= 19
			if ring(11, 18) || ring(25, 18) || bar {
				im.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}
