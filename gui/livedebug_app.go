package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
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

	// Window hierarchy watchdog: if main core server shuts down, close debug window too
	go func() {
		client := &http.Client{Timeout: 800 * time.Millisecond}
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		consecutiveFails := 0
		for range ticker.C {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/livedebug/ping", HTTPPort))
			if err != nil {
				consecutiveFails++
				if consecutiveFails >= 2 {
					if a.ctx != nil {
						wailsRuntime.Quit(a.ctx)
					}
					return
				}
			} else {
				consecutiveFails = 0
				resp.Body.Close()
			}
		}
	}()

	// Native Go telemetry bridge: dials core server WS directly bypassing WebView2 loopback limits
	go func() {
		wsURL := fmt.Sprintf("ws://127.0.0.1:%d/livedebug/ws", HTTPPort)
		for {
			if a.ctx != nil && a.ctx.Err() != nil {
				return
			}
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					conn.Close()
					break
				}
				if a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "livedebug:telemetry", string(msg))
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
	}()
}

// GetDeviceStatus checks device connection status directly from core server via Go HTTP.
func (a *LiveDebugApp) GetDeviceStatus() bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/livedebug/status", HTTPPort))
	if err != nil || resp == nil {
		return false
	}
	defer resp.Body.Close()
	var st struct {
		DeviceConnected bool `json:"device_connected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return false
	}
	return st.DeviceConnected
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

// SetWindowTheme sets the native window title bar theme for the debug window.
func (a *LiveDebugApp) SetWindowTheme(theme string) {
	if a.ctx == nil {
		return
	}
	if theme == "dark" {
		wailsRuntime.WindowSetDarkTheme(a.ctx)
	} else if theme == "light" {
		wailsRuntime.WindowSetLightTheme(a.ctx)
	}
}

// SaveCSVFile opens a native save dialog and saves the CSV content to the selected file path.
func (a *LiveDebugApp) SaveCSVFile(defaultName string, content string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("context not initialized")
	}
	savePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "Сохранить запись телеметрии CSV",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "CSV Files (*.csv)",
				Pattern:     "*.csv",
			},
			{
				DisplayName: "All Files (*.*)",
				Pattern:     "*.*",
			},
		},
	})
	if err != nil {
		return "", err
	}
	if savePath == "" {
		return "", nil
	}
	if !strings.HasSuffix(strings.ToLower(savePath), ".csv") {
		savePath += ".csv"
	}
	if err := os.WriteFile(savePath, []byte(content), 0644); err != nil {
		return "", err
	}
	return savePath, nil
}

// OpenInFolder opens Windows Explorer selecting the given file path.
func (a *LiveDebugApp) OpenInFolder(filePath string) {
	if filePath == "" {
		return
	}
	go func() {
		_ = exec.Command("explorer.exe", "/select,", filepath.Clean(filePath)).Start()
	}()
}

// runLiveDebug initializes and runs the dedicated Live Debug window instance.
func runLiveDebug() {
	debugApp := NewLiveDebugApp()

	err := wails.Run(&options.App{
		Title:            "GyroBridge - Окно статистики и 3D просмотра",
		Width:            1120,
		Height:           720,
		MinWidth:         820,
		MinHeight:        500,
		StartHidden:      true,
		BackgroundColour: &options.RGBA{R: 6, G: 8, B: 13, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					p := strings.TrimPrefix(r.URL.Path, "/")
					if p == "" || p == "index.html" || p == "index.htm" {
						r.URL.Path = "/livedebug.html"
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
		EnableDefaultContextMenu: true,
		Debug: options.Debug{
			OpenInspectorOnStartup: false,
		},
		Windows: &windows.Options{
			WebviewUserDataPath:  filepath.Join(os.Getenv("APPDATA"), "GyroBridge", "WebView2_LiveDebug"),
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("LiveDebug window error:", err.Error())
	}
}
