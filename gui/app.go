package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gyrobridge/pkg/ca"
	"gyrobridge/pkg/dsu"
	"gyrobridge/pkg/filter"
	"gyrobridge/pkg/i18n"
	"gyrobridge/pkg/pairing"
	"gyrobridge/pkg/server"
	"gyrobridge/web"

	"gyrobridge-gui/resmon"

	"github.com/gorilla/websocket"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func init() {
	_ = mime.AddExtensionType(".glb", "model/gltf-binary")
}

const (
	HTTPPort  = 8080
	HTTPSPort = 8443
)

// Profile represents a saved calibration profile with a 3x3 signed-permutation matrix.
type Profile struct {
	Slot       int           `json:"slot"`   // 0-5
	Name       string        `json:"name"`   // user-visible name
	Device     string        `json:"device"` // device name e.g. "Unknown"
	Icon       string        `json:"icon"`   // "default", "vertical", "horizontal"
	Matrix     [3][3]float64 `json:"matrix"` // signed permutation matrix
	Active     bool          `json:"active"` // is this the currently applied profile?
	GyroBias   [3]float64    `json:"gyroBias,omitempty"`
	CalGravity [3]float64    `json:"calGravity,omitempty"`
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
	// Raw sensor orientation quaternion (live, latest sample from device)
	Qx            float64 `json:"qx"`
	Qy            float64 `json:"qy"`
	Qz            float64 `json:"qz"`
	Qw            float64 `json:"qw"`
	// Profile system
	Profiles      []Profile    `json:"profiles"`
	ActiveSlot    int          `json:"activeSlot"`   // -1 = none (identity matrix)
	ActiveMatrix  [3][3]float64 `json:"activeMatrix"` // currently applied calibration matrix (or preview during wizard)
	// Madgwick AHRS quaternion computed from calibrated gyro/accel (matching PadTest conventions).
	// Use these (not raw Qx/Qy/Qz/Qw) for 3D rendering.
	// Q0=w, Q1=x, Q2=y, Q3=z. Apply PadTest negate to get display: (-Q1, -Q2, Q3, Q0).
	AhrsQ0 float64 `json:"ahrsQ0"`
	AhrsQ1 float64 `json:"ahrsQ1"`
	AhrsQ2 float64 `json:"ahrsQ2"`
	AhrsQ3      float64 `json:"ahrsQ3"`
	FirstLaunch bool    `json:"firstLaunch"`
	HideAuthor  bool    `json:"hideAuthor"`
}

// captureSample holds raw 60 Hz gyro and accel readings
type captureSample struct {
	rot [3]float64 // RotX, RotY, RotZ in °/s
	acc [3]float64 // AccX, AccY, AccZ in g
}

// CaptureResult represents the computed result of a calibration gesture
type CaptureResult struct {
	Success     bool       `json:"success"`
	AxisIdx     int        `json:"axisIdx"`     // 0=X, 1=Y, 2=Z
	Sign        float64    `json:"sign"`        // +1.0 or -1.0
	AxisName    string     `json:"axisName"`    // "+X", "-Y", etc.
	Confidence  float64    `json:"confidence"`  // 0.0 to 1.0
	SampleCount int        `json:"sampleCount"`
	PeakSpeed   float64    `json:"peakSpeed"`   // peak speed in °/s
	Vector      [3]float64 `json:"vector"`      // Normalized 3D direction vector
	ErrorCode   string     `json:"errorCode"`
	ErrorMsg    string     `json:"errorMsg"`
}

// ValidationResult represents the 3-tier foolproof verification of all 3 calibration gestures
type ValidationResult struct {
	Success   bool          `json:"success"`
	ErrorCode string        `json:"errorCode"`
	ErrorMsg  string        `json:"errorMsg"`
	Matrix    [3][3]float64 `json:"matrix"`
	Det       float64       `json:"det"`
	PitchAxis string        `json:"pitchAxis"`
	YawAxis   string        `json:"yawAxis"`
	RollAxis  string        `json:"rollAxis"`
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
	curQx       atomic.Uint64
	curQy       atomic.Uint64
	curQz       atomic.Uint64
	curQw       atomic.Uint64
	deviceName  atomic.Value
	connectedAt time.Time
	clientAddr  string
	primaryIP   string
	gamepadURL  string
	setupURL    string
	qrCodePNG   string
	setupQRPNG  string
	toggleMu    sync.Mutex
	lastToggle  time.Time
	// Calibration capture buffer (buffered directly at 60 Hz from WebSocket)
	isCapturing   atomic.Bool
	captureMu     sync.Mutex
	captureBuffer []captureSample
	calVectors    [3][3]float64
	calGravity    [3]float64 // captured gravity unit vector from step 0 rest
	// Gyroscope stationary zero-bias correction (§2 of spec)
	biasMu        sync.RWMutex
	gyroBias      [3]float64
	// Profile system
	profilesMu   sync.RWMutex
	profiles     [6]Profile // exactly 6 slots, always
	activeSlot   int        // -1 = identity/none
	profilesDir  string
	// Active calibration matrix (applied to frames before DSU forwarding)
	matrixMu     sync.RWMutex
	activeMatrix [3][3]float64 // identity by default
	// Live preview matrix during calibration wizard confirm/manual screens
	previewMu     sync.RWMutex
	previewMatrix [3][3]float64
	usePreview    bool
	// Madgwick AHRS filter matching PadTest.exe exactly for 3D viewport synchronization
	ahrs             *MadgwickAHRS
	ahrsNeedConverge atomic.Bool
	accFilterReset   atomic.Bool
	// Latest AHRS quaternion stored atomically for lock-free read by GetState.
	// Q0=w, Q1=x, Q2=y, Q3=z (same as AHRS return values).
	curAhrsQ0 atomic.Uint64
	curAhrsQ1 atomic.Uint64
	curAhrsQ2 atomic.Uint64
	curAhrsQ3 atomic.Uint64
	// Buffer of last 20 raw frames from mobile device for diagnostics
	recentFramesMu sync.Mutex
	recentFrames   []RawLogFrame
	// Full calibration session report buffer
	calLogMu     sync.Mutex
	calStepLogs  map[int]StepCaptureLog
	calValResult ValidationResult
	// LiveDebug standalone window WebSocket clients and process handle
	liveDebugMu      sync.RWMutex
	liveDebugClients map[*websocket.Conn]struct{}
	liveDebugSeq     atomic.Uint64
	liveDebugCmdMu   sync.Mutex
	liveDebugCmd     *exec.Cmd
	lastMotionRecvTs atomic.Int64
	// Multi-window theme and language synchronization
	themeMu         sync.RWMutex
	currentTheme    string
	currentLang     string
	firstLaunchDone bool
	hideAuthor      bool
	// Process resource monitor (CPU / RAM)
	stopResmon   func()
	lastResStats atomic.Pointer[map[string]any]
	// Advanced configuration settings
	dsuPort          int
	httpPort         int
	httpsPort           int
	gyroDeadzoneBits    atomic.Uint64
	stillnessHint       atomic.Bool
	disconnectAlert     atomic.Bool
	soundMode           string
	// Adaptive 1-Euro DSU filter and dynamic response parameters
	gyroFilter          *filter.Vector3OneEuroFilter
	gyroSmoothingBits   atomic.Uint64 // float64 (0.0 to 1.0, default 0.50)
	gyroDeadbandBits    atomic.Uint64 // float64 (deg/s, default 0.10)
	gyroSensitivityBits atomic.Uint64 // float64 (multiplier, default 1.00)
	tuningActive        atomic.Bool
	lastTuningEmit      atomic.Int64
}

// AppSettings holds configurable parameters exposed in the settings window
type AppSettings struct {
	Theme           string  `json:"theme"`
	Lang            string  `json:"lang"`
	ActiveSlot      int     `json:"activeSlot"`
	FirstLaunchDone bool    `json:"firstLaunchDone"`
	HideAuthor      bool    `json:"hideAuthor"`
	DSUPort         int     `json:"dsuPort"`
	HTTPPort        int     `json:"httpPort"`
	HTTPSPort       int     `json:"httpsPort"`
	GyroDeadzone    float64 `json:"gyroDeadzone"`
	StillnessHint   bool    `json:"stillnessHint"`
	DisconnectAlert bool    `json:"disconnectAlert"`
	SoundMode       string  `json:"soundMode"`
	GyroSmoothing   float64 `json:"gyroSmoothing"`
	GyroDeadband    float64 `json:"gyroDeadband"`
	GyroSensitivity float64 `json:"gyroSensitivity"`
}

// TuningFrame conveys simultaneous raw and filtered telemetry to the frontend tuning bench
type TuningFrame struct {
	RawX  float32 `json:"rawX"`
	RawY  float32 `json:"rawY"`
	RawZ  float32 `json:"rawZ"`
	OutX  float32 `json:"outX"`
	OutY  float32 `json:"outY"`
	OutZ  float32 `json:"outZ"`
	Hz    float32 `json:"hz"`
	Pitch float32 `json:"pitch"`
	Roll  float32 `json:"roll"`
	Yaw   float32 `json:"yaw"`
}

// StepCaptureLog stores the full recorded session of a calibration gesture step
type StepCaptureLog struct {
	Step      int             `json:"step"`
	Samples   []captureSample `json:"samples"`
	Result    CaptureResult   `json:"result"`
	Timestamp time.Time       `json:"timestamp"`
}

// RawLogFrame holds exact raw telemetry frame fields for debug logging
type RawLogFrame struct {
	Timestamp uint32  `json:"ts"`
	RotX      float32 `json:"rotX"`
	RotY      float32 `json:"rotY"`
	RotZ      float32 `json:"rotZ"`
	AccX      float32 `json:"accX"`
	AccY      float32 `json:"accY"`
	AccZ      float32 `json:"accZ"`
	Qx        float32 `json:"qx"`
	Qy        float32 `json:"qy"`
	Qz        float32 `json:"qz"`
	Qw        float32 `json:"qw"`
}

// identity3x3 returns the 3x3 identity matrix
func identity3x3() [3][3]float64 {
	return [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
}

// defaultMatrix3x3 returns the canonical portrait orientation matrix for Cemuhook DSU:
// Pitch = +X, Yaw = +Y, Roll = -Z (det = -1.0)
func defaultMatrix3x3() [3][3]float64 {
	return [3][3]float64{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, -1},
	}
}

// computeAccMatrix builds the accelerometer alignment matrix so that resting gravity
// is guaranteed to map exactly to AccY = -|g|, with AccX = 0 and AccZ = 0 in Cemuhook DSU.
// In Cemuhook DSU / PadTest, the vertical axis of the gamepad is Y (Green axis pointing UP).
// When the controller is resting flat on a table, gravity points down along -AccY.
// pitchAxisOpt allows providing the calibrated pitch axis (Row 0 of active matrix) so
// that AccX (lateral) is aligned with the controller's lateral axis regardless of landscape or portrait.
func computeAccMatrix(calGravity [3]float64, pitchAxisOpt ...[3]float64) [3][3]float64 {
	gx, gy, gz := calGravity[0], calGravity[1], calGravity[2]
	gNorm := math.Sqrt(gx*gx + gy*gy + gz*gz)
	if gNorm < 0.3 {
		// Default: phone sitting flat on table, screen up -> Phone Acc = [0, 0, -1.0]
		// Maps Phone Z to AccY so resting gravity lands on AccY = -1.0g
		return [3][3]float64{
			{1, 0, 0},
			{0, 0, 1},
			{0, -1, 0},
		}
	}

	// Unit resting gravity vector pointing down in phone frame
	uG := [3]float64{gx / gNorm, gy / gNorm, gz / gNorm}

	// In Cemuhook DSU, resting gravity points along -AccY (Row 1).
	// We want Row 1 = -uG so that Row 1 · uG = -1.0
	row1 := [3]float64{-uG[0], -uG[1], -uG[2]}

	// Pick a reference direction for Row 0 (lateral X axis, pitch) orthogonal to row1
	ref := [3]float64{1, 0, 0}
	if len(pitchAxisOpt) > 0 {
		pAx := pitchAxisOpt[0]
		if math.Abs(pAx[0])+math.Abs(pAx[1])+math.Abs(pAx[2]) > 0.5 {
			ref = pAx
		}
	} else if math.Abs(uG[0]) > 0.8 {
		ref = [3]float64{0, 0, 1}
	}

	// If ref is nearly collinear with row1, choose an alternate reference
	dot := ref[0]*row1[0] + ref[1]*row1[1] + ref[2]*row1[2]
	if math.Abs(math.Abs(dot)-1.0) < 0.15 {
		if math.Abs(row1[0]) < 0.8 {
			ref = [3]float64{1, 0, 0}
		} else {
			ref = [3]float64{0, 1, 0}
		}
		dot = ref[0]*row1[0] + ref[1]*row1[1] + ref[2]*row1[2]
	}

	// Gram-Schmidt for Row 0: ref - (ref · row1) * row1
	row0 := [3]float64{ref[0] - dot*row1[0], ref[1] - dot*row1[1], ref[2] - dot*row1[2]}
	r0Norm := math.Sqrt(row0[0]*row0[0] + row0[1]*row0[1] + row0[2]*row0[2])
	if r0Norm > 1e-4 {
		row0 = [3]float64{row0[0] / r0Norm, row0[1] / r0Norm, row0[2] / r0Norm}
	} else {
		row0 = [3]float64{1, 0, 0}
	}

	// Row 2 = row0 x row1 (Z axis, longitudinal, roll)
	row2 := [3]float64{
		row0[1]*row1[2] - row0[2]*row1[1],
		row0[2]*row1[0] - row0[0]*row1[2],
		row0[0]*row1[1] - row0[1]*row1[0],
	}

	return [3][3]float64{row0, row1, row2}
}

// applyMatrix multiplies a 3x3 matrix by a column vector [x, y, z]
func applyMatrix(m [3][3]float64, x, y, z float64) (float64, float64, float64) {
	rx := m[0][0]*x + m[0][1]*y + m[0][2]*z
	ry := m[1][0]*x + m[1][1]*y + m[1][2]*z
	rz := m[2][0]*x + m[2][1]*y + m[2][2]*z
	return rx, ry, rz
}

// matMul multiplies two 3x3 matrices: c = a * b
func matMul(a, b [3][3]float64) [3][3]float64 {
	var c [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			c[i][j] = a[i][0]*b[0][j] + a[i][1]*b[1][j] + a[i][2]*b[2][j]
		}
	}
	return c
}

// matTranspose returns the transpose of a 3x3 matrix
func matTranspose(a [3][3]float64) [3][3]float64 {
	return [3][3]float64{
		{a[0][0], a[1][0], a[2][0]},
		{a[0][1], a[1][1], a[2][1]},
		{a[0][2], a[1][2], a[2][2]},
	}
}

// quatToMatrix converts a unit quaternion (qx, qy, qz, qw) to a 3x3 SO(3) rotation matrix
func quatToMatrix(qx, qy, qz, qw float64) [3][3]float64 {
	norm := math.Sqrt(qx*qx + qy*qy + qz*qz + qw*qw)
	if norm > 1e-9 {
		qx /= norm
		qy /= norm
		qz /= norm
		qw /= norm
	} else {
		return [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	}

	xx := qx * qx
	yy := qy * qy
	zz := qz * qz
	xy := qx * qy
	xz := qx * qz
	yz := qy * qz
	wx := qw * qx
	wy := qw * qy
	wz := qw * qz

	return [3][3]float64{
		{1 - 2*(yy+zz), 2 * (xy - wz), 2 * (xz + wy)},
		{2 * (xy + wz), 1 - 2*(xx+zz), 2 * (yz - wx)},
		{2 * (xz - wy), 2 * (yz + wx), 1 - 2*(xx+yy)},
	}
}

// matrixToQuat converts a 3x3 SO(3) rotation matrix to a unit quaternion (qx, qy, qz, qw)
func matrixToQuat(m [3][3]float64) (qx, qy, qz, qw float64) {
	tr := m[0][0] + m[1][1] + m[2][2]
	if tr > 0 {
		s := 0.5 / math.Sqrt(tr+1.0)
		qw = 0.25 / s
		qx = (m[2][1] - m[1][2]) * s
		qy = (m[0][2] - m[2][0]) * s
		qz = (m[1][0] - m[0][1]) * s
	} else if m[0][0] > m[1][1] && m[0][0] > m[2][2] {
		s := 2.0 * math.Sqrt(math.Max(0, 1.0+m[0][0]-m[1][1]-m[2][2]))
		if s > 1e-9 {
			qw = (m[2][1] - m[1][2]) / s
			qx = 0.25 * s
			qy = (m[0][1] + m[1][0]) / s
			qz = (m[0][2] + m[2][0]) / s
		} else {
			qw = 1
		}
	} else if m[1][1] > m[2][2] {
		s := 2.0 * math.Sqrt(math.Max(0, 1.0+m[1][1]-m[0][0]-m[2][2]))
		if s > 1e-9 {
			qw = (m[0][2] - m[2][0]) / s
			qx = (m[0][1] + m[1][0]) / s
			qy = 0.25 * s
			qz = (m[1][2] + m[2][1]) / s
		} else {
			qw = 1
		}
	} else {
		s := 2.0 * math.Sqrt(math.Max(0, 1.0+m[2][2]-m[0][0]-m[1][1]))
		if s > 1e-9 {
			qw = (m[1][0] - m[0][1]) / s
			qx = (m[0][2] + m[2][0]) / s
			qy = (m[1][2] + m[2][1]) / s
			qz = 0.25 * s
		} else {
			qw = 1
		}
	}

	norm := math.Sqrt(qx*qx + qy*qy + qz*qz + qw*qw)
	if norm > 1e-9 {
		qx /= norm
		qy /= norm
		qz /= norm
		qw /= norm
	}
	if qw < 0 {
		qx, qy, qz, qw = -qx, -qy, -qz, -qw
	}
	return qx, qy, qz, qw
}

// matrixToEuler extracts intuitive (Pitch, Roll, Yaw) in degrees from an SO(3) controller rotation matrix
func matrixToEuler(m [3][3]float64) (pitch, roll, yaw float64) {
	// Pitch: forward/backward tilt
	sinp := -m[1][2]
	if sinp >= 1.0 {
		pitch = 90.0
	} else if sinp <= -1.0 {
		pitch = -90.0
	} else {
		pitch = math.Asin(sinp) * 180.0 / math.Pi
	}

	// Roll: left/right tilt (positive = tilt right)
	roll = math.Atan2(m[1][0], m[1][1]) * 180.0 / math.Pi

	// Yaw: spin around vertical axis (positive = turn right)
	yaw = math.Atan2(m[0][2], m[2][2]) * 180.0 / math.Pi

	return pitch, roll, yaw
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
	appURL := fmt.Sprintf("https://%s:%d/", primaryIP, HTTPSPort)

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
		dsuPort:      26760,
		httpPort:     HTTPPort,
		httpsPort:    HTTPSPort,
		setupURL:     setupURL,
		gamepadURL:   appURL,
		qrCodePNG:    qrBase64,
		setupQRPNG:   setupQRBase64,
		activeSlot:   -1,
		activeMatrix: defaultMatrix3x3(),
		profilesDir:  profilesDir,
		calStepLogs:  make(map[int]StepCaptureLog),
		ahrs:         NewMadgwickAHRS(0.0), // Pure gyro integration with stationary deadband
		gyroFilter:   filter.NewVector3OneEuroFilter(1.7, 0.015, 1.0),
		currentTheme: "dark",
		currentLang:  "ru",
	}
	app.gyroDeadzoneBits.Store(math.Float64bits(0.20))
	app.gyroSmoothingBits.Store(math.Float64bits(0.50))
	app.gyroDeadbandBits.Store(math.Float64bits(0.10))
	app.gyroSensitivityBits.Store(math.Float64bits(1.00))
	app.ahrsNeedConverge.Store(true)
	app.stillnessHint.Store(true)
	app.disconnectAlert.Store(true)
	app.soundMode = "cute"
	app.deviceName.Store("Controller")

	// Initialize 6 empty slots with default portrait matrix
	for i := range app.profiles {
		app.profiles[i] = Profile{
			Slot:   i,
			Name:   "",
			Device: "Unknown",
			Icon:   "default",
			Matrix: defaultMatrix3x3(),
			Active: false,
		}
	}

	// Ensure logs directory exists
	_ = os.MkdirAll(filepath.Join(profilesDir, "logs"), 0755)

	// Load persisted settings and profiles
	app.loadSettings()
	app.rebuildURLsAndQRCodes()
	app.loadProfiles()
	app.logEvent("INFO", "GyroBridge initialized: IP=%s, Theme=%s, Lang=%s, DSU=%d, HTTP=%d, HTTPS=%d", primaryIP, app.currentTheme, app.currentLang, app.dsuPort, app.httpPort, app.httpsPort)

	return app
}

const CurrentProfileSchemaVersion = 2

// logEvent writes a timestamped line to %APPDATA%/gyrobridge/logs/gyrobridge.log
func (a *App) logEvent(level, format string, args ...any) {
	if a.profilesDir == "" {
		return
	}
	logDir := filepath.Join(a.profilesDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "gyrobridge.log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(f, "[%s] [%s] %s\n", ts, level, msg)
}

// loadSettings loads theme, language, and slot preferences from settings.json
func (a *App) loadSettings() {
	if a.profilesDir == "" {
		return
	}
	path := filepath.Join(a.profilesDir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		// First launch!
		a.themeMu.Lock()
		a.firstLaunchDone = false
		if a.currentLang == "" {
			a.currentLang = "ru"
		}
		a.themeMu.Unlock()
		return
	}
	var s struct {
		Theme           string   `json:"theme"`
		Lang            string   `json:"lang"`
		ActiveSlot      int      `json:"activeSlot"`
		FirstLaunchDone bool     `json:"firstLaunchDone"`
		HideAuthor      bool     `json:"hideAuthor"`
		DSUPort         int      `json:"dsuPort"`
		HTTPPort        int      `json:"httpPort"`
		HTTPSPort       int      `json:"httpsPort"`
		GyroDeadzone    float64  `json:"gyroDeadzone"`
		StillnessHint   *bool    `json:"stillnessHint"`
		DisconnectAlert *bool    `json:"disconnectAlert"`
		SoundMode       string   `json:"soundMode"`
		GyroSmoothing   *float64 `json:"gyroSmoothing,omitempty"`
		GyroDeadband    *float64 `json:"gyroDeadband,omitempty"`
		GyroSensitivity *float64 `json:"gyroSensitivity,omitempty"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	a.themeMu.Lock()
	if s.Theme == "dark" || s.Theme == "light" {
		a.currentTheme = s.Theme
	}
	if s.Lang == "ru" || s.Lang == "en" {
		a.currentLang = s.Lang
	} else if a.currentLang == "" {
		a.currentLang = "ru"
	}
	a.firstLaunchDone = s.FirstLaunchDone
	a.hideAuthor = s.HideAuthor
	a.themeMu.Unlock()

	a.profilesMu.Lock()
	if s.ActiveSlot >= 0 && s.ActiveSlot < 6 {
		a.activeSlot = s.ActiveSlot
	}
	a.profilesMu.Unlock()

	if s.DSUPort >= 1024 && s.DSUPort <= 65535 {
		a.dsuPort = s.DSUPort
	}
	if s.HTTPPort >= 1024 && s.HTTPPort <= 65535 {
		a.httpPort = s.HTTPPort
	}
	if s.HTTPSPort >= 1024 && s.HTTPSPort <= 65535 {
		a.httpsPort = s.HTTPSPort
	}
	if s.GyroDeadzone > 0 {
		a.gyroDeadzoneBits.Store(math.Float64bits(s.GyroDeadzone))
	}
	if s.StillnessHint != nil {
		a.stillnessHint.Store(*s.StillnessHint)
	}
	if s.DisconnectAlert != nil {
		a.disconnectAlert.Store(*s.DisconnectAlert)
	}
	if s.SoundMode != "" {
		a.soundMode = s.SoundMode
	} else {
		a.soundMode = "cute"
	}
	if s.GyroSmoothing != nil {
		a.gyroSmoothingBits.Store(math.Float64bits(*s.GyroSmoothing))
	} else {
		a.gyroSmoothingBits.Store(math.Float64bits(0.50))
	}
	if s.GyroDeadband != nil {
		a.gyroDeadbandBits.Store(math.Float64bits(*s.GyroDeadband))
	} else if s.GyroDeadzone > 0 {
		a.gyroDeadbandBits.Store(math.Float64bits(s.GyroDeadzone))
	} else {
		a.gyroDeadbandBits.Store(math.Float64bits(0.10))
	}
	if s.GyroSensitivity != nil && *s.GyroSensitivity > 0 {
		a.gyroSensitivityBits.Store(math.Float64bits(*s.GyroSensitivity))
	} else {
		a.gyroSensitivityBits.Store(math.Float64bits(1.00))
	}
	a.updateFilterParams()
}

// saveSettings persists theme, language, activeSlot, and preferences to settings.json
func (a *App) saveSettings() {
	if a.profilesDir == "" {
		return
	}
	if err := os.MkdirAll(a.profilesDir, 0755); err != nil {
		return
	}
	path := filepath.Join(a.profilesDir, "settings.json")

	a.themeMu.RLock()
	theme := a.currentTheme
	lang := a.currentLang
	firstLaunchDone := a.firstLaunchDone
	hideAuthor := a.hideAuthor
	a.themeMu.RUnlock()

	a.profilesMu.RLock()
	slot := a.activeSlot
	a.profilesMu.RUnlock()

	dsuP := a.dsuPort
	if dsuP == 0 {
		dsuP = 26760
	}
	httpP := a.httpPort
	if httpP == 0 {
		httpP = HTTPPort
	}
	httpsP := a.httpsPort
	if httpsP == 0 {
		httpsP = HTTPSPort
	}

	deadzone := math.Float64frombits(a.gyroDeadzoneBits.Load())
	if deadzone == 0 && a.gyroDeadzoneBits.Load() == 0 {
		deadzone = 0.20
	}

	soundM := a.soundMode
	if soundM == "" {
		soundM = "cute"
	}

	smoothing := math.Float64frombits(a.gyroSmoothingBits.Load())
	deadband := math.Float64frombits(a.gyroDeadbandBits.Load())
	sensitivity := math.Float64frombits(a.gyroSensitivityBits.Load())
	if sensitivity <= 0 {
		sensitivity = 1.00
	}

	s := AppSettings{
		Theme:           theme,
		Lang:            lang,
		ActiveSlot:      slot,
		FirstLaunchDone: firstLaunchDone,
		HideAuthor:      hideAuthor,
		DSUPort:         dsuP,
		HTTPPort:        httpP,
		HTTPSPort:       httpsP,
		GyroDeadzone:    deadzone,
		StillnessHint:   a.stillnessHint.Load(),
		DisconnectAlert: a.disconnectAlert.Load(),
		SoundMode:       soundM,
		GyroSmoothing:   smoothing,
		GyroDeadband:    deadband,
		GyroSensitivity: sensitivity,
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func (a *App) updateFilterParams() {
	if a.gyroFilter == nil {
		return
	}
	smoothing := math.Float64frombits(a.gyroSmoothingBits.Load())
	if smoothing <= 0 {
		return
	}
	// Dynamic 1-Euro tuning: minCutoff from 3.0 Hz down to 0.4 Hz
	minCutoff := 3.0 - smoothing*2.6
	if minCutoff < 0.2 {
		minCutoff = 0.2
	}
	beta := 0.005 + (1.0-smoothing)*0.025
	a.gyroFilter.SetParams(minCutoff, beta, 1.0)
}

// loadProfiles reads profiles.json from disk
func (a *App) loadProfiles() {
	path := filepath.Join(a.profilesDir, "profiles.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // first run — default profiles are fine
	}

	var stored struct {
		SchemaVersion int        `json:"schemaVersion"`
		Profiles      []Profile  `json:"profiles"`
		ActiveSlot    int        `json:"activeSlot"`
		GyroBias      [3]float64 `json:"gyroBias"`
		CalGravity    [3]float64 `json:"calGravity,omitempty"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return
	}

	a.profilesMu.Lock()
	defer a.profilesMu.Unlock()

	// If schema version is outdated (< 2), reset all profiles to canonical defaults (§5 of spec)
	if stored.SchemaVersion < CurrentProfileSchemaVersion {
		for i := 0; i < 6; i++ {
			a.profiles[i] = Profile{
				Slot:   i,
				Name:   "",
				Device: "Unknown",
				Icon:   "default",
				Matrix: defaultMatrix3x3(),
				Active: false,
			}
		}
		a.activeSlot = -1
		return
	}

	for i := 0; i < 6; i++ {
		if i < len(stored.Profiles) {
			a.profiles[i] = stored.Profiles[i]
		} else {
			a.profiles[i] = Profile{
				Slot:   i,
				Name:   "",
				Device: "Unknown",
				Icon:   "default",
				Matrix: defaultMatrix3x3(),
				Active: false,
			}
		}
		a.profiles[i].Slot = i // ensure slot index is canonical
		if a.profiles[i].Device == "" {
			a.profiles[i].Device = "Unknown"
		}
		if a.profiles[i].Icon == "" {
			a.profiles[i].Icon = "default"
		}
		// Validate matrix: determinant must be |det| ≈ 1.0 (valid signed-permutation matrix)
		if math.Abs(math.Abs(det3x3(a.profiles[i].Matrix))-1.0) > 0.05 {
			a.profiles[i].Matrix = defaultMatrix3x3()
		}
	}
	a.activeSlot = stored.ActiveSlot

	// Restore active matrix, bias, and gravity
	if a.activeSlot >= 0 && a.activeSlot < 6 {
		p := a.profiles[a.activeSlot]
		a.matrixMu.Lock()
		a.activeMatrix = p.Matrix
		a.matrixMu.Unlock()
		a.profiles[a.activeSlot].Active = true

		if p.GyroBias != [3]float64{0, 0, 0} {
			stored.GyroBias = p.GyroBias
		}
		if math.Sqrt(p.CalGravity[0]*p.CalGravity[0]+p.CalGravity[1]*p.CalGravity[1]+p.CalGravity[2]*p.CalGravity[2]) > 0.3 {
			stored.CalGravity = p.CalGravity
		}
	}

	a.biasMu.Lock()
	a.gyroBias = stored.GyroBias
	a.biasMu.Unlock()

	if math.Sqrt(stored.CalGravity[0]*stored.CalGravity[0]+stored.CalGravity[1]*stored.CalGravity[1]+stored.CalGravity[2]*stored.CalGravity[2]) > 0.3 {
		a.calGravity = stored.CalGravity
	}
}

// saveProfiles writes profiles.json to disk with schemaVersion 2
func (a *App) saveProfiles() {
	if err := os.MkdirAll(a.profilesDir, 0755); err != nil {
		return
	}
	path := filepath.Join(a.profilesDir, "profiles.json")

	a.profilesMu.RLock()
	a.biasMu.RLock()
	stored := struct {
		SchemaVersion int        `json:"schemaVersion"`
		Profiles      [6]Profile `json:"profiles"`
		ActiveSlot    int        `json:"activeSlot"`
		GyroBias      [3]float64 `json:"gyroBias"`
		CalGravity    [3]float64 `json:"calGravity,omitempty"`
	}{
		SchemaVersion: CurrentProfileSchemaVersion,
		Profiles:      a.profiles,
		ActiveSlot:    a.activeSlot,
		GyroBias:      a.gyroBias,
		CalGravity:    a.calGravity,
	}
	a.biasMu.RUnlock()
	a.profilesMu.RUnlock()

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
	a.saveSettings()
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

	// 2. DSU Server (UDP default 26760)
	dsuSrv := dsu.NewServer(a.dsuPort)
	if err := dsuSrv.Start(); err != nil {
		fmt.Printf("[-] DSU start error: %v\n", err)
	}
	a.dsuSrv = dsuSrv

	var (
		accFiltered   [3]float64
		accFilterInit bool
		stillFrames   int
		stillSumGx    float64
		stillSumGy    float64
		stillSumGz    float64
	)

	// 3. Web & Telemetry Server (HTTP / HTTPS)
	var srv *server.Server
	srv = server.NewServer(caMgr, a.httpPort, a.httpsPort, web.IndexHTML, func(frame server.MotionFrame) {
		startPipe := time.Now()
		recvTs := startPipe.UnixMilli()
		a.lastMotionRecvTs.Store(recvTs)

		if !a.hasClient.Load() {
			a.hasClient.Store(true)
			if a.connectedAt.IsZero() {
				a.connectedAt = time.Now()
			}
			a.emitStateChange()
			a.broadcastLiveDebugJSON(map[string]any{
				"type":      "device_status",
				"connected": true,
			})
		}

		// Store latest raw gyro/accel/quaternion for calibration wizard
		a.curRotX.Store(math.Float64bits(float64(frame.RotX)))
		a.curRotY.Store(math.Float64bits(float64(frame.RotY)))
		a.curRotZ.Store(math.Float64bits(float64(frame.RotZ)))
		a.curAccX.Store(math.Float64bits(float64(frame.AccX)))
		a.curAccY.Store(math.Float64bits(float64(frame.AccY)))
		a.curAccZ.Store(math.Float64bits(float64(frame.AccZ)))
		a.curQx.Store(math.Float64bits(float64(frame.Qx)))
		a.curQy.Store(math.Float64bits(float64(frame.Qy)))
		a.curQz.Store(math.Float64bits(float64(frame.Qz)))
		a.curQw.Store(math.Float64bits(float64(frame.Qw)))

		// Record raw frame in rolling 20-frame debug buffer
		a.recentFramesMu.Lock()
		a.recentFrames = append(a.recentFrames, RawLogFrame{
			Timestamp: frame.Timestamp,
			RotX:      frame.RotX,
			RotY:      frame.RotY,
			RotZ:      frame.RotZ,
			AccX:      frame.AccX,
			AccY:      frame.AccY,
			AccZ:      frame.AccZ,
			Qx:        frame.Qx,
			Qy:        frame.Qy,
			Qz:        frame.Qz,
			Qw:        frame.Qw,
		})
		if len(a.recentFrames) > 20 {
			a.recentFrames = a.recentFrames[len(a.recentFrames)-20:]
		}
		a.recentFramesMu.Unlock()

		// If calibration gesture recording is active, capture every 60 Hz frame
		if a.isCapturing.Load() {
			a.captureMu.Lock()
			a.captureBuffer = append(a.captureBuffer, captureSample{
				rot: [3]float64{float64(frame.RotX), float64(frame.RotY), float64(frame.RotZ)},
				acc: [3]float64{float64(frame.AccX), float64(frame.AccY), float64(frame.AccZ)},
			})
			a.captureMu.Unlock()
		}

		// Apply active or preview calibration matrix to the frame
		a.previewMu.RLock()
		usePrev := a.usePreview
		prevMat := a.previewMatrix
		a.previewMu.RUnlock()

		var mat [3][3]float64
		if usePrev {
			mat = prevMat
		} else {
			a.matrixMu.RLock()
			mat = a.activeMatrix
			a.matrixMu.RUnlock()
		}

		// Automatic resting zero-bias refinement:
		// When the device is completely still on a flat surface:
		// Acc magnitude is ≈ 1.0g (0.92 .. 1.08) and raw gyro rate is < 0.35 °/s
		accMag := math.Sqrt(float64(frame.AccX*frame.AccX + frame.AccY*frame.AccY + frame.AccZ*frame.AccZ))
		rawGyroSpeed := math.Sqrt(float64(frame.RotX*frame.RotX + frame.RotY*frame.RotY + frame.RotZ*frame.RotZ))

		if !a.isCapturing.Load() && accMag >= 0.92 && accMag <= 1.08 && rawGyroSpeed < 0.35 {
			stillFrames++
			stillSumGx += float64(frame.RotX)
			stillSumGy += float64(frame.RotY)
			stillSumGz += float64(frame.RotZ)
			if stillFrames >= 60 { // 1 full second of stationary rest
				avgGx := stillSumGx / 60.0
				avgGy := stillSumGy / 60.0
				avgGz := stillSumGz / 60.0
				stillFrames = 0
				stillSumGx = 0
				stillSumGy = 0
				stillSumGz = 0

				const alpha = 0.05
				a.biasMu.Lock()
				a.gyroBias[0] += alpha * (avgGx - a.gyroBias[0])
				a.gyroBias[1] += alpha * (avgGy - a.gyroBias[1])
				a.gyroBias[2] += alpha * (avgGz - a.gyroBias[2])
				a.biasMu.Unlock()
			}
		} else {
			stillFrames = 0
			stillSumGx = 0
			stillSumGy = 0
			stillSumGz = 0
		}

		// Subtract gyro zero-bias before applying calibration matrix M (§1, §2 of spec)
		a.biasMu.RLock()
		bx, by, bz := a.gyroBias[0], a.gyroBias[1], a.gyroBias[2]
		a.biasMu.RUnlock()

		rawRx := float64(frame.RotX) - bx
		rawRy := float64(frame.RotY) - by
		rawRz := float64(frame.RotZ) - bz

		// Apply matrix to rotation rate and acceleration
		rx, ry, rz := applyMatrix(mat, rawRx, rawRy, rawRz)
		accMat := computeAccMatrix(a.calGravity, mat[0])
		ax, ay, az := applyMatrix(accMat, float64(frame.AccX), float64(frame.AccY), float64(frame.AccZ))

		gyroSpeed := math.Sqrt(rx*rx + ry*ry + rz*rz)

		// 1. Gyroscope deadband and 1-Euro adaptive smoothing pipeline.
		deadband := math.Float64frombits(a.gyroDeadbandBits.Load())
		if deadband <= 0 && a.gyroDeadzoneBits.Load() > 0 {
			deadband = math.Float64frombits(a.gyroDeadzoneBits.Load())
		}
		gyroDeadband := deadband
		smoothing := math.Float64frombits(a.gyroSmoothingBits.Load())
		sens := math.Float64frombits(a.gyroSensitivityBits.Load())
		if sens <= 0 {
			sens = 1.00
		}

		// Raw converted velocities for AHRS and DSU (deg/s)
		var ahrsRx, ahrsRy, ahrsRz float32
		rawDsuRx := float32(rx)
		rawDsuRy := -float32(ry) // Cemuhook DSU protocol convention (nose right / clockwise is negative)
		rawDsuRz := float32(rz)

		// AHRS input (clean deadbanded signal for rock-solid stationary 3D model)
		if gyroSpeed < deadband {
			ahrsRx = 0
			ahrsRy = 0
			ahrsRz = 0
		} else {
			scale := float32((gyroSpeed - deadband) / gyroSpeed)
			ahrsRx = rawDsuRx * scale
			ahrsRy = rawDsuRy * scale
			ahrsRz = rawDsuRz * scale
		}

		// Step A: Anti-tremor deadband with C1-continuous Hermite smoothstep
		dbRx := filter.ApplySmoothDeadband(float64(rawDsuRx), gyroSpeed, deadband)
		dbRy := filter.ApplySmoothDeadband(float64(rawDsuRy), gyroSpeed, deadband)
		dbRz := filter.ApplySmoothDeadband(float64(rawDsuRz), gyroSpeed, deadband)

		// Step B: 1-Euro Adaptive Filter (reduces micro-tremor when slow, 0-lag on fast flicks)
		var filtRx, filtRy, filtRz float64
		if smoothing > 0 && a.gyroFilter != nil {
			filtRx, filtRy, filtRz = a.gyroFilter.Filter(dbRx, dbRy, dbRz, startPipe)
		} else {
			filtRx, filtRy, filtRz = dbRx, dbRy, dbRz
		}

		// Step C: DSU Sensitivity Multiplier
		var dsuRx, dsuRy, dsuRz float32
		dsuRx = float32(filtRx * sens)
		dsuRy = float32(filtRy * sens)
		dsuRz = float32(filtRz * sens)

		// 2. Accelerometer filtering and stationary table lock.
		// PadTest's Madgwick AHRS normalizes the error vector (2*beta*s/|s| = 11.5 deg/s step),
		// meaning even tiny 0.003g electrical noise causes violent 60 Hz limit-cycle shaking.
		// When the phone is resting on the table in neutral (gyroSpeed < 0.25 and Acc ≈ [0, -1, 0]),
		// we lock Acc exactly to [0, -1.0, 0] which cancels the gradient error to 0.000000.
		// When moving or held in hands, we apply an exponential moving average (EMA) low-pass filter
		// to eliminate 60 Hz electrical noise while tracking true gravity smoothly.
		if a.accFilterReset.Swap(false) || !accFilterInit {
			accFiltered = [3]float64{ax, ay, az}
			accFilterInit = true
		}

		isRestingOnTable := gyroSpeed < gyroDeadband &&
			math.Abs(ax) < 0.015 &&
			math.Abs(ay+1.0) < 0.025 &&
			math.Abs(az) < 0.015

		var finalAx, finalAy, finalAz float32
		if isRestingOnTable {
			finalAx = 0.0
			finalAy = -1.0
			finalAz = 0.0
			accFiltered = [3]float64{0.0, -1.0, 0.0}
		} else {
			alpha := 0.20
			if gyroSpeed < gyroDeadband {
				alpha = 0.05
			}
			accFiltered[0] += alpha * (ax - accFiltered[0])
			accFiltered[1] += alpha * (ay - accFiltered[1])
			accFiltered[2] += alpha * (az - accFiltered[2])
			finalAx = float32(accFiltered[0])
			finalAy = float32(accFiltered[1])
			finalAz = float32(accFiltered[2])
		}

		corrected := frame
		corrected.RotX = ahrsRx
		corrected.RotY = ahrsRy
		corrected.RotZ = ahrsRz
		corrected.AccX = finalAx
		corrected.AccY = finalAy
		corrected.AccZ = finalAz

		// Update Madgwick AHRS filter.
		var curP, curR, curY float64
		if a.ahrs != nil {
			if a.ahrsNeedConverge.Swap(false) {
				a.ahrs.ConvergeToGravity(finalAx, finalAy, finalAz)
			}
			q0, q1, q2, q3 := a.ahrs.Update(ahrsRx, ahrsRy, ahrsRz, finalAx, finalAy, finalAz, time.Now())
			p, r, y := a.ahrs.GetEulerAngles()
			curP, curR, curY = p, r, y
			a.curPitch.Store(math.Float64bits(p))
			a.curRoll.Store(math.Float64bits(r))
			a.curYaw.Store(math.Float64bits(y))
			a.curAhrsQ0.Store(math.Float64bits(float64(q0)))
			a.curAhrsQ1.Store(math.Float64bits(float64(q1)))
			a.curAhrsQ2.Store(math.Float64bits(float64(q2)))
			a.curAhrsQ3.Store(math.Float64bits(float64(q3)))

			var dsuClients int
			if a.dsuSrv != nil {
				dsuClients = a.dsuSrv.ActiveClients()
			}
			_, _, inHz := srv.PacketStats()
			pipeMs := float64(time.Since(startPipe).Microseconds()) / 1000.0
			sendTs := time.Now().UnixMilli()
			seq := a.liveDebugSeq.Add(1)

			a.broadcastLiveDebug(q0, q1, q2, q3, liveDebugMsg{
				Seq:        seq,
				Timestamp:  frame.Timestamp,
				RecvTs:     recvTs,
				SendTs:     sendTs,
				RawGx:      frame.RotX,
				RawGy:      frame.RotY,
				RawGz:      frame.RotZ,
				RawAx:      frame.AccX,
				RawAy:      frame.AccY,
				RawAz:      frame.AccZ,
				OutGx:      dsuRx,
				OutGy:      dsuRy,
				OutGz:      dsuRz,
				OutAx:      finalAx,
				OutAy:      finalAy,
				OutAz:      finalAz,
				StickLx:    0,
				StickLy:    0,
				InHz:       inHz,
				OutHz:      inHz,
				PipeMs:     pipeMs,
				DsuClients: dsuClients,
			})
		}

		if a.isPaused.Load() {
			return // Muted during pause
		}
		if a.dsuSrv != nil {
			dsuFrame := frame
			dsuFrame.RotX = dsuRx
			dsuFrame.RotY = dsuRy
			dsuFrame.RotZ = dsuRz
			dsuFrame.AccX = finalAx
			dsuFrame.AccY = finalAy
			dsuFrame.AccZ = finalAz
			a.dsuSrv.SendMotion(dsuFrame)
		}

		// Stream real-time telemetry to Settings test bench if active (throttled to ~60Hz to prevent IPC queue clogging)
		if a.tuningActive.Load() && a.ctx != nil {
			nowMs := time.Now().UnixMilli()
			if nowMs-a.lastTuningEmit.Load() >= 16 {
				a.lastTuningEmit.Store(nowMs)
				_, _, inHz := srv.PacketStats()
				wailsRuntime.EventsEmit(a.ctx, "tuning:frame", TuningFrame{
					RawX:  rawDsuRx,
					RawY:  rawDsuRy,
					RawZ:  rawDsuRz,
					OutX:  dsuRx,
					OutY:  dsuRy,
					OutZ:  dsuRz,
					Hz:    float32(inHz),
					Pitch: float32(curP),
					Roll:  float32(curR),
					Yaw:   float32(curY),
				})
			}
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

		a.ResetAHRS()
		a.ResetGyroFilter()

		a.hasClient.Store(true)
		a.clientAddr = remoteAddr
		if a.connectedAt.IsZero() {
			a.connectedAt = time.Now()
		}
		a.emitStateChange()
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "device:connected", true)
		}
		a.broadcastLiveDebugJSON(map[string]any{
			"type":      "device_status",
			"connected": true,
		})
	}

	srv.OnClientDisconnect = func(remoteAddr string) {
		disconnectMu.Lock()
		defer disconnectMu.Unlock()

		a.ResetGyroFilter()

		_, clients, _ := srv.PacketStats()
		if clients > 0 {
			return
		}

		// Emit immediate disconnection event for active calibration/monitoring
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "device:connection-lost", true)
		}

		if disconnectTimer != nil {
			disconnectTimer.Stop()
		}
		disconnectTimer = time.AfterFunc(600*time.Millisecond, func() {
			_, c, _ := srv.PacketStats()
			if c <= 0 {
				a.hasClient.Store(false)
				a.connectedAt = time.Time{}
				a.deviceName.Store("Controller")
				a.emitStateChange()
				if a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "device:disconnected", true)
				}
				a.broadcastLiveDebugJSON(map[string]any{
					"type":      "device_status",
					"connected": false,
				})
			}
		})
	}

	srv.OnClientVisibility = func(visible bool) {
		if visible {
			a.ahrsNeedConverge.Store(true)
			a.ResetGyroFilter()
		}
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "device:visibility", visible)
		}
	}

	srv.OnClientDevice = func(device string) {
		if device == "" {
			return
		}
		a.deviceName.Store(device)

		// Check if active profile already matches this device
		a.profilesMu.RLock()
		curSlot := a.activeSlot
		var curMatches bool
		if curSlot >= 0 && curSlot < 6 {
			curDev := strings.ToLower(strings.TrimSpace(a.profiles[curSlot].Device))
			curName := strings.ToLower(strings.TrimSpace(a.profiles[curSlot].Name))
			inDev := strings.ToLower(strings.TrimSpace(device))
			if curDev == inDev || strings.Contains(curDev, inDev) || strings.Contains(inDev, curDev) || strings.Contains(curName, inDev) {
				curMatches = true
			}
		}

		targetSlot := -1
		if !curMatches {
			for i, p := range a.profiles {
				pDev := strings.ToLower(strings.TrimSpace(p.Device))
				pName := strings.ToLower(strings.TrimSpace(p.Name))
				inDev := strings.ToLower(strings.TrimSpace(device))
				if p.Name != "" && (pDev == inDev || strings.Contains(pDev, inDev) || strings.Contains(inDev, pDev) || strings.Contains(pName, inDev)) {
					targetSlot = i
					break
				}
			}
		}
		a.profilesMu.RUnlock()

		if targetSlot >= 0 {
			a.SetActiveProfile(targetSlot)
		} else {
			a.emitStateChange()
		}
	}

	srv.OnClientPause = func(isPaused bool) {
		if a.isPaused.Load() != isPaused {
			a.isPaused.Store(isPaused)
			a.emitStateChange()
		}
	}

	srv.GetIsPaused = a.isPaused.Load

	// LiveDebug standalone 3D window routes and WebSocket streamer
	subFS, err := fs.Sub(assets, "frontend/src")
	if err == nil {
		liveUpgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}

		registerLiveDebug := func(mux *http.ServeMux) {
			mux.Handle("/assets/", http.FileServer(http.FS(subFS)))
			mux.Handle("/livedebug/assets/", http.StripPrefix("/livedebug", http.FileServer(http.FS(subFS))))
			mux.HandleFunc("/livedebug", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				data, err := fs.ReadFile(subFS, "livedebug.html")
				if err != nil {
					http.Error(w, "Not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.WriteHeader(http.StatusOK)
				w.Write(data)
			})
			mux.HandleFunc("/livedebug/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.URL.Path == "/livedebug/" {
					data, err := fs.ReadFile(subFS, "livedebug.html")
					if err != nil {
						http.Error(w, "Not found", http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
					w.WriteHeader(http.StatusOK)
					w.Write(data)
					return
				}
				http.NotFound(w, r)
			})
			mux.HandleFunc("/livedebug/ping", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"status":"ok"}`))
			})
			mux.HandleFunc("/livedebug/recenter", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.Method == http.MethodPost {
					a.ResetAHRS()
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"status":"ok"}`))
			})
			mux.HandleFunc("/livedebug/theme", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				val := r.URL.Query().Get("value")
				if val != "" {
					a.SetTheme(val)
				}
				a.themeMu.RLock()
				curT := a.currentTheme
				a.themeMu.RUnlock()
				if curT == "" {
					curT = "dark"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"theme": curT})
			})
			mux.HandleFunc("/livedebug/lang", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				val := r.URL.Query().Get("value")
				if val != "" {
					a.SetLang(val)
				}
				a.themeMu.RLock()
				curL := a.currentLang
				a.themeMu.RUnlock()
				if curL == "" {
					curL = "ru"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"lang": curL})
			})
			mux.HandleFunc("/livedebug/status", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"device_connected": a.hasClient.Load(),
				})
			})
			mux.HandleFunc("/livedebug/show-in-folder", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				filePath := r.URL.Query().Get("path")
				if filePath != "" {
					go func() {
						_ = exec.Command("explorer.exe", "/select,", filepath.Clean(filePath)).Start()
					}()
				}
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("/livedebug/ws", func(w http.ResponseWriter, r *http.Request) {
				conn, err := liveUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}

				// Send immediate initial theme, language, and device connection sync frame
				a.themeMu.RLock()
				curT := a.currentTheme
				curL := a.currentLang
				a.themeMu.RUnlock()
				if curT == "" {
					curT = "dark"
				}
				if curL == "" {
					curL = "ru"
				}
				syncBytes, _ := json.Marshal(map[string]any{
					"type":             "sync",
					"theme":            curT,
					"lang":             curL,
					"device_connected": a.hasClient.Load(),
				})

				a.liveDebugMu.Lock()
				if a.liveDebugClients == nil {
					a.liveDebugClients = make(map[*websocket.Conn]struct{})
				}
				_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_ = conn.WriteMessage(websocket.TextMessage, syncBytes)
				a.liveDebugClients[conn] = struct{}{}
				a.liveDebugMu.Unlock()

				go func(c *websocket.Conn) {
					defer func() {
						a.liveDebugMu.Lock()
						delete(a.liveDebugClients, c)
						a.liveDebugMu.Unlock()
						c.Close()
					}()

					for {
						_, msgBytes, err := c.ReadMessage()
						if err != nil {
							break
						}
						var req map[string]string
						if json.Unmarshal(msgBytes, &req) == nil {
							if req["action"] == "recenter" {
								a.ResetAHRS()
							}
						}
					}
				}(conn)
			})
		}

		registerLiveDebug(srv.HTTPMux)
		registerLiveDebug(srv.HTTPSMux)
	}

	if err := srv.Start(); err != nil {
		fmt.Printf("[-] Server start error: %v\n", err)
	}
	a.srv = srv

	// Orientation heartbeat (15 Hz = 66ms) for smooth main GUI telemetry
	go func() {
		ticker := time.NewTicker(66 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if a.hasClient.Load() {
				a.emitStateChange()
				// Only send fallback heartbeat to 3D window if no live motion packet arrived recently (> 150ms)
				if time.Now().UnixMilli()-a.lastMotionRecvTs.Load() > 150 {
					q0 := float32(math.Float64frombits(a.curAhrsQ0.Load()))
					q1 := float32(math.Float64frombits(a.curAhrsQ1.Load()))
					q2 := float32(math.Float64frombits(a.curAhrsQ2.Load()))
					q3 := float32(math.Float64frombits(a.curAhrsQ3.Load()))
					a.liveDebugMu.RLock()
					numDebug := len(a.liveDebugClients)
					a.liveDebugMu.RUnlock()
					if numDebug > 0 {
						var inHz float64
						if a.srv != nil {
							_, _, inHz = a.srv.PacketStats()
						}
						a.broadcastLiveDebug(q0, q1, q2, q3, liveDebugMsg{
							InHz: inHz,
						})
					}
				}
			}
		}
	}()

	// Process resource monitor (CPU / RAM)
	a.stopResmon = resmon.RunLoop(1500*time.Millisecond, func(s resmon.Stats) {
		ramPercent := 0.0
		if s.TotalRAMBytes > 0 {
			ramPercent = (float64(s.RAMBytes) / float64(s.TotalRAMBytes)) * 100.0
		}
		statsPayload := map[string]any{
			"cpuPercent": s.CPUPercent,
			"ramMb":      float64(s.RAMBytes) / (1024 * 1024),
			"totalRamMb": float64(s.TotalRAMBytes) / (1024 * 1024),
			"ramPercent": ramPercent,
		}
		a.lastResStats.Store(&statsPayload)
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "resource-stats", statsPayload)
		}
	})

	// Background network IP watcher (detects DHCP updates, USB tethering, or Wi-Fi reconnects)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			lanIPs := pairing.GetLocalIPv4s()
			if len(lanIPs) == 0 {
				continue
			}
			newPrimary := pairing.GetPrimaryIP(lanIPs)
			if newPrimary != "" && newPrimary != a.primaryIP {
				a.primaryIP = newPrimary
				a.rebuildURLsAndQRCodes()
				if a.caMgr != nil {
					a.caMgr.AddHostIPs(lanIPs)
				}
				a.emitStateChange()
				if a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "network:ip-changed", newPrimary)
				}
				a.logEvent("INFO", "Network adapter change detected: primary IP updated to %s", newPrimary)
			}
		}
	}()
}

// shutdown is called when the Wails application terminates
func (a *App) shutdown(ctx context.Context) {
	if a.stopResmon != nil {
		a.stopResmon()
		a.stopResmon = nil
	}

	// Terminate child Live Debug process if running
	a.liveDebugCmdMu.Lock()
	if a.liveDebugCmd != nil && a.liveDebugCmd.Process != nil {
		_ = a.liveDebugCmd.Process.Kill()
		a.liveDebugCmd = nil
	}
	a.liveDebugCmdMu.Unlock()

	if a.srv != nil {
		a.srv.Stop()
	}
	if a.dsuSrv != nil {
		a.dsuSrv.Stop()
	}
	a.liveDebugMu.Lock()
	for conn := range a.liveDebugClients {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"shutdown"}`))
		conn.Close()
	}
	a.liveDebugClients = make(map[*websocket.Conn]struct{})
	a.liveDebugMu.Unlock()
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

	profilesList := make([]Profile, 6)
	for i := 0; i < 6; i++ {
		profilesList[i] = profilesCopy[i]
		profilesList[i].Active = (i == activeSlot)
	}

	// Determine which matrix is currently effective: preview during wizard, or saved active matrix.
	a.previewMu.RLock()
	usePrev := a.usePreview
	effectiveMat := a.previewMatrix
	a.previewMu.RUnlock()
	if !usePrev {
		a.matrixMu.RLock()
		effectiveMat = a.activeMatrix
		a.matrixMu.RUnlock()
	}

	devName := "Controller"
	if v := a.deviceName.Load(); v != nil {
		if s, ok := v.(string); ok && s != "" {
			devName = s
		}
	}

	return AppState{
		Status:        status,
		IsPaused:      a.isPaused.Load(),
		DeviceName:    devName,
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
		Qx:            math.Float64frombits(a.curQx.Load()),
		Qy:            math.Float64frombits(a.curQy.Load()),
		Qz:            math.Float64frombits(a.curQz.Load()),
		Qw:            math.Float64frombits(a.curQw.Load()),
		Profiles:      profilesList,
		ActiveSlot:    activeSlot,
		ActiveMatrix:  effectiveMat,
		AhrsQ0:        math.Float64frombits(a.curAhrsQ0.Load()),
		AhrsQ1:        math.Float64frombits(a.curAhrsQ1.Load()),
		AhrsQ2:        math.Float64frombits(a.curAhrsQ2.Load()),
		AhrsQ3:        math.Float64frombits(a.curAhrsQ3.Load()),
		FirstLaunch:   !a.firstLaunchDone,
		HideAuthor:    a.hideAuthor,
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

// GetProfiles returns current 6 profile slots
func (a *App) GetProfiles() []Profile {
	a.profilesMu.RLock()
	defer a.profilesMu.RUnlock()

	result := make([]Profile, 6)
	for i := 0; i < 6; i++ {
		result[i] = a.profiles[i]
		result[i].Active = (i == a.activeSlot)
	}
	return result
}

// SaveProfile overwrites a profile slot (slot 0-5) with the given name, device, icon, and matrix.
// The matrix must be a valid signed-permutation matrix with determinant -1.
func (a *App) SaveProfile(slot int, name string, device string, icon string, matrix [3][3]float64) string {
	if slot < 0 || slot > 5 {
		return "invalid slot"
	}
	if device == "" {
		device = "Unknown"
	}
	if icon == "" {
		icon = "default"
	}

	// Validate matrix: determinant must be -1 (Cemuhook DSU left-handed parity convention)
	det := matrix[0][0]*(matrix[1][1]*matrix[2][2]-matrix[1][2]*matrix[2][1]) -
		matrix[0][1]*(matrix[1][0]*matrix[2][2]-matrix[1][2]*matrix[2][0]) +
		matrix[0][2]*(matrix[1][0]*matrix[2][1]-matrix[1][1]*matrix[2][0])

	if math.Abs(det+1.0) > 0.05 {
		return fmt.Sprintf("invalid matrix: determinant is %.4f, must be -1.0", det)
	}

	a.biasMu.RLock()
	curBias := a.gyroBias
	a.biasMu.RUnlock()
	curGrav := a.calGravity

	a.profilesMu.Lock()
	a.profiles[slot] = Profile{
		Slot:       slot,
		Name:       name,
		Device:     device,
		Icon:       icon,
		Matrix:     matrix,
		Active:     (slot == a.activeSlot),
		GyroBias:   curBias,
		CalGravity: curGrav,
	}
	a.profilesMu.Unlock()

	// If this slot is currently active, push the new matrix into the live path immediately.
	if slot == a.activeSlot {
		a.matrixMu.Lock()
		a.activeMatrix = matrix
		a.matrixMu.Unlock()
		a.ResetAHRS()
	}

	a.saveProfiles()
	a.emitStateChange()
	return "ok"
}

// SetActiveProfile selects the profile at the given slot (-1 = identity/none)
func (a *App) SetActiveProfile(slot int) string {
	if slot < -1 || slot > 5 {
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

	// Update active matrix, bias, and gravity
	a.matrixMu.Lock()
	if slot >= 0 {
		a.profilesMu.RLock()
		targetProf := a.profiles[slot]
		a.profilesMu.RUnlock()

		a.activeMatrix = targetProf.Matrix

		if targetProf.GyroBias != [3]float64{0, 0, 0} {
			a.biasMu.Lock()
			a.gyroBias = targetProf.GyroBias
			a.biasMu.Unlock()
		}
		if math.Sqrt(targetProf.CalGravity[0]*targetProf.CalGravity[0]+targetProf.CalGravity[1]*targetProf.CalGravity[1]+targetProf.CalGravity[2]*targetProf.CalGravity[2]) > 0.3 {
			a.calGravity = targetProf.CalGravity
		}
	} else {
		a.activeMatrix = defaultMatrix3x3()
	}
	a.matrixMu.Unlock()

	a.ResetAHRS()

	a.saveProfiles()
	a.emitStateChange()
	return "ok"
}

// StartCapture begins buffering raw 60 Hz gyro and accel frames for calibration

// PreviewMatrix temporarily overrides the active calibration matrix for the 3D viewport.
// Call this when the calibration wizard shows the confirm or manual screen so the user
// can see exactly how the candidate matrix behaves before saving.
func (a *App) PreviewMatrix(matrix [3][3]float64) {
	a.previewMu.Lock()
	a.previewMatrix = matrix
	a.usePreview = true
	a.previewMu.Unlock()
	a.ResetAHRS()
}

// ClearPreview removes the temporary preview matrix and reverts to the saved activeMatrix.
// Call this when the calibration wizard is closed or cancelled.
func (a *App) ClearPreview() {
	a.previewMu.Lock()
	a.usePreview = false
	a.previewMu.Unlock()
	a.ResetAHRS()
}

// ResetAHRS zeroes the 3D orientation filter
func (a *App) ResetAHRS() {
	if a.ahrs != nil {
		a.ahrs.Reset()
		a.ahrsNeedConverge.Store(true)
		a.broadcastLiveDebug(1, 0, 0, 0)
	}
}

// QuickCalibrate performs a rapid 1-second calibration of zero-bias and horizon gravity.
// Samples resting data, updates gyro bias & calGravity, recenters AHRS to (0, 0, 0),
// and updates the active profile on disk.
func (a *App) QuickCalibrate() map[string]any {
	if !a.hasClient.Load() {
		return map[string]any{
			"success": false,
			"error":   a.getI18nMsg("status.device_offline"),
		}
	}

	// Capture stationary samples over 800ms
	a.isCapturing.Store(true)
	a.captureMu.Lock()
	a.captureBuffer = nil
	a.captureMu.Unlock()

	time.Sleep(800 * time.Millisecond)

	a.isCapturing.Store(false)
	a.captureMu.Lock()
	samples := a.captureBuffer
	a.captureBuffer = nil
	a.captureMu.Unlock()

	if len(samples) < 15 {
		return map[string]any{
			"success": false,
			"error":   a.getI18nMsg("calibration.error_too_few_samples"),
		}
	}

	var sumX, sumY, sumZ float64
	var sumAx, sumAy, sumAz float64
	var peakSpeed float64
	for _, s := range samples {
		sumX += s.rot[0]
		sumY += s.rot[1]
		sumZ += s.rot[2]
		sumAx += s.acc[0]
		sumAy += s.acc[1]
		sumAz += s.acc[2]
		spd := math.Sqrt(s.rot[0]*s.rot[0] + s.rot[1]*s.rot[1] + s.rot[2]*s.rot[2])
		if spd > peakSpeed {
			peakSpeed = spd
		}
	}
	n := float64(len(samples))
	bX, bY, bZ := sumX/n, sumY/n, sumZ/n

	// Check if phone was moved excessively
	var varSum float64
	for _, s := range samples {
		dx := s.rot[0] - bX
		dy := s.rot[1] - bY
		dz := s.rot[2] - bZ
		varSum += dx*dx + dy*dy + dz*dz
	}
	stdDev := math.Sqrt(varSum / n)
	if stdDev > 2.5 || peakSpeed > 8.0 {
		return map[string]any{
			"success": false,
			"error":   a.getI18nMsg("calibration.error_moved_during_rest"),
		}
	}

	// Apply new zero-bias
	a.biasMu.Lock()
	a.gyroBias = [3]float64{bX, bY, bZ}
	a.biasMu.Unlock()

	// Apply new calGravity
	gX, gY, gZ := sumAx/n, sumAy/n, sumAz/n
	gNorm := math.Sqrt(gX*gX + gY*gY + gZ*gZ)
	if gNorm > 0.4 {
		a.calGravity = [3]float64{gX / gNorm, gY / gNorm, gZ / gNorm}
	}

	// If an active profile is selected, persist updated bias and gravity
	if a.activeSlot >= 0 && a.activeSlot <= 5 {
		a.profilesMu.Lock()
		a.profiles[a.activeSlot].GyroBias = a.gyroBias
		a.profiles[a.activeSlot].CalGravity = a.calGravity
		a.profilesMu.Unlock()
		a.saveProfiles()
	}

	// Reset AHRS to 0 pitch, roll, and yaw, and trigger gravity convergence
	a.ResetAHRS()
	a.emitStateChange()

	return map[string]any{
		"success": true,
	}
}

type liveDebugMsg struct {
	DeviceConnected bool    `json:"device_connected"`
	Q0              float32 `json:"q0"`
	Q1              float32 `json:"q1"`
	Q2              float32 `json:"q2"`
	Q3              float32 `json:"q3"`
	Seq             uint64  `json:"seq,omitempty"`
	Timestamp       uint32  `json:"ts,omitempty"`
	RecvTs          int64   `json:"recv_ts,omitempty"`
	SendTs          int64   `json:"send_ts,omitempty"`
	RawGx           float32 `json:"raw_gx,omitempty"`
	RawGy           float32 `json:"raw_gy,omitempty"`
	RawGz           float32 `json:"raw_gz,omitempty"`
	RawAx           float32 `json:"raw_ax,omitempty"`
	RawAy           float32 `json:"raw_ay,omitempty"`
	RawAz           float32 `json:"raw_az,omitempty"`
	OutGx           float32 `json:"out_gx,omitempty"`
	OutGy           float32 `json:"out_gy,omitempty"`
	OutGz           float32 `json:"out_gz,omitempty"`
	OutAx           float32 `json:"out_ax,omitempty"`
	OutAy           float32 `json:"out_ay,omitempty"`
	OutAz           float32 `json:"out_az,omitempty"`
	StickLx         float32 `json:"stick_lx,omitempty"`
	StickLy         float32 `json:"stick_ly,omitempty"`
	InHz            float64 `json:"in_hz,omitempty"`
	OutHz           float64 `json:"out_hz,omitempty"`
	PipeMs          float64 `json:"pipe_ms,omitempty"`
	DsuClients      int     `json:"dsu_clients,omitempty"`
}

func (a *App) broadcastLiveDebug(q0, q1, q2, q3 float32, extras ...liveDebugMsg) {
	a.liveDebugMu.RLock()
	clientCount := len(a.liveDebugClients)
	a.liveDebugMu.RUnlock()
	if clientCount == 0 {
		return
	}

	msg := liveDebugMsg{
		DeviceConnected: a.hasClient.Load(),
		Q0:              q0,
		Q1:              q1,
		Q2:              q2,
		Q3:              q3,
	}
	if len(extras) > 0 {
		e := extras[0]
		msg.Seq = e.Seq
		msg.Timestamp = e.Timestamp
		msg.RecvTs = e.RecvTs
		msg.SendTs = e.SendTs
		msg.RawGx = e.RawGx
		msg.RawGy = e.RawGy
		msg.RawGz = e.RawGz
		msg.RawAx = e.RawAx
		msg.RawAy = e.RawAy
		msg.RawAz = e.RawAz
		msg.OutGx = e.OutGx
		msg.OutGy = e.OutGy
		msg.OutGz = e.OutGz
		msg.OutAx = e.OutAx
		msg.OutAy = e.OutAy
		msg.OutAz = e.OutAz
		msg.StickLx = e.StickLx
		msg.StickLy = e.StickLy
		msg.InHz = e.InHz
		msg.OutHz = e.OutHz
		msg.PipeMs = e.PipeMs
		msg.DsuClients = e.DsuClients
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	a.liveDebugMu.Lock()
	defer a.liveDebugMu.Unlock()
	for conn := range a.liveDebugClients {
		_ = conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			delete(a.liveDebugClients, conn)
		}
	}
}

// SetTheme updates theme on backend, broadcasts to Live Debug window, and emits event to main window.
func (a *App) SetTheme(theme string) {
	if theme == "" {
		return
	}
	a.themeMu.Lock()
	a.currentTheme = theme
	a.themeMu.Unlock()

	a.broadcastLiveDebugJSON(map[string]string{
		"type":  "theme",
		"theme": theme,
	})

	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "theme-sync", theme)
		if theme == "dark" {
			wailsRuntime.WindowSetDarkTheme(a.ctx)
		} else if theme == "light" {
			wailsRuntime.WindowSetLightTheme(a.ctx)
		}
	}
	a.saveSettings()
}

// SetLang updates language on backend, broadcasts to Live Debug window, and emits event to main window.
func (a *App) SetLang(lang string) {
	if lang == "" {
		return
	}
	a.themeMu.Lock()
	a.currentLang = lang
	a.themeMu.Unlock()

	a.broadcastLiveDebugJSON(map[string]string{
		"type": "lang",
		"lang": lang,
	})

	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "lang-sync", lang)
	}
	a.saveSettings()
}

// GetTheme returns the current synchronized theme.
func (a *App) GetTheme() string {
	a.themeMu.RLock()
	defer a.themeMu.RUnlock()
	if a.currentTheme == "" {
		return "dark"
	}
	return a.currentTheme
}

// GetLang returns the current synchronized language.
func (a *App) GetLang() string {
	a.themeMu.RLock()
	defer a.themeMu.RUnlock()
	if a.currentLang == "" {
		return "ru"
	}
	return a.currentLang
}

// IsFirstLaunch returns true if the application has not finished its first-launch onboarding.
func (a *App) IsFirstLaunch() bool {
	a.themeMu.RLock()
	defer a.themeMu.RUnlock()
	return !a.firstLaunchDone
}

// MarkFirstLaunchDone marks that the welcome onboarding has been viewed and persists to settings.
func (a *App) MarkFirstLaunchDone() {
	a.themeMu.Lock()
	a.firstLaunchDone = true
	a.themeMu.Unlock()
	a.saveSettings()
}

// GetHideAuthor returns whether the discreet author attribution should be hidden.
func (a *App) GetHideAuthor() bool {
	a.themeMu.RLock()
	defer a.themeMu.RUnlock()
	return a.hideAuthor
}

// SetHideAuthor configures author attribution visibility and persists to settings.
func (a *App) SetHideAuthor(hide bool) {
	a.themeMu.Lock()
	a.hideAuthor = hide
	a.themeMu.Unlock()
	a.saveSettings()
}

// SetWindowTheme sets the native window title bar theme.
func (a *App) SetWindowTheme(theme string) {
	if a.ctx == nil {
		return
	}
	if theme == "dark" {
		wailsRuntime.WindowSetDarkTheme(a.ctx)
	} else if theme == "light" {
		wailsRuntime.WindowSetLightTheme(a.ctx)
	}
}

func (a *App) rebuildURLsAndQRCodes() {
	if a.primaryIP == "" {
		return
	}
	hPort := a.httpPort
	if hPort == 0 {
		hPort = HTTPPort
	}
	hsPort := a.httpsPort
	if hsPort == 0 {
		hsPort = HTTPSPort
	}

	setupURL := fmt.Sprintf("http://%s:%d/ca.mobileconfig", a.primaryIP, hPort)
	appURL := fmt.Sprintf("https://%s:%d/", a.primaryIP, hsPort)

	qrBytes, err := pairing.GenerateQRPNG(appURL, 240)
	if err == nil {
		a.qrCodePNG = "data:image/png;base64," + base64.StdEncoding.EncodeToString(qrBytes)
	}

	setupQRBytes, err := pairing.GenerateQRPNG(setupURL, 240)
	if err == nil {
		a.setupQRPNG = "data:image/png;base64," + base64.StdEncoding.EncodeToString(setupQRBytes)
	}

	a.setupURL = setupURL
	a.gamepadURL = appURL
}

// GetAppSettings returns all current application settings
func (a *App) GetAppSettings() AppSettings {
	a.themeMu.RLock()
	theme := a.currentTheme
	lang := a.currentLang
	firstLaunch := a.firstLaunchDone
	hideAuthor := a.hideAuthor
	a.themeMu.RUnlock()

	a.profilesMu.RLock()
	slot := a.activeSlot
	a.profilesMu.RUnlock()

	dsuP := a.dsuPort
	if dsuP == 0 {
		dsuP = 26760
	}
	httpP := a.httpPort
	if httpP == 0 {
		httpP = HTTPPort
	}
	httpsP := a.httpsPort
	if httpsP == 0 {
		httpsP = HTTPSPort
	}

	deadzone := math.Float64frombits(a.gyroDeadzoneBits.Load())

	soundM := a.soundMode
	if soundM == "" {
		soundM = "cute"
	}

	smoothing := math.Float64frombits(a.gyroSmoothingBits.Load())
	deadband := math.Float64frombits(a.gyroDeadbandBits.Load())
	sensitivity := math.Float64frombits(a.gyroSensitivityBits.Load())
	if sensitivity <= 0 {
		sensitivity = 1.00
	}

	return AppSettings{
		Theme:           theme,
		Lang:            lang,
		ActiveSlot:      slot,
		FirstLaunchDone: firstLaunch,
		HideAuthor:      hideAuthor,
		DSUPort:         dsuP,
		HTTPPort:        httpP,
		HTTPSPort:       httpsP,
		GyroDeadzone:    deadzone,
		StillnessHint:   a.stillnessHint.Load(),
		DisconnectAlert: a.disconnectAlert.Load(),
		SoundMode:       soundM,
		GyroSmoothing:   smoothing,
		GyroDeadband:    deadband,
		GyroSensitivity: sensitivity,
	}
}

// SaveAppSettings validates, applies and persists settings
func (a *App) SaveAppSettings(s AppSettings) (map[string]any, error) {
	if s.DSUPort < 1024 || s.DSUPort > 65535 {
		return nil, fmt.Errorf("DSU port must be between 1024 and 65535")
	}
	if s.HTTPPort < 1024 || s.HTTPPort > 65535 {
		return nil, fmt.Errorf("HTTP port must be between 1024 and 65535")
	}
	if s.HTTPSPort < 1024 || s.HTTPSPort > 65535 {
		return nil, fmt.Errorf("HTTPS port must be between 1024 and 65535")
	}
	if s.HTTPPort == s.HTTPSPort || s.HTTPPort == s.DSUPort || s.HTTPSPort == s.DSUPort {
		return nil, fmt.Errorf("DSU, HTTP and HTTPS ports must be different")
	}

	dsuRestarted := false
	if s.DSUPort != a.dsuPort && a.dsuSrv != nil {
		a.dsuSrv.Stop()
		newDsu := dsu.NewServer(s.DSUPort)
		if err := newDsu.Start(); err != nil {
			oldDsu := dsu.NewServer(a.dsuPort)
			_ = oldDsu.Start()
			a.dsuSrv = oldDsu
			return nil, fmt.Errorf("failed to bind DSU port %d: %w", s.DSUPort, err)
		}
		a.dsuSrv = newDsu
		a.dsuPort = s.DSUPort
		dsuRestarted = true
	} else if a.dsuPort == 0 {
		a.dsuPort = s.DSUPort
	}

	if s.HTTPPort != a.httpPort || s.HTTPSPort != a.httpsPort {
		a.httpPort = s.HTTPPort
		a.httpsPort = s.HTTPSPort
		a.rebuildURLsAndQRCodes()
	}

	if s.GyroDeadzone >= 0 {
		a.gyroDeadzoneBits.Store(math.Float64bits(s.GyroDeadzone))
	}
	if s.GyroSmoothing >= 0 && s.GyroSmoothing <= 1.0 {
		a.gyroSmoothingBits.Store(math.Float64bits(s.GyroSmoothing))
	}
	if s.GyroDeadband >= 0 && s.GyroDeadband <= 1.0 {
		a.gyroDeadbandBits.Store(math.Float64bits(s.GyroDeadband))
	}
	if s.GyroSensitivity >= 0.25 && s.GyroSensitivity <= 3.0 {
		a.gyroSensitivityBits.Store(math.Float64bits(s.GyroSensitivity))
	}
	a.updateFilterParams()

	a.stillnessHint.Store(s.StillnessHint)
	a.disconnectAlert.Store(s.DisconnectAlert)
	if s.SoundMode != "" {
		a.soundMode = s.SoundMode
	}

	a.themeMu.Lock()
	if s.Theme == "dark" || s.Theme == "light" {
		a.currentTheme = s.Theme
	}
	if s.Lang == "ru" || s.Lang == "en" {
		a.currentLang = s.Lang
	}
	a.themeMu.Unlock()

	a.saveSettings()
	a.emitStateChange()

	return map[string]any{
		"success":      true,
		"dsuRestarted": dsuRestarted,
	}, nil
}

// SetTuningActive toggles 60 Hz real-time telemetry streaming for the settings test bench
func (a *App) SetTuningActive(active bool) {
	a.tuningActive.Store(active)
}

// SetTuningFilterParams dynamically updates filter parameters for live bench previewing without saving
func (a *App) SetTuningFilterParams(smoothing, deadband, sensitivity float64) {
	if smoothing >= 0 && smoothing <= 1.0 {
		a.gyroSmoothingBits.Store(math.Float64bits(smoothing))
	}
	if deadband >= 0 && deadband <= 1.0 {
		a.gyroDeadbandBits.Store(math.Float64bits(deadband))
	}
	if sensitivity >= 0.25 && sensitivity <= 3.0 {
		a.gyroSensitivityBits.Store(math.Float64bits(sensitivity))
	}
	a.updateFilterParams()
	a.saveSettings()
}

// ResetGyroFilter zeroes filter history
func (a *App) ResetGyroFilter() {
	if a.gyroFilter != nil {
		a.gyroFilter.Reset()
	}
}

// PlaySystemSound plays a native Windows sound for hardware connect/disconnect
func (a *App) PlaySystemSound(soundType string) {
	if runtime.GOOS != "windows" {
		return
	}
	mod := syscall.NewLazyDLL("winmm.dll")
	proc := mod.NewProc("PlaySoundW")

	var soundName string
	var fallbackPath string
	switch soundType {
	case "connect":
		soundName = "DeviceConnect"
		fallbackPath = `C:\Windows\Media\Windows Hardware Insert.wav`
	case "disconnect":
		soundName = "DeviceDisconnect"
		fallbackPath = `C:\Windows\Media\Windows Hardware Remove.wav`
	default:
		return
	}

	ptr, err := syscall.UTF16PtrFromString(soundName)
	if err == nil {
		// SND_ASYNC (0x0001) | SND_ALIAS (0x00010000) | SND_NODEFAULT (0x0002)
		ret, _, _ := proc.Call(uintptr(unsafe.Pointer(ptr)), 0, uintptr(0x0001|0x00010000|0x0002))
		if ret != 0 {
			return
		}
	}

	if _, err := os.Stat(fallbackPath); err == nil {
		if fptr, ferr := syscall.UTF16PtrFromString(fallbackPath); ferr == nil {
			// SND_ASYNC (0x0001) | SND_FILENAME (0x00020000)
			_, _, _ = proc.Call(uintptr(unsafe.Pointer(fptr)), 0, uintptr(0x0001|0x00020000))
		}
	}
}

func (a *App) broadcastLiveDebugJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	a.liveDebugMu.Lock()
	defer a.liveDebugMu.Unlock()
	for conn := range a.liveDebugClients {
		_ = conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			delete(a.liveDebugClients, conn)
		}
	}
}

var (
	modUser32                    = syscall.NewLazyDLL("user32.dll")
	procAllowSetForegroundWindow = modUser32.NewProc("AllowSetForegroundWindow")
)

// OpenLiveDebugWindow opens the standalone 3D Live Debug desktop .exe window.
func (a *App) OpenLiveDebugWindow() {
	exePath, err := os.Executable()
	if err == nil {
		// ASFW_ANY (-1 = 0xFFFFFFFF) grants the spawned child process permission to activate into foreground
		_, _, _ = procAllowSetForegroundWindow.Call(uintptr(0xFFFFFFFF))
		cmd := exec.Command(exePath, "--livedebug")
		if err := cmd.Start(); err == nil {
			a.liveDebugCmdMu.Lock()
			a.liveDebugCmd = cmd
			a.liveDebugCmdMu.Unlock()
			return
		}
	}
	// Fallback to browser if process execution fails
	url := fmt.Sprintf("http://127.0.0.1:%d/livedebug", HTTPPort)
	if a.ctx != nil {
		wailsRuntime.BrowserOpenURL(a.ctx, url)
	}
}

func (a *App) StartCapture() {
	a.captureMu.Lock()
	a.captureBuffer = make([]captureSample, 0, 300)
	a.captureMu.Unlock()
	a.isCapturing.Store(true)
}

// writeDebugCSV appends a capture session block to gyro_debug_capture.csv in profilesDir.
// Each session is separated by a blank line and starts with a header and a metadata row.
func (a *App) writeDebugCSV(step int, samples []captureSample, result CaptureResult) {
	path := filepath.Join(a.profilesDir, "gyro_debug_capture.csv")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(f, "\n# Session: %s  step=%d  samples=%d  success=%v  axisIdx=%d  axisName=%s  confidence=%.3f  peakSpeed=%.2f\n",
		ts, step, len(samples), result.Success, result.AxisIdx, result.AxisName, result.Confidence, result.PeakSpeed)
	fmt.Fprintln(f, "idx,rotX,rotY,rotZ,accX,accY,accZ,speed")
	for i, s := range samples {
		speed := math.Sqrt(s.rot[0]*s.rot[0] + s.rot[1]*s.rot[1] + s.rot[2]*s.rot[2])
		fmt.Fprintf(f, "%d,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f\n",
			i, s.rot[0], s.rot[1], s.rot[2],
			s.acc[0], s.acc[1], s.acc[2], speed)
	}

	a.calLogMu.Lock()
	if a.calStepLogs == nil {
		a.calStepLogs = make(map[int]StepCaptureLog)
	}
	samplesCopy := make([]captureSample, len(samples))
	copy(samplesCopy, samples)
	a.calStepLogs[step] = StepCaptureLog{
		Step:      step,
		Samples:   samplesCopy,
		Result:    result,
		Timestamp: time.Now(),
	}
	a.calLogMu.Unlock()
}

// getI18nMsg returns localized message or fallback
func (a *App) getI18nMsg(key string) string {
	if a.i18nMgr != nil {
		a.themeMu.RLock()
		lang := a.currentLang
		a.themeMu.RUnlock()
		if lang == "" {
			lang = a.i18nMgr.BaseLanguage()
		}
		return a.i18nMgr.Get(lang, key)
	}
	return key
}

// StopCapture stops buffering and analyzes the captured gyro frames for the given gesture step.
// step 0: Stillness / "Покой" — phone motionless on desk (~1.5s) to calibrate zero-bias
// step 1: Pitch     / "Кивни" — tilt phone forward/back (nod gesture, target RotX < 0)
// step 2: Roll      / "Самолётик" — bank phone left/right (wing gesture, target RotZ > 0)
// Yaw is computed automatically in ValidateCalibration with det = -1.0.
func (a *App) StopCapture(step int) CaptureResult {
	a.isCapturing.Store(false)
	a.captureMu.Lock()
	samples := a.captureBuffer
	a.captureBuffer = nil
	a.captureMu.Unlock()

	if len(samples) < 5 {
		res := CaptureResult{
			Success:   false,
			ErrorCode: "error_too_few_samples",
			ErrorMsg:  a.getI18nMsg("calibration.error_too_few_samples"),
		}
		a.writeDebugCSV(step, samples, res)
		return res
	}

	// ── Step 0: Stillness / Bias Calibration (§2 of spec) ──
	if step == 0 {
		if len(samples) < 20 {
			res := CaptureResult{
				Success:   false,
				ErrorCode: "error_too_few_samples",
				ErrorMsg:  a.getI18nMsg("calibration.error_too_few_samples"),
			}
			a.writeDebugCSV(step, samples, res)
			return res
		}

		var sumX, sumY, sumZ float64
		var peakSpeed float64
		for _, s := range samples {
			sumX += s.rot[0]
			sumY += s.rot[1]
			sumZ += s.rot[2]
			spd := math.Sqrt(s.rot[0]*s.rot[0] + s.rot[1]*s.rot[1] + s.rot[2]*s.rot[2])
			if spd > peakSpeed {
				peakSpeed = spd
			}
		}
		n := float64(len(samples))
		bX := sumX / n
		bY := sumY / n
		bZ := sumZ / n

		// Compute variance to verify phone was not shaken or moved during rest
		var varSum float64
		for _, s := range samples {
			dx := s.rot[0] - bX
			dy := s.rot[1] - bY
			dz := s.rot[2] - bZ
			varSum += dx*dx + dy*dy + dz*dz
		}
		stdDev := math.Sqrt(varSum / n)

		if stdDev > 2.5 || peakSpeed > 6.0 {
			res := CaptureResult{
				Success:     false,
				ErrorCode:   "error_moved_during_rest",
				ErrorMsg:    a.getI18nMsg("calibration.error_moved_during_rest"),
				SampleCount: len(samples),
				PeakSpeed:   peakSpeed,
			}
			a.writeDebugCSV(step, samples, res)
			return res
		}

		// Store verified zero-bias
		a.biasMu.Lock()
		a.gyroBias = [3]float64{bX, bY, bZ}
		a.biasMu.Unlock()

		// Capture average gravity unit vector during stillness
		var sumAccX, sumAccY, sumAccZ float64
		for _, s := range samples {
			sumAccX += s.acc[0]
			sumAccY += s.acc[1]
			sumAccZ += s.acc[2]
		}
		gX := sumAccX / n
		gY := sumAccY / n
		gZ := sumAccZ / n
		gNorm := math.Sqrt(gX*gX + gY*gY + gZ*gZ)
		if gNorm > 0.4 {
			a.calGravity = [3]float64{gX / gNorm, gY / gNorm, gZ / gNorm}
		}

		res := CaptureResult{
			Success:     true,
			AxisIdx:     -1,
			Sign:        1.0,
			AxisName:    "",
			Confidence:  1.0,
			SampleCount: len(samples),
			PeakSpeed:   peakSpeed,
		}
		a.writeDebugCSV(step, samples, res)
		return res
	}

	// ── Step 1 & 2: Dynamic gestures (Pitch / Roll) ──
	// Subtract calibrated bias first (§1 of spec)
	a.biasMu.RLock()
	bx, by, bz := a.gyroBias[0], a.gyroBias[1], a.gyroBias[2]
	a.biasMu.RUnlock()

	for i := range samples {
		samples[i].rot[0] -= bx
		samples[i].rot[1] -= by
		samples[i].rot[2] -= bz
	}

	axisNames := []string{"X", "Y", "Z"}

	var peakSpeed float64
	var activeCount int
	var energy [3]float64
	var peakVal [3]float64

	for _, s := range samples {
		speed := math.Sqrt(s.rot[0]*s.rot[0] + s.rot[1]*s.rot[1] + s.rot[2]*s.rot[2])
		if speed > peakSpeed {
			peakSpeed = speed
		}
		if speed >= 10.0 { // movement threshold in °/s
			activeCount++
			for i := 0; i < 3; i++ {
				energy[i] += s.rot[i] * s.rot[i]
				if math.Abs(s.rot[i]) > math.Abs(peakVal[i]) {
					peakVal[i] = s.rot[i]
				}
			}
		}
	}

	if activeCount < 3 || peakSpeed < 12.0 {
		res := CaptureResult{
			Success:     false,
			SampleCount: len(samples),
			PeakSpeed:   peakSpeed,
			ErrorCode:   "error_too_weak",
			ErrorMsg:    a.getI18nMsg("calibration.error_too_weak"),
		}
		a.writeDebugCSV(step, samples, res)
		return res
	}

	totalEnergy := energy[0] + energy[1] + energy[2]
	if totalEnergy < 1e-3 {
		res := CaptureResult{
			Success:     false,
			SampleCount: len(samples),
			PeakSpeed:   peakSpeed,
			ErrorCode:   "error_too_weak",
			ErrorMsg:    a.getI18nMsg("calibration.error_too_weak"),
		}
		a.writeDebugCSV(step, samples, res)
		return res
	}

	// Find dominant axis by energy
	axisIdx := 0
	maxEnergy := energy[0]
	for i := 1; i < 3; i++ {
		if energy[i] > maxEnergy {
			maxEnergy = energy[i]
			axisIdx = i
		}
	}
	secondEnergy := 0.0
	for i := 0; i < 3; i++ {
		if i != axisIdx && energy[i] > secondEnergy {
			secondEnergy = energy[i]
		}
	}

	confidence := 0.0
	if maxEnergy > 1e-6 {
		confidence = (maxEnergy - secondEnergy) / maxEnergy
	}

	// Determine gesture sign from the first significant half-wave of motion
	maxPeakOnAxis := math.Abs(peakVal[axisIdx])
	threshold := math.Max(8.0, 0.25*maxPeakOnAxis)
	sign := 1.0
	firstSign := 0.0
	for _, s := range samples {
		v := s.rot[axisIdx]
		if firstSign == 0 {
			if math.Abs(v) >= threshold {
				if v >= 0 {
					firstSign = 1.0
				} else {
					firstSign = -1.0
				}
			}
		} else {
			if (firstSign > 0 && v < -5.0) || (firstSign < 0 && v > 5.0) {
				break // End of first half-wave
			}
		}
	}
	if firstSign != 0 {
		sign = firstSign
	} else if peakVal[axisIdx] < 0 {
		sign = -1.0
	}

	signStr := "+"
	if sign < 0 {
		signStr = "-"
	}
	name := signStr + axisNames[axisIdx]

	// Ambiguity check
	if maxPeakOnAxis < 10.0 || confidence < 0.15 {
		res := CaptureResult{
			Success:     false,
			AxisIdx:     axisIdx,
			Sign:        sign,
			AxisName:    name,
			Confidence:  confidence,
			SampleCount: activeCount,
			PeakSpeed:   peakSpeed,
			ErrorCode:   "error_ambiguous",
			ErrorMsg:    a.getI18nMsg("calibration.error_ambiguous"),
		}
		a.writeDebugCSV(step, samples, res)
		return res
	}

	// Build canonical row vector according to formula in §3.1:
	// row_k = target_sign * detected_sign * e_{detected_axis}
	var d [3]float64
	if step == 1 {
		// Pitch step: target RotX < 0 when nodding forward -> target = -1.0
		targetPitchSign := -1.0
		d[axisIdx] = targetPitchSign * sign
		a.calVectors[0] = d
		a.calVectors[1] = [3]float64{0, 0, 0}
	} else if step == 2 {
		// Roll step: target RotZ > 0 when banking right -> target = +1.0
		targetRollSign := +1.0
		d[axisIdx] = targetRollSign * sign

		// Verify it's a different physical axis from Pitch
		pitchAxIdx := -1
		for i := 0; i < 3; i++ {
			if math.Abs(a.calVectors[0][i]) > 0.5 {
				pitchAxIdx = i
				break
			}
		}
		if pitchAxIdx >= 0 && pitchAxIdx == axisIdx {
			res := CaptureResult{
				Success:     false,
				AxisIdx:     axisIdx,
				Sign:        sign,
				AxisName:    name,
				Confidence:  confidence,
				SampleCount: activeCount,
				PeakSpeed:   peakSpeed,
				Vector:      d,
				ErrorCode:   "error_grip_changed",
				ErrorMsg:    a.getI18nMsg("calibration.error_grip_changed"),
			}
			a.writeDebugCSV(step, samples, res)
			return res
		}
		a.calVectors[1] = d
	}

	res := CaptureResult{
		Success:     true,
		AxisIdx:     axisIdx,
		Sign:        sign,
		AxisName:    name,
		Confidence:  confidence,
		SampleCount: activeCount,
		PeakSpeed:   peakSpeed,
		Vector:      d,
	}
	a.writeDebugCSV(step, samples, res)
	return res
}

// ValidateCalibration builds the calibration matrix from the 2 captured gesture vectors: Pitch and Roll.
// Yaw is computed as Pitch x Roll with det = -1.0 (Cemuhook DSU left-handed parity convention, §3.3).
func (a *App) ValidateCalibration(pitch, roll [3]float64) ValidationResult {
	norm := func(v [3]float64) [3]float64 {
		m := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
		if m < 1e-6 {
			return v
		}
		return [3]float64{v[0] / m, v[1] / m, v[2] / m}
	}

	// Snap each vector to nearest cardinal axis (±X, ±Y, or ±Z)
	snap := func(v [3]float64) [3]float64 {
		ax := 0
		maxVal := math.Abs(v[0])
		if math.Abs(v[1]) > maxVal {
			ax = 1
			maxVal = math.Abs(v[1])
		}
		if math.Abs(v[2]) > maxVal {
			ax = 2
		}
		var res [3]float64
		if v[ax] >= 0 {
			res[ax] = 1.0
		} else {
			res[ax] = -1.0
		}
		return res
	}

	getAxisIdx := func(v [3]float64) int {
		for i := 0; i < 3; i++ {
			if math.Abs(v[i]) > 0.5 {
				return i
			}
		}
		return -1
	}

	pitchRow := snap(norm(pitch))
	rollRow  := snap(norm(roll))

	idxP := getAxisIdx(pitchRow)
	idxR := getAxisIdx(rollRow)

	// Tier 2: Pitch and Roll must activate different physical axes
	if idxP < 0 || idxR < 0 || idxP == idxR {
		res := ValidationResult{
			Success:   false,
			ErrorCode: "error_grip_changed",
			ErrorMsg:  a.getI18nMsg("calibration.error_grip_changed"),
		}
		a.calLogMu.Lock()
		a.calValResult = res
		a.calLogMu.Unlock()
		return res
	}

	// Tier 3: Compute canonical Cemuhook DSU calibration matrix.
	// Cross product for Yaw:
	yawRow := [3]float64{
		pitchRow[1]*rollRow[2] - pitchRow[2]*rollRow[1],
		pitchRow[2]*rollRow[0] - pitchRow[0]*rollRow[2],
		pitchRow[0]*rollRow[1] - pitchRow[1]*rollRow[0],
	}

	var mat [3][3]float64
	mat[0] = pitchRow
	mat[1] = yawRow
	mat[2] = rollRow

	// Cemuhook DSU is left-handed parity convention -> det(M) must be -1.0 (§3.3)
	if det3x3(mat) > 0 {
		yawRow = [3]float64{-yawRow[0], -yawRow[1], -yawRow[2]}
		mat[1] = yawRow
	}

	det := det3x3(mat)
	if math.Abs(det+1.0) > 0.05 {
		res := ValidationResult{
			Success:   false,
			ErrorCode: "error_invalid_determinant",
			ErrorMsg:  a.getI18nMsg("calibration.error_invalid_determinant"),
		}
		a.calLogMu.Lock()
		a.calValResult = res
		a.calLogMu.Unlock()
		return res
	}

	formatAxis := func(v [3]float64) string {
		axes := []string{"X", "Y", "Z"}
		for i := 0; i < 3; i++ {
			if v[i] > 0.5 {
				return "+" + axes[i]
			} else if v[i] < -0.5 {
				return "-" + axes[i]
			}
		}
		return "?"
	}

	res := ValidationResult{
		Success:   true,
		Matrix:    mat,
		Det:       det,
		PitchAxis: formatAxis(mat[0]),
		YawAxis:   formatAxis(mat[1]),
		RollAxis:  formatAxis(mat[2]),
	}
	a.calLogMu.Lock()
	a.calValResult = res
	a.calLogMu.Unlock()
	return res
}

func det3x3(m [3][3]float64) float64 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
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

// CopyLast20Frames returns the last 20 raw frames formatted as CSV text for clipboard
func (a *App) CopyLast20Frames() string {
	a.recentFramesMu.Lock()
	defer a.recentFramesMu.Unlock()

	if len(a.recentFrames) == 0 {
		return "No frames received from phone yet"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Recent %d raw frames from phone:\n", len(a.recentFrames)))
	sb.WriteString("idx,ts,rotX,rotY,rotZ,accX,accY,accZ,qx,qy,qz,qw\n")
	for i, f := range a.recentFrames {
		sb.WriteString(fmt.Sprintf("%d,%d,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f\n",
			i+1, f.Timestamp, f.RotX, f.RotY, f.RotZ, f.AccX, f.AccY, f.AccZ, f.Qx, f.Qy, f.Qz, f.Qw))
	}
	return sb.String()
}

// CopyCalibrationReport returns a detailed text report of the last calibration session:
// raw samples from each 2.5s step, algorithm decisions, and final matrix verdict.
func (a *App) CopyCalibrationReport() string {
	a.calLogMu.Lock()
	defer a.calLogMu.Unlock()

	var sb strings.Builder
	sb.WriteString("=== GYROBRIDGE CALIBRATION FULL REPORT ===\n")
	sb.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// Final Verdict
	val := a.calValResult
	sb.WriteString("--- FINAL VERDICT & MATRIX ---\n")
	sb.WriteString(fmt.Sprintf("Validation Success: %v\n", val.Success))
	if !val.Success && val.ErrorCode != "" {
		sb.WriteString(fmt.Sprintf("Error: [%s] %s\n", val.ErrorCode, val.ErrorMsg))
	}
	sb.WriteString(fmt.Sprintf("Determinant: %.4f\n", val.Det))
	sb.WriteString(fmt.Sprintf("Pitch Axis (Row 0): %s\n", val.PitchAxis))
	sb.WriteString(fmt.Sprintf("Yaw Axis   (Row 1): %s\n", val.YawAxis))
	sb.WriteString(fmt.Sprintf("Roll Axis  (Row 2): %s\n", val.RollAxis))
	sb.WriteString("Matrix:\n")
	for row := 0; row < 3; row++ {
		sb.WriteString(fmt.Sprintf("  [%8.4f, %8.4f, %8.4f]\n",
			val.Matrix[row][0], val.Matrix[row][1], val.Matrix[row][2]))
	}
	sb.WriteString("\n")

	// Static Gyro Bias
	a.biasMu.RLock()
	bias := a.gyroBias
	a.biasMu.RUnlock()
	sb.WriteString(fmt.Sprintf("Static Gyro Bias: [%.4f, %.4f, %.4f] deg/s\n\n", bias[0], bias[1], bias[2]))

	// Steps (0 = Rest/Stillness, 1 = Pitch, 2 = Roll)
	stepNames := map[int]string{
		0: "STEP 0: REST / STILLNESS BIAS",
		1: "STEP 1: PITCH (Nod forward)",
		2: "STEP 2: ROLL (Bank right)",
	}

	for step := 0; step < 3; step++ {
		log, exists := a.calStepLogs[step]
		name := stepNames[step]
		sb.WriteString(fmt.Sprintf("--- %s ---\n", name))
		if !exists || len(log.Samples) == 0 {
			sb.WriteString("No samples recorded for this step.\n\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("Algorithm Result: Success=%v, DetectedAxis=%s (index %d), Sign=%+.1f, Confidence=%.1f%%, PeakSpeed=%.2f deg/s, SamplesCount=%d\n",
			log.Result.Success, log.Result.AxisName, log.Result.AxisIdx, log.Result.Sign, log.Result.Confidence*100, log.Result.PeakSpeed, len(log.Samples)))
		sb.WriteString(fmt.Sprintf("Detected Vector: [%.1f, %.1f, %.1f]\n",
			log.Result.Vector[0], log.Result.Vector[1], log.Result.Vector[2]))
		if log.Result.ErrorCode != "" {
			sb.WriteString(fmt.Sprintf("Error: [%s] %s\n", log.Result.ErrorCode, log.Result.ErrorMsg))
		}
		sb.WriteString("RAW SAMPLES (2.5 sec at 60 Hz):\n")
		sb.WriteString("idx,rotX,rotY,rotZ,accX,accY,accZ,speed\n")
		for i, s := range log.Samples {
			speed := math.Sqrt(s.rot[0]*s.rot[0] + s.rot[1]*s.rot[1] + s.rot[2]*s.rot[2])
			sb.WriteString(fmt.Sprintf("%d,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f\n",
				i+1, s.rot[0], s.rot[1], s.rot[2], s.acc[0], s.acc[1], s.acc[2], speed))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("=== END OF REPORT ===\n")
	return sb.String()
}

// GetResourceStats returns the most recent CPU and RAM snapshot for the process.
func (a *App) GetResourceStats() map[string]any {
	if p := a.lastResStats.Load(); p != nil {
		return *p
	}
	return map[string]any{
		"cpuPercent": 0.0,
		"ramMb":      0.0,
		"totalRamMb": 0.0,
		"ramPercent": 0.0,
	}
}

