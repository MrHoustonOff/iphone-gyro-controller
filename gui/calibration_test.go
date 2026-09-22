package main

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCalibration_StillnessStep0(t *testing.T) {
	app := &App{}

	// 1. Normal motionless resting on desk (bias test)
	app.StartCapture()
	for i := 0; i < 30; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{0.45, -0.25, 0.15},
			acc: [3]float64{0, 0, 1.0},
		})
		app.captureMu.Unlock()
	}
	res := app.StopCapture(0)
	if !res.Success {
		t.Fatalf("expected Step 0 stillness to succeed, got error: %s (%s)", res.ErrorCode, res.ErrorMsg)
	}

	app.biasMu.RLock()
	bx, by, bz := app.gyroBias[0], app.gyroBias[1], app.gyroBias[2]
	app.biasMu.RUnlock()

	if math.Abs(bx-0.45) > 0.01 || math.Abs(by+0.25) > 0.01 || math.Abs(bz-0.15) > 0.01 {
		t.Fatalf("unexpected bias values: %f, %f, %f", bx, by, bz)
	}

	// 2. Moving / shaking during Step 0 -> must fail with error_moved_during_rest
	app.StartCapture()
	for i := 0; i < 30; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{float64(i % 5) * 3.0, 0.0, 0.0},
			acc: [3]float64{0, 0, 1.0},
		})
		app.captureMu.Unlock()
	}
	resMoved := app.StopCapture(0)
	if resMoved.Success {
		t.Fatalf("expected shaking during Step 0 to fail")
	}
	if resMoved.ErrorCode != "error_moved_during_rest" {
		t.Fatalf("expected error_moved_during_rest, got %s", resMoved.ErrorCode)
	}
}

func TestCalibration_SignsAndDeterminant(t *testing.T) {
	app := &App{}

	// Portrait mode gestures:
	// Pitch forward: channel 0 (X) moves in negative direction (beta < 0) -> row_pitch = target(-1) * sign(-1) * e_0 = [+1, 0, 0]
	// Roll right: channel 2 (Z) moves in negative direction (alpha < 0) -> row_roll = target(+1) * sign(-1) * e_2 = [0, 0, -1]
	pitchRow := [3]float64{1, 0, 0}
	rollRow := [3]float64{0, 0, -1}

	res := app.ValidateCalibration(pitchRow, rollRow)
	if !res.Success {
		t.Fatalf("expected valid calibration to pass, got error: %s (%s)", res.ErrorCode, res.ErrorMsg)
	}

	// Cemuhook DSU is a left-handed parity convention (§3.3 of spec) -> det = -1.0
	if math.Abs(res.Det+1.0) > 1e-4 {
		t.Fatalf("expected det = -1.0, got %f", res.Det)
	}

	if res.PitchAxis != "+X" || res.YawAxis != "+Y" || res.RollAxis != "-Z" {
		t.Fatalf("unexpected axis names: P=%s Y=%s R=%s", res.PitchAxis, res.YawAxis, res.RollAxis)
	}

	expectedMatrix := [3][3]float64{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, -1},
	}
	if res.Matrix != expectedMatrix {
		t.Fatalf("expected canonical matrix %v, got %v", expectedMatrix, res.Matrix)
	}
}

func TestYawSignNotFlipped(t *testing.T) {
	app := &App{}

	// Regression test for §3.3 & §7:
	// In portrait orientation, Pitch=+X, Roll=-Z.
	// When user turns phone clockwise (looking from top down), physical sensor rotates around +Y in negative direction (gamma < 0).
	// Protcol requires RotY < 0.
	pitchRow := [3]float64{1, 0, 0}
	rollRow := [3]float64{0, 0, -1}
	res := app.ValidateCalibration(pitchRow, rollRow)
	if !res.Success {
		t.Fatalf("calibration failed: %s", res.ErrorMsg)
	}

	// Matrix multiplication of pure clockwise turn: raw = [0, -40, 0]
	rawClockwise := [3]float64{0, -40.0, 0}
	rx, ry, rz := applyMatrix(res.Matrix, rawClockwise[0], rawClockwise[1], rawClockwise[2])

	if ry >= 0 {
		t.Fatalf("RotY must be NEGATIVE for clockwise turn, got %f", ry)
	}
	if math.Abs(rx) > 1e-4 || math.Abs(rz) > 1e-4 {
		t.Fatalf("Cross-axis leakage: rx=%f, rz=%f", rx, rz)
	}
}

func TestPitchRollAxisCollision(t *testing.T) {
	app := &App{}

	// Pitch and Roll mapped to same physical axis (X) -> must fail with error_grip_changed
	resGrip := app.ValidateCalibration([3]float64{1, 0, 0}, [3]float64{1, 0, 0})
	if resGrip.Success {
		t.Fatalf("expected duplicate Pitch+Roll axes to fail")
	}
	if resGrip.ErrorCode != "error_grip_changed" {
		t.Fatalf("expected error_grip_changed, got %s", resGrip.ErrorCode)
	}
}

func TestCalibration_EndToEnd_StandardPortraitFlow(t *testing.T) {
	app := &App{}

	// Step 0: Stillness (Bias)
	app.StartCapture()
	for i := 0; i < 25; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{0.1, -0.05, 0.08},
			acc: [3]float64{0, 1.0, 0},
		})
		app.captureMu.Unlock()
	}
	res0 := app.StopCapture(0)
	if !res0.Success {
		t.Fatalf("Step 0 failed: %s", res0.ErrorMsg)
	}

	// Step 1 (Pitch / "Кивни"): Nod phone forward -> raw sensor detects -X
	app.StartCapture()
	for i := 0; i < 20; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{-65.0, 0.2, -0.1},
			acc: [3]float64{0, 1.0, 0},
		})
		app.captureMu.Unlock()
	}
	res1 := app.StopCapture(1)
	if !res1.Success {
		t.Fatalf("Step 1 failed: %s (%s)", res1.ErrorCode, res1.ErrorMsg)
	}
	if res1.Vector != [3]float64{1, 0, 0} {
		t.Fatalf("Step 1 expected [+1, 0, 0], got %v", res1.Vector)
	}

	// Step 2 (Roll / "Самолётик"): Bank right -> raw sensor detects -Z in portrait
	app.StartCapture()
	for i := 0; i < 20; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{0.1, 0.2, -58.0},
			acc: [3]float64{0, 1.0, 0},
		})
		app.captureMu.Unlock()
	}
	res2 := app.StopCapture(2)
	if !res2.Success {
		t.Fatalf("Step 2 failed: %s (%s)", res2.ErrorCode, res2.ErrorMsg)
	}
	if res2.Vector != [3]float64{0, 0, -1} {
		t.Fatalf("Step 2 expected [0, 0, -1], got %v", res2.Vector)
	}

	// Validation
	val := app.ValidateCalibration(res1.Vector, res2.Vector)
	if !val.Success {
		t.Fatalf("Validation failed: %s (%s)", val.ErrorCode, val.ErrorMsg)
	}
	if val.PitchAxis != "+X" || val.YawAxis != "+Y" || val.RollAxis != "-Z" {
		t.Fatalf("Expected P=+X, Y=+Y, R=-Z; got P=%s, Y=%s, R=%s", val.PitchAxis, val.YawAxis, val.RollAxis)
	}
	if math.Abs(val.Det+1.0) > 1e-4 {
		t.Fatalf("Expected det = -1.0, got %f", val.Det)
	}

	expectedMatrix := [3][3]float64{
		{1, 0, 0},   // Pitch = +X
		{0, 1, 0},   // Yaw   = +Y
		{0, 0, -1},  // Roll  = -Z
	}
	if val.Matrix != expectedMatrix {
		t.Fatalf("Matrix mismatch. Got %v, expected %v", val.Matrix, expectedMatrix)
	}
}

func TestMadgwickAHRS_EulerAngleSigns(t *testing.T) {
	// 1. Identity quaternion -> all angles 0
	ahrs := NewMadgwickAHRS(0.0)
	p0, r0, y0 := ahrs.GetEulerAngles()
	if math.Abs(p0) > 1e-3 || math.Abs(r0) > 1e-3 || math.Abs(y0) > 1e-3 {
		t.Fatalf("Expected identity angles to be 0, got p=%f r=%f y=%f", p0, r0, y0)
	}

	// 2. Pitch forward (nodding forward, q1 > 0): Pitch must be positive
	halfAng := 15.0 * math.Pi / 180.0
	ahrs.Q0 = float32(math.Cos(halfAng))
	ahrs.Q1 = float32(math.Sin(halfAng))
	ahrs.Q2 = 0
	ahrs.Q3 = 0
	p, r, y := ahrs.GetEulerAngles()
	if p < 29.0 || p > 31.0 || math.Abs(r) > 1e-3 || math.Abs(y) > 1e-3 {
		t.Fatalf("Expected pitch ~+30.0, got p=%f r=%f y=%f", p, r, y)
	}

	// 3. Roll right (banking right, q3 < 0): Roll must be positive
	ahrs.Q0 = float32(math.Cos(halfAng))
	ahrs.Q1 = 0
	ahrs.Q2 = 0
	ahrs.Q3 = float32(-math.Sin(halfAng))
	p, r, y = ahrs.GetEulerAngles()
	if r < 29.0 || r > 31.0 || math.Abs(p) > 1e-3 || math.Abs(y) > 1e-3 {
		t.Fatalf("Expected roll ~+30.0, got p=%f r=%f y=%f", p, r, y)
	}

	// 4. Yaw clockwise (turning right, q2 > 0): Yaw must be positive
	ahrs.Q0 = float32(math.Cos(halfAng))
	ahrs.Q1 = 0
	ahrs.Q2 = float32(math.Sin(halfAng))
	ahrs.Q3 = 0
	p, r, y = ahrs.GetEulerAngles()
	if y < 29.0 || y > 31.0 || math.Abs(p) > 1e-3 || math.Abs(r) > 1e-3 {
		t.Fatalf("Expected yaw ~+30.0, got p=%f r=%f y=%f", p, r, y)
	}
}

func TestLiveDebug_AssetsAndBroadcast(t *testing.T) {
	// Verify embedded assets contain livedebug.html and required static assets
	subFS, err := fs.Sub(assets, "frontend/src")
	if err != nil {
		t.Fatalf("fs.Sub failed: %v", err)
	}

	htmlData, err := fs.ReadFile(subFS, "livedebug.html")
	if err != nil {
		t.Fatalf("failed to read livedebug.html from embedded FS: %v", err)
	}
	if len(htmlData) == 0 {
		t.Fatal("livedebug.html is empty")
	}

	// Verify crucial elements in livedebug.html
	content := string(htmlData)
	if !strings.Contains(content, "livedebug-canvas") {
		t.Fatal("livedebug.html missing livedebug-canvas")
	}
	if !strings.Contains(content, "eco-toggle") {
		t.Fatal("livedebug.html missing eco-toggle")
	}
	if !strings.Contains(content, "/livedebug/ws") {
		t.Fatal("livedebug.html missing /livedebug/ws endpoint connection")
	}
	if !strings.Contains(content, "model-segmented") {
		t.Fatal("livedebug.html missing model-segmented control")
	}
	if !strings.Contains(content, "btn-recenter") {
		t.Fatal("livedebug.html missing btn-recenter button")
	}

	// Verify broadcast with zero clients does not panic
	app := &App{
		currentTheme: "dark",
		currentLang:  "ru",
	}
	app.broadcastLiveDebug(1, 0, 0, 0)
	app.broadcastLiveDebugJSON(map[string]string{"type": "theme", "theme": "light"})
}

func TestLiveDebug_ThemeAndLangSync(t *testing.T) {
	app := &App{
		currentTheme: "dark",
		currentLang:  "ru",
	}

	if app.GetTheme() != "dark" {
		t.Fatalf("expected initial theme dark, got %s", app.GetTheme())
	}
	if app.GetLang() != "ru" {
		t.Fatalf("expected initial lang ru, got %s", app.GetLang())
	}

	app.SetTheme("light")
	if app.GetTheme() != "light" {
		t.Fatalf("expected theme light after SetTheme, got %s", app.GetTheme())
	}

	app.SetLang("en")
	if app.GetLang() != "en" {
		t.Fatalf("expected lang en after SetLang, got %s", app.GetLang())
	}
}

func TestLiveDebug_AppMethods(t *testing.T) {
	debugApp := NewLiveDebugApp()
	if debugApp == nil {
		t.Fatal("NewLiveDebugApp returned nil")
	}
	// Verify GetDeviceStatus returns without panicking regardless of background server state
	_ = debugApp.GetDeviceStatus()
}

func TestCalibration_UnconstrainedGestureRecognition(t *testing.T) {
	app := &App{}

	// Step 0: Rest on desk
	app.StartCapture()
	for i := 0; i < 25; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{0.0, 0.0, 0.0},
			acc: [3]float64{0, 0, 1.0},
		})
		app.captureMu.Unlock()
	}
	res0 := app.StopCapture(0)
	if !res0.Success {
		t.Fatalf("Step 0 failed: %s", res0.ErrorMsg)
	}

	// Step 1: Gesture on any axis succeeds without artificial blocking
	app.StartCapture()
	for i := 0; i < 20; i++ {
		app.captureMu.Lock()
		app.captureBuffer = append(app.captureBuffer, captureSample{
			rot: [3]float64{0.1, 0.2, -65.0},
			acc: [3]float64{0, 0, 1.0},
		})
		app.captureMu.Unlock()
	}
	res1 := app.StopCapture(1)
	if !res1.Success {
		t.Fatalf("Step 1 expected to succeed, got error: %s", res1.ErrorCode)
	}
}

func TestPadTest_Convergence(t *testing.T) {
	testCases := []struct {
		name string
		ax, ay, az float32
	}{
		{"AccX=-1.0 (Current bug)", -1.0, 0.0, 0.0},
		{"AccZ=-1.0", 0.0, 0.0, -1.0},
		{"AccZ=+1.0", 0.0, 0.0, 1.0},
		{"AccY=-1.0", 0.0, -1.0, 0.0},
		{"AccY=+1.0", 0.0, 1.0, 0.0},
	}

	for _, tc := range testCases {
		ahrs := NewMadgwickAHRS(0.1) // Exact PadTest Beta
		// Run 300 steps (5 seconds at 60 Hz) of stationary holding
		for i := 0; i < 300; i++ {
			ahrs.Update(0, 0, 0, tc.ax, tc.ay, tc.az, time.Now())
		}
		p, r, y := ahrs.GetEulerAngles()
		t.Logf("[%s] Q: (%+.3f, %+.3f, %+.3f, %+.3f) -> Pitch: %+.1f°, Roll: %+.1f°, Yaw: %+.1f°",
			tc.name, ahrs.Q0, ahrs.Q1, ahrs.Q2, ahrs.Q3, p, r, y)
	}
}

func TestComputeAccMatrix(t *testing.T) {
	// 1. Phone flat on table, screen up: calGravity = [0, 0, -1.0]
	// Cemuhook DSU resting gravity is along -AccY: [0, -1.0, 0]
	m1 := computeAccMatrix([3]float64{0, 0, -1.0})
	ax1, ay1, az1 := applyMatrix(m1, 0, 0, -1.0)
	if math.Abs(ax1) > 1e-4 || math.Abs(ay1+1.0) > 1e-4 || math.Abs(az1) > 1e-4 {
		t.Fatalf("Case 1 (screen up) failed: got (%f, %f, %f), expected (0, -1, 0)", ax1, ay1, az1)
	}

	// 2. Phone flat on table, screen down: calGravity = [0, 0, +1.0]
	m2 := computeAccMatrix([3]float64{0, 0, 1.0})
	ax2, ay2, az2 := applyMatrix(m2, 0, 0, 1.0)
	if math.Abs(ax2) > 1e-4 || math.Abs(ay2+1.0) > 1e-4 || math.Abs(az2) > 1e-4 {
		t.Fatalf("Case 2 (screen down) failed: got (%f, %f, %f), expected (0, -1, 0)", ax2, ay2, az2)
	}

	// 3. Uninitialized / zero calGravity
	m3 := computeAccMatrix([3]float64{0, 0, 0})
	ax3, ay3, az3 := applyMatrix(m3, 0, 0, -1.0)
	if math.Abs(ax3) > 1e-4 || math.Abs(ay3+1.0) > 1e-4 || math.Abs(az3) > 1e-4 {
		t.Fatalf("Case 3 (zero) failed: got (%f, %f, %f), expected (0, -1, 0)", ax3, ay3, az3)
	}
}

func TestLandscapeCalibration_YawAndPadTest(t *testing.T) {
	app := &App{}

	// Landscape user gestures: Pitch = +Z, Roll = +X
	pitchRow := [3]float64{0, 0, 1}
	rollRow := [3]float64{1, 0, 0}

	val := app.ValidateCalibration(pitchRow, rollRow)
	if !val.Success {
		t.Fatalf("Validation failed: %s (%s)", val.ErrorCode, val.ErrorMsg)
	}

	expectedMatrix := [3][3]float64{
		{0, 0, 1},  // Pitch = +Z
		{0, 1, 0},  // Yaw   = +Y (natural right-handed yaw, matches Three.js and PadTest)
		{1, 0, 0},  // Roll  = +X
	}
	if val.Matrix != expectedMatrix {
		t.Fatalf("Matrix mismatch. Got %v, expected %v", val.Matrix, expectedMatrix)
	}

	// Verify Yaw sign: Clockwise turn produces positive RotY
	rawClockwise := [3]float64{0, 35.0, 0}
	_, ry, _ := applyMatrix(val.Matrix, rawClockwise[0], rawClockwise[1], rawClockwise[2])
	if ry <= 0 {
		t.Fatalf("RotY must be POSITIVE for clockwise turn in landscape, got %f", ry)
	}

	// Verify Accelerometer when resting on desk: calGravity = [0, 0, -1.0]
	accMat := computeAccMatrix([3]float64{0.01, 0.04, -1.00})
	ax, ay, az := applyMatrix(accMat, 0.01, 0.04, -1.00)
	if math.Abs(ax) > 0.05 || math.Abs(ay+1.0) > 0.05 || math.Abs(az) > 0.05 {
		t.Fatalf("Resting gravity not on AccY: got (%f, %f, %f)", ax, ay, az)
	}
}

func TestProfileSlots6_And_SettingsPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gyrobridge-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	app := NewApp()
	app.profilesDir = tempDir
	app.loadSettings()
	app.loadProfiles()

	// Verify initial 6 slots
	profs := app.GetProfiles()
	if len(profs) != 6 {
		t.Fatalf("expected 6 profile slots, got %d", len(profs))
	}

	// Test saving to slot 5 (6th slot)
	mat := defaultMatrix3x3()
	res := app.SaveProfile(5, "Slot Six Custom", "iPhone 15 Pro", "vertical", mat)
	if res != "ok" {
		t.Fatalf("failed to save slot 5: %s", res)
	}

	// Verify out-of-bounds rejected
	resInvalid := app.SaveProfile(6, "Invalid", "iPhone", "vertical", mat)
	if resInvalid != "invalid slot" {
		t.Fatalf("expected invalid slot for slot 6, got %s", resInvalid)
	}

	// Verify slot 5 in GetProfiles
	profs = app.GetProfiles()
	if profs[5].Name != "Slot Six Custom" {
		t.Fatalf("expected slot 5 name 'Slot Six Custom', got '%s'", profs[5].Name)
	}

	// Test SetActiveProfile to 5
	resActive := app.SetActiveProfile(5)
	if resActive != "ok" {
		t.Fatalf("failed to set active profile to 5: %s", resActive)
	}
	if app.activeSlot != 5 {
		t.Fatalf("expected activeSlot 5, got %d", app.activeSlot)
	}

	// Test settings persistence
	app.SetTheme("light")
	app.SetLang("en")

	// Test first-launch behavior
	if !app.IsFirstLaunch() {
		t.Fatalf("expected IsFirstLaunch to be true initially")
	}
	app.MarkFirstLaunchDone()
	if app.IsFirstLaunch() {
		t.Fatalf("expected IsFirstLaunch to be false after MarkFirstLaunchDone")
	}

	// Test hideAuthor behavior
	if app.GetHideAuthor() {
		t.Fatalf("expected hideAuthor to default to false")
	}
	app.SetHideAuthor(true)
	if !app.GetHideAuthor() {
		t.Fatalf("expected hideAuthor to be true after SetHideAuthor(true)")
	}

	settingsPath := filepath.Join(tempDir, "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatalf("settings.json was not created at %s", settingsPath)
	}

	// Test reload settings
	appReload := NewApp()
	appReload.profilesDir = tempDir
	appReload.loadSettings()
	if appReload.IsFirstLaunch() {
		t.Fatalf("expected reloaded app to have firstLaunchDone = true")
	}
	if !appReload.GetHideAuthor() {
		t.Fatalf("expected reloaded app to have hideAuthor = true")
	}
	if appReload.GetLang() != "en" {
		t.Fatalf("expected reloaded app to have lang = en, got %s", appReload.GetLang())
	}

	// Test sound volumes persistence
	defs := defaultSoundVolumes()
	if defs["connect"] != 1 || defs["dsu"] != 1 || defs["recenter"] != 1 {
		t.Fatalf("expected default sound volumes of 1, got %+v", defs)
	}
	customVols := map[string]int{
		"connect":    2,
		"disconnect": 1,
		"dsu":        3,
		"recenter":   0,
		"goal":       1,
		"defeat":     2,
	}
	app.setSoundVolumes(customVols)
	app.saveSettings()

	// Test logs persistence
	app.logEvent("TEST", "Test log message")
	logPath := filepath.Join(tempDir, "logs", "gyrobridge.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Fatalf("gyrobridge.log was not created at %s", logPath)
	}

	// Verify sound volumes reloaded
	appReload.loadSettings()
	reloadedVols := appReload.getSoundVolumes()
	if reloadedVols["dsu"] != 3 || reloadedVols["recenter"] != 0 || reloadedVols["connect"] != 2 {
		t.Fatalf("expected reloaded sound volumes to match, got %+v", reloadedVols)
	}
}
