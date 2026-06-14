package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

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
		// OS-level window paint colour shown before the webview renders. This is
		// the RGB form of the --bg-void token (#0c0c0e); it cannot read the
		// theme JSON here because the vault/theme path isn't known until startup.
		BackgroundColour: &options.RGBA{R: 12, G: 12, B: 14, A: 1},
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
