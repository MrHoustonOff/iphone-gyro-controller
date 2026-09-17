package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/src
var assets embed.FS

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--livedebug" {
			runLiveDebug()
			return
		}
	}

	app := NewApp()
	debugApp := NewLiveDebugApp()

	err := wails.Run(&options.App{
		Title:     "GyroBridge",
		Width:     1020,
		Height:    860,
		MinWidth:  680,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "gyrobridge-desktop-lock-uuid",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				if app.ctx != nil {
					wailsRuntime.WindowUnminimise(app.ctx)
					wailsRuntime.WindowShow(app.ctx)
					wailsRuntime.WindowSetAlwaysOnTop(app.ctx, true)
					wailsRuntime.WindowSetAlwaysOnTop(app.ctx, false)
				}
			},
		},
		Bind: []interface{}{
			app,
			debugApp,
		},
		EnableDefaultContextMenu: true,
		Debug: options.Debug{
			OpenInspectorOnStartup: false,
		},
		Windows: &windows.Options{
			WebviewUserDataPath:  filepath.Join(os.Getenv("APPDATA"), "GyroBridge", "WebView2_Main"),
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
