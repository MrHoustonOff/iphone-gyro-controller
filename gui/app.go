package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gyrobridge/pkg/ca"
	"gyrobridge/pkg/dsu"
	"gyrobridge/pkg/i18n"
	"gyrobridge/pkg/pairing"
	"gyrobridge/pkg/server"
	"gyrobridge/web"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	HTTPPort  = 8080
	HTTPSPort = 8443
)

// AppState represents the live state of Gyro Bridge
type AppState struct {
	Status        string  `json:"status"`        // "offline", "online", "paused"
	IsPaused      bool    `json:"isPaused"`
	DeviceName    string  `json:"deviceName"`    // e.g. "Controller"
	Hz            float64 `json:"hz"`
	PingMs        int     `json:"pingMs"`
	ConnectedTime string  `json:"connectedTime"` // "00:07:32"
	Pitch         float64 `json:"pitch"`         // Live pitch in degrees
	Roll          float64 `json:"roll"`          // Live roll in degrees
	Yaw           float64 `json:"yaw"`           // Live yaw in degrees
	IP            string  `json:"ip"`
	GamepadURL    string  `json:"gamepadUrl"`
	SetupURL      string  `json:"setupUrl"`
	QRCode        string  `json:"qrCode"`        // Base64 data URI
	SetupQRCode   string  `json:"setupQrCode"`   // Base64 data URI for iOS profile
}

// App struct manages desktop backend and GyroBridge services
type App struct {
	ctx         context.Context
	i18nMgr     *i18n.Manager
	srv         *server.Server
	dsuSrv      *dsu.Server
	caMgr       *ca.CertificateManager
	isPaused    atomic.Bool
	hasClient   atomic.Bool
	curPitch    atomic.Uint64
	curRoll     atomic.Uint64
	curYaw      atomic.Uint64
	connectedAt time.Time
	clientAddr  string
	primaryIP   string
	gamepadURL  string
	setupURL    string
	qrCodePNG   string
	setupQRPNG  string
	toggleMu    sync.Mutex
	lastToggle  time.Time
}

// NewApp creates a new App application struct
func NewApp() *App {
	mgr, err := i18n.NewManager("ru")
	if err != nil {
		fmt.Printf("[-] Failed to init i18n manager: %v\n", err)
	}

	lanIPs := pairing.GetLocalIPv4s()
	primaryIP := pairing.GetPrimaryIP(lanIPs)

	setupURL := fmt.Sprintf("http://%s:%d/ca.mobileconfig", primaryIP, HTTPPort)
	appURL := fmt.Sprintf("https://%s:%d", primaryIP, HTTPSPort)

	// Pre-generate Gamepad QR code PNG for instant display on start
	qrBytes, err := pairing.GenerateQRPNG(appURL, 240)
	qrBase64 := ""
	if err == nil {
		qrBase64 = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrBytes)
	}

	// Pre-generate Setup CA profile QR code PNG for iOS setup guide
	setupQRBytes, err := pairing.GenerateQRPNG(setupURL, 240)
	setupQRBase64 := ""
	if err == nil {
		setupQRBase64 = "data:image/png;base64," + base64.StdEncoding.EncodeToString(setupQRBytes)
	}

	return &App{
		i18nMgr:     mgr,
		primaryIP:   primaryIP,
		setupURL:    setupURL,
		gamepadURL:  appURL,
		qrCodePNG:   qrBase64,
		setupQRPNG:  setupQRBase64,
	}
}

// startup is called at application startup: initializes services in background
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 1. Certificate Authority
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = "."
	}
	caDir := filepath.Join(appData, "gyrobridge", "ca")

	lanIPs := pairing.GetLocalIPv4s()
	caMgr, err := ca.NewCertificateManager(caDir, lanIPs, []string{"gamepad.local"})
	if err != nil {
		fmt.Printf("[-] CA init error: %v\n", err)
		return
	}
	a.caMgr = caMgr

	// 2. DSU Server (UDP 26760)
	dsuSrv := dsu.NewServer(dsu.DefaultPort)
	if err := dsuSrv.Start(); err != nil {
		fmt.Printf("[-] DSU start error: %v\n", err)
	}
	a.dsuSrv = dsuSrv

	// 3. Web & Telemetry Server (HTTP 8080 / HTTPS 8443)
	srv := server.NewServer(caMgr, HTTPPort, HTTPSPort, web.IndexHTML, func(frame server.MotionFrame) {
		p, r, y := server.QuaternionToEuler(frame.Qx, frame.Qy, frame.Qz, frame.Qw)
		a.curPitch.Store(math.Float64bits(p))
		a.curRoll.Store(math.Float64bits(r))
		a.curYaw.Store(math.Float64bits(y))

		if a.isPaused.Load() {
			return // Muted during pause
		}
		if a.dsuSrv != nil {
			a.dsuSrv.SendMotion(frame)
		}
	})

	var disconnectTimer *time.Timer
	var disconnectMu sync.Mutex

	srv.OnClientConnect = func(remoteAddr string) {
		disconnectMu.Lock()
		if disconnectTimer != nil {
			disconnectTimer.Stop()
			disconnectTimer = nil
		}
		disconnectMu.Unlock()

		a.hasClient.Store(true)
		a.clientAddr = remoteAddr
		if a.connectedAt.IsZero() {
			a.connectedAt = time.Now()
		}
		a.emitStateChange()
	}

	srv.OnClientDisconnect = func(remoteAddr string) {
		disconnectMu.Lock()
		defer disconnectMu.Unlock()

		_, clients, _ := srv.PacketStats()
		if clients > 0 {
			return
		}

		if disconnectTimer != nil {
			disconnectTimer.Stop()
		}
		disconnectTimer = time.AfterFunc(2500*time.Millisecond, func() {
			_, c, _ := srv.PacketStats()
			if c <= 0 {
				a.hasClient.Store(false)
				a.connectedAt = time.Time{}
				a.emitStateChange()
			}
		})
	}

	srv.OnClientPause = func(isPaused bool) {
		if a.isPaused.Load() != isPaused {
			a.isPaused.Store(isPaused)
			a.emitStateChange()
		}
	}

	srv.GetIsPaused = a.isPaused.Load

	if err := srv.Start(); err != nil {
		fmt.Printf("[-] Server start error: %v\n", err)
	}
	a.srv = srv

	// Smooth live telemetry ticker (10 Hz) when client is active
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if a.hasClient.Load() {
				a.emitStateChange()
			}
		}
	}()
}

// shutdown is called when the Wails application terminates
func (a *App) shutdown(ctx context.Context) {
	if a.srv != nil {
		a.srv.Stop()
	}
	if a.dsuSrv != nil {
		a.dsuSrv.Stop()
	}
}

func (a *App) emitStateChange() {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "state:change", a.GetState())
	}
}

// GetState returns the current unified application state
func (a *App) GetState() AppState {
	status := "offline"
	hz := 0.0
	connectedDuration := "00:00:00"

	if a.hasClient.Load() {
		if a.isPaused.Load() {
			status = "paused"
		} else {
			status = "online"
		}

		if !a.connectedAt.IsZero() {
			dur := time.Since(a.connectedAt)
			h := int(dur.Hours())
			m := int(dur.Minutes()) % 60
			s := int(dur.Seconds()) % 60
			connectedDuration = fmt.Sprintf("%02d:%02d:%02d", h, m, s)
		}

		if a.srv != nil {
			_, _, curHz := a.srv.PacketStats()
			hz = curHz
		}
	}

	return AppState{
		Status:        status,
		IsPaused:      a.isPaused.Load(),
		DeviceName:    "Controller",
		Hz:            hz,
		PingMs:        3,
		ConnectedTime: connectedDuration,
		Pitch:         math.Float64frombits(a.curPitch.Load()),
		Roll:          math.Float64frombits(a.curRoll.Load()),
		Yaw:           math.Float64frombits(a.curYaw.Load()),
		IP:            a.primaryIP,
		GamepadURL:    a.gamepadURL,
		SetupURL:      a.setupURL,
		QRCode:        a.qrCodePNG,
		SetupQRCode:   a.setupQRPNG,
	}
}

// TogglePause flips stream transmission pause
func (a *App) TogglePause() AppState {
	a.toggleMu.Lock()
	defer a.toggleMu.Unlock()

	now := time.Now()
	if now.Sub(a.lastToggle) < 250*time.Millisecond {
		return a.GetState()
	}
	a.lastToggle = now

	current := a.isPaused.Load()
	next := !current
	a.isPaused.Store(next)
	if a.srv != nil {
		a.srv.BroadcastPause(next)
	}
	a.emitStateChange()
	return a.GetState()
}

// GetLanguages returns available languages
func (a *App) GetLanguages() []string {
	if a.i18nMgr != nil {
		return a.i18nMgr.Languages()
	}
	return []string{"ru", "en"}
}

// GetTranslations returns raw JSON for the specified language
func (a *App) GetTranslations(lang string) string {
	if a.i18nMgr != nil {
		if raw, ok := a.i18nMgr.RawLocaleJSON(lang); ok {
			return string(raw)
		}
	}
	return "{}"
}

// ValidateSync runs translation mutual synchronization check
func (a *App) ValidateSync() []string {
	if a.i18nMgr != nil {
		return a.i18nMgr.ValidateSync()
	}
	return nil
}
