package main

import (
	"embed"
	"path/filepath"

	"silt/backend/themes"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// launchBackgroundColour resolves the OS-level window paint colour shown
// before the webview renders. It reads the stored ThemeMode + active theme
// id, resolves the effective mode's bg.void from the in-process theme
// cache (or the embedded default as the final fallback), and converts it
// to RGBA. This removes the pre-CSS flash and tracks the active theme
// even when it is not the embedded default (#73). A custom theme with a
// different bg.void used to show the default void for a few ms until the
// runtime injector caught up; the cache lookup short-circuits that gap
// because it serves the on-disk theme from a single read at startup.
func launchBackgroundColour() *options.RGBA {
	fallback := func() *options.RGBA { return &options.RGBA{R: 12, G: 12, B: 14, A: 1} } // #0c0c0e
	settings, err := vault.LoadSettings()
	if err != nil {
		// No settings → no active id → embedded default bg.void (always
		// available from the binary).
		if th, perr := themes.ParseDefault(); perr == nil {
			mode := effectiveMode("")
			r, g, b, ok := themes.HexToRGB(th.BGVoid(mode))
			if ok {
				return &options.RGBA{R: r, G: g, B: b, A: 1}
			}
		}
		return fallback()
	}
	mode := effectiveMode(settings.ThemeMode)
	themesDir := ""
	if settings.VaultPath != "" {
		themesDir = filepath.Join(settings.VaultPath, ".system", "themes")
	}
	th, err := themes.CachedThemeByID(themesDir, settings.ActiveTheme)
	if err != nil {
		if th, perr := themes.ParseDefault(); perr == nil {
			r, g, b, ok := themes.HexToRGB(th.BGVoid(mode))
			if ok {
				return &options.RGBA{R: r, G: g, B: b, A: 1}
			}
		}
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

