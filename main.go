package main

import (
	"embed"

	"silt/backend/themes"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// launchBackgroundColour resolves the OS-level window paint colour shown
// before the webview renders. It reads the stored ThemeMode, resolves the
// effective mode's bg.void from the EMBEDDED default theme (always
// available — no disk/vault needed), and converts it to RGBA. This removes
// the pre-CSS flash and tracks the active theme. A user's custom theme with
// a different bg.void shows the default void for only the few ms until the
// runtime injector (frontend/src/theme) applies the real theme over IPC.
func launchBackgroundColour() *options.RGBA {
	fallback := func() *options.RGBA { return &options.RGBA{R: 12, G: 12, B: 14, A: 1} } // #0c0c0e
	mode := "dark"
	if settings, err := vault.LoadSettings(); err == nil {
		mode = effectiveMode(settings.ThemeMode)
	}
	th, err := themes.ParseDefault()
	if err != nil {
		return fallback()
	}
	r, g, b, ok := themes.HexToRGB(th.BGVoid(mode))
	if !ok {
		return fallback()
	}
	return &options.RGBA{R: r, G: g, B: b, A: 1}
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "Silt",
		Width:            1024,
		Height:           768,
		WindowStartState: options.Maximised,
		Frameless:        true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// OS-level window paint colour shown before the webview renders,
		// resolved from the active theme mode's bg.void so there is no
		// pre-CSS flash that matches no token.
		BackgroundColour: launchBackgroundColour(),
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

