package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// LiveDebugApp manages the standalone Live Debug desktop window.
type LiveDebugApp struct {
	ctx context.Context
}

// NewLiveDebugApp creates a new LiveDebugApp instance.
func NewLiveDebugApp() *LiveDebugApp {
	return &LiveDebugApp{}
}

func (a *LiveDebugApp) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *LiveDebugApp) shutdown(ctx context.Context) {
}

// ResetAHRS triggers AHRS calibration recenter via the running core server.
func (a *LiveDebugApp) ResetAHRS() {
	go func() {
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/livedebug/recenter", HTTPPort), "application/json", nil)
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	}()
}

// runLiveDebug initializes and runs the dedicated Live Debug window instance.
func runLiveDebug() {
	debugApp := NewLiveDebugApp()

	subFS, _ := fs.Sub(assets, "frontend/src")

	err := wails.Run(&options.App{
		Title:            "GyroBridge - Live Debug",
		Width:            1180,
		Height:           760,
		MinWidth:         860,
		MinHeight:        560,
		StartHidden:      true,
		BackgroundColour: &options.RGBA{R: 6, G: 8, B: 13, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					p := strings.TrimPrefix(r.URL.Path, "/")
					if p == "" || p == "index.html" || p == "index.htm" || p == "livedebug.html" {
						if subFS != nil {
							data, err := fs.ReadFile(subFS, "livedebug.html")
							if err == nil {
								w.Header().Set("Content-Type", "text/html; charset=utf-8")
								w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
								w.WriteHeader(http.StatusOK)
								w.Write(data)
								return
							}
						}
					}
					next.ServeHTTP(w, r)
				})
			},
		},
		OnStartup: debugApp.startup,
		OnDomReady: func(ctx context.Context) {
			wailsRuntime.WindowCenter(ctx)
			wailsRuntime.WindowShow(ctx)
			wailsRuntime.WindowUnminimise(ctx)
		},
		OnShutdown: debugApp.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "gyrobridge-livedebug-window-lock-uuid",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				if debugApp.ctx != nil {
					wailsRuntime.WindowUnminimise(debugApp.ctx)
					wailsRuntime.Show(debugApp.ctx)
				}
			},
		},
		Bind: []interface{}{
			debugApp,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("LiveDebug window error:", err.Error())
	}
}
