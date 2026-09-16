package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// Profile represents a saved calibration profile with a 3x3 signed-permutation matrix.
type Profile struct {
	Slot   int         `json:"slot"`   // 0-3
	Name   string      `json:"name"`   // user-visible name
	Matrix [3][3]float64 `json:"matrix"` // signed permutation matrix
	Active bool        `json:"active"` // is this the currently applied profile?
}

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
	// Raw sensor data for calibration wizard (live, latest sample)
	RawRotX       float64 `json:"rawRotX"` // Angular velocity X in °/s
	RawRotY       float64 `json:"rawRotY"` // Angular velocity Y in °/s
	RawRotZ       float64 `json:"rawRotZ"` // Angular velocity Z in °/s
	RawAccX       float64 `json:"rawAccX"` // Acceleration X in g
	RawAccY       float64 `json:"rawAccY"` // Acceleration Y in g
	RawAccZ       float64 `json:"rawAccZ"` // Acceleration Z in g
	// Profile system
	Profiles      []Profile `json:"profiles"`
	ActiveSlot    int       `json:"activeSlot"` // -1 = none (identity matrix)
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
	// Raw latest gyro/accel (stored as float64 bits for atomic access)
	curRotX     atomic.Uint64
	curRotY     atomic.Uint64
	curRotZ     atomic.Uint64
	curAccX     atomic.Uint64
	curAccY     atomic.Uint64
	curAccZ     atomic.Uint64
	connectedAt time.Time
	clientAddr  string
	primaryIP   string
	gamepadURL  string
	setupURL    string
	qrCodePNG   string
	setupQRPNG  string
	toggleMu    sync.Mutex
	lastToggle  time.Time
	// Profile system
	profilesMu  sync.RWMutex
	profiles    [4]Profile // exactly 4 slots, always
	activeSlot  int        // -1 = identity/none
	profilesDir string
	// Active calibration matrix (applied to frames before DSU forwarding)
	matrixMu    sync.RWMutex
	activeMatrix [3][3]float64 // identity by default
}

// identity3x3 returns the 3x3 identity matrix
func identity3x3() [3][3]float64 {
	return [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
}

// applyMatrix multiplies a 3x3 matrix by a column vector [x, y, z]
func applyMatrix(m [3][3]float64, x, y, z float64) (float64, float64, float64) {
	rx := m[0][0]*x + m[0][1]*y + m[0][2]*z
	ry := m[1][0]*x + m[1][1]*y + m[1][2]*z
	rz := m[2][0]*x + m[2][1]*y + m[2][2]*z
	return rx, ry, rz
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

	// Determine profiles dir
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = "."
	}
	profilesDir := filepath.Join(appData, "gyrobridge")

	app := &App{
		i18nMgr:      mgr,
		primaryIP:    primaryIP,
		setupURL:     setupURL,
		gamepadURL:   appURL,
		qrCodePNG:    qrBase64,
		setupQRPNG:   setupQRBase64,
		activeSlot:   -1,
		activeMatrix: identity3x3(),
		profilesDir:  profilesDir,
	}

	// Initialize 4 empty slots
	for i := range app.profiles {
		app.profiles[i] = Profile{
			Slot:   i,
			Name:   "",
			Matrix: identity3x3(),
			Active: false,
		}
	}

	// Load persisted profiles
	app.loadProfiles()

	return app
}

// loadProfiles reads profiles.json from disk
func (a *App) loadProfiles() {
	path := filepath.Join(a.profilesDir, "profiles.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // first run — empty profiles is fine
	}

	var stored struct {
		Profiles   [4]Profile `json:"profiles"`
		ActiveSlot int        `json:"activeSlot"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return
	}

	a.profilesMu.Lock()
	defer a.profilesMu.Unlock()

	for i := 0; i < 4; i++ {
		a.profiles[i] = stored.Profiles[i]
		a.profiles[i].Slot = i // ensure slot index is canonical
	}
	a.activeSlot = stored.ActiveSlot

	// Restore active matrix
	if a.activeSlot >= 0 && a.activeSlot < 4 {
		a.matrixMu.Lock()
		a.activeMatrix = a.profiles[a.activeSlot].Matrix
		a.matrixMu.Unlock()
		a.profiles[a.activeSlot].Active = true
	}
}

// saveProfiles writes profiles.json to disk
func (a *App) saveProfiles() {
	if err := os.MkdirAll(a.profilesDir, 0755); err != nil {
		return
	}
	path := filepath.Join(a.profilesDir, "profiles.json")

	a.profilesMu.RLock()
	stored := struct {
		Profiles   [4]Profile `json:"profiles"`
		ActiveSlot int        `json:"activeSlot"`
	}{
		Profiles:   a.profiles,
		ActiveSlot: a.activeSlot,
	}
	a.profilesMu.RUnlock()

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
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
		// Store latest raw gyro/accel for calibration wizard
		a.curRotX.Store(math.Float64bits(float64(frame.RotX)))
		a.curRotY.Store(math.Float64bits(float64(frame.RotY)))
		a.curRotZ.Store(math.Float64bits(float64(frame.RotZ)))
		a.curAccX.Store(math.Float64bits(float64(frame.AccX)))
		a.curAccY.Store(math.Float64bits(float64(frame.AccY)))
		a.curAccZ.Store(math.Float64bits(float64(frame.AccZ)))

		// Apply calibration matrix to the frame before forwarding to DSU
		a.matrixMu.RLock()
		mat := a.activeMatrix
		a.matrixMu.RUnlock()

		// Apply matrix to rotation rate and acceleration
		rx, ry, rz := applyMatrix(mat, float64(frame.RotX), float64(frame.RotY), float64(frame.RotZ))
		ax, ay, az := applyMatrix(mat, float64(frame.AccX), float64(frame.AccY), float64(frame.AccZ))

		corrected := frame
		corrected.RotX = float32(rx)
		corrected.RotY = float32(ry)
		corrected.RotZ = float32(rz)
		corrected.AccX = float32(ax)
		corrected.AccY = float32(ay)
		corrected.AccZ = float32(az)

		// Recompute Euler angles from quaternion (quaternion reflects physical orientation, unaffected by axis reorder)
		p, r, y := server.QuaternionToEuler(frame.Qx, frame.Qy, frame.Qz, frame.Qw)
		a.curPitch.Store(math.Float64bits(p))
		a.curRoll.Store(math.Float64bits(r))
		a.curYaw.Store(math.Float64bits(y))

		if a.isPaused.Load() {
			return // Muted during pause
		}
		if a.dsuSrv != nil {
			a.dsuSrv.SendMotion(corrected)
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

	a.profilesMu.RLock()
	profilesCopy := a.profiles
	activeSlot := a.activeSlot
	a.profilesMu.RUnlock()

	profilesList := make([]Profile, 4)
	for i := 0; i < 4; i++ {
		profilesList[i] = profilesCopy[i]
		profilesList[i].Active = (i == activeSlot)
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
		RawRotX:       math.Float64frombits(a.curRotX.Load()),
		RawRotY:       math.Float64frombits(a.curRotY.Load()),
		RawRotZ:       math.Float64frombits(a.curRotZ.Load()),
		RawAccX:       math.Float64frombits(a.curAccX.Load()),
		RawAccY:       math.Float64frombits(a.curAccY.Load()),
		RawAccZ:       math.Float64frombits(a.curAccZ.Load()),
		Profiles:      profilesList,
		ActiveSlot:    activeSlot,
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

// GetProfiles returns current 4 profile slots
func (a *App) GetProfiles() []Profile {
	a.profilesMu.RLock()
	defer a.profilesMu.RUnlock()

	result := make([]Profile, 4)
	for i := 0; i < 4; i++ {
		result[i] = a.profiles[i]
		result[i].Active = (i == a.activeSlot)
	}
	return result
}

// SaveProfile overwrites a profile slot (slot 0-3) with the given name and matrix.
// The matrix must be a valid signed-permutation matrix with determinant +1.
func (a *App) SaveProfile(slot int, name string, matrix [3][3]float64) string {
	if slot < 0 || slot > 3 {
		return "invalid slot"
	}

	// Validate matrix: determinant must be +1
	det := matrix[0][0]*(matrix[1][1]*matrix[2][2]-matrix[1][2]*matrix[2][1]) -
		matrix[0][1]*(matrix[1][0]*matrix[2][2]-matrix[1][2]*matrix[2][0]) +
		matrix[0][2]*(matrix[1][0]*matrix[2][1]-matrix[1][1]*matrix[2][0])

	if math.Abs(det-1.0) > 0.01 {
		return fmt.Sprintf("invalid matrix: determinant is %.4f, must be +1.0", det)
	}

	a.profilesMu.Lock()
	a.profiles[slot] = Profile{
		Slot:   slot,
		Name:   name,
		Matrix: matrix,
		Active: (slot == a.activeSlot),
	}
	a.profilesMu.Unlock()

	a.saveProfiles()
	a.emitStateChange()
	return "ok"
}

// SetActiveProfile selects the profile at the given slot (-1 = identity/none)
func (a *App) SetActiveProfile(slot int) string {
	if slot < -1 || slot > 3 {
		return "invalid slot"
	}

	a.profilesMu.Lock()
	// Clear previous active flag
	for i := range a.profiles {
		a.profiles[i].Active = false
	}
	a.activeSlot = slot
	if slot >= 0 {
		a.profiles[slot].Active = true
	}
	a.profilesMu.Unlock()

	// Update active matrix
	a.matrixMu.Lock()
	if slot >= 0 {
		a.profilesMu.RLock()
		a.activeMatrix = a.profiles[slot].Matrix
		a.profilesMu.RUnlock()
	} else {
		a.activeMatrix = identity3x3()
	}
	a.matrixMu.Unlock()

	a.saveProfiles()
	a.emitStateChange()
	return "ok"
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
