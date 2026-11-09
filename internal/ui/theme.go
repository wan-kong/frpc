package ui

import (
	"sync"

	u "github.com/egoist/mygo/ui"
)

// The app keeps one restrained palette: neutral grays and a blue
// accent. Status colors stay semantic so a failure still reads as a failure.
var (
	themeOnce       sync.Once
	themeLight      *u.Theme
	themeDark       *u.Theme
	tableThemeLight *u.Theme
	tableThemeDark  *u.Theme
)

var blueLightPalette = u.Theme{
	Background:     u.Hex("#fafafa"),
	Surface:        u.Hex("#f2f2f2"),
	SurfaceHover:   u.Hex("#e8e8e8"),
	SurfacePressed: u.Hex("#dedede"),
	Border:         u.Hex("#d4d4d4"),
	Text:           u.Hex("#202020"),
	TextMuted:      u.Hex("#656565"),
	Accent:         u.Hex("#2563eb"),
	AccentHover:    u.Hex("#1d4ed8"),
	AccentPressed:  u.Hex("#1e40af"),
	AccentText:     u.Hex("#ffffff"),
	Danger:         u.Hex("#b33a3a"),
	Warning:        u.Hex("#946a15"),
	Success:        u.Hex("#3a7a4a"),
	Selection:      u.RGBA(37, 99, 235, 0.18),
	Focus:          u.RGBA(37, 99, 235, 0.55),
	Scrollbar:      u.RGBA(32, 32, 32, 0.28),
	Radius:         6,
	Spacing:        4,
}

var blueDarkPalette = u.Theme{
	Dark:           true,
	Background:     u.Hex("#191919"),
	Surface:        u.Hex("#242424"),
	SurfaceHover:   u.Hex("#2e2e2e"),
	SurfacePressed: u.Hex("#383838"),
	Border:         u.Hex("#424242"),
	Text:           u.Hex("#f0f0f0"),
	TextMuted:      u.Hex("#ababab"),
	Accent:         u.Hex("#60a5fa"),
	AccentHover:    u.Hex("#93c5fd"),
	AccentPressed:  u.Hex("#3b82f6"),
	AccentText:     u.Hex("#ffffff"),
	Danger:         u.Hex("#e88383"),
	Warning:        u.Hex("#d9ac55"),
	Success:        u.Hex("#7fc08d"),
	Selection:      u.RGBA(96, 165, 250, 0.30),
	Focus:          u.RGBA(96, 165, 250, 0.60),
	Scrollbar:      u.RGBA(240, 240, 240, 0.30),
	Radius:         6,
	Spacing:        4,
}

// appTheme returns a stable theme pointer so a frame that reapplies it does
// not repaint on every build.
func appTheme(dark bool) *u.Theme {
	themeOnce.Do(func() {
		light, darkTheme := u.LightTheme(), u.DarkTheme()
		lightPalette := blueLightPalette
		lightPalette.FontSize, lightPalette.Font = light.FontSize, light.Font
		themeLight = &lightPalette
		darkPalette := blueDarkPalette
		darkPalette.FontSize, darkPalette.Font = darkTheme.FontSize, darkTheme.Font
		themeDark = &darkPalette

		// Table rows use Accent as their selection fill.
		lightTable := lightPalette
		lightTable.Accent = u.Hex("#dbeafe")
		lightTable.AccentText = lightPalette.Text
		tableThemeLight = &lightTable
		darkTable := darkPalette
		darkTable.Accent = u.Hex("#243750")
		darkTable.AccentText = darkPalette.Text
		tableThemeDark = &darkTable
	})
	if dark {
		return themeDark
	}
	return themeLight
}

func tableTheme(dark bool) *u.Theme {
	appTheme(dark)
	if dark {
		return tableThemeDark
	}
	return tableThemeLight
}
