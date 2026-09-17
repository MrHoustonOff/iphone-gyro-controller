package main

import (
	"io/fs"
	"math"
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
	// DSU Acc must be [0, 0, -1.0] (gravity on AccZ)
	m1 := computeAccMatrix([3]float64{0, 0, -1.0})
	ax1, ay1, az1 := applyMatrix(m1, 0, 0, -1.0)
	if math.Abs(ax1) > 1e-4 || math.Abs(ay1) > 1e-4 || math.Abs(az1+1.0) > 1e-4 {
		t.Fatalf("Case 1 (screen up) failed: got (%f, %f, %f), expected (0, 0, -1)", ax1, ay1, az1)
	}

	// 2. Phone flat on table, screen down: calGravity = [0, 0, +1.0]
	m2 := computeAccMatrix([3]float64{0, 0, 1.0})
	ax2, ay2, az2 := applyMatrix(m2, 0, 0, 1.0)
	if math.Abs(ax2) > 1e-4 || math.Abs(ay2) > 1e-4 || math.Abs(az2+1.0) > 1e-4 {
		t.Fatalf("Case 2 (screen down) failed: got (%f, %f, %f), expected (0, 0, -1)", ax2, ay2, az2)
	}

	// 3. Uninitialized / zero calGravity
	m3 := computeAccMatrix([3]float64{0, 0, 0})
	ax3, ay3, az3 := applyMatrix(m3, 0, 0, -1.0)
	if math.Abs(ax3) > 1e-4 || math.Abs(ay3) > 1e-4 || math.Abs(az3+1.0) > 1e-4 {
		t.Fatalf("Case 3 (zero) failed: got (%f, %f, %f), expected (0, 0, -1)", ax3, ay3, az3)
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
		{0, 0, 1},   // Pitch = +Z
		{0, -1, 0},  // Yaw   = -Y (negated so clockwise physical turn rotates clockwise in PadTest!)
		{1, 0, 0},   // Roll  = +X
	}
	if val.Matrix != expectedMatrix {
		t.Fatalf("Matrix mismatch. Got %v, expected %v", val.Matrix, expectedMatrix)
	}

	// Verify Yaw sign: Clockwise turn produces raw positive Y (looking from above, angle advances)
	// In Cemuhook DSU, RotY must be negative for PadTest to rotate clockwise
	rawClockwise := [3]float64{0, 35.0, 0}
	_, ry, _ := applyMatrix(val.Matrix, rawClockwise[0], rawClockwise[1], rawClockwise[2])
	if ry >= 0 {
		t.Fatalf("RotY must be NEGATIVE for clockwise turn in landscape, got %f", ry)
	}

	// Verify Accelerometer when resting on desk: calGravity = [0, 0, -1.0]
	accMat := computeAccMatrix([3]float64{0.01, 0.04, -1.00})
	ax, ay, az := applyMatrix(accMat, 0.01, 0.04, -1.00)
	if math.Abs(ax) > 0.05 || math.Abs(ay) > 0.05 || math.Abs(az+1.0) > 0.05 {
		t.Fatalf("Resting gravity not on AccZ: got (%f, %f, %f)", ax, ay, az)
	}

	// Verify PadTest equilibrium orientation: must be exactly upright (zero roll/pitch tilt)
	ahrs := NewMadgwickAHRS(0.1)
	for i := 0; i < 200; i++ {
		ahrs.Update(0, 0, 0, float32(ax), float32(ay), float32(az), time.Now())
		if i%50 == 0 || i == 199 {
			p, r, y := ahrs.GetEulerAngles()
			t.Logf("iter %d: Q=(%.3f, %.3f, %.3f, %.3f) p=%.1f r=%.1f y=%.1f", i, ahrs.Q0, ahrs.Q1, ahrs.Q2, ahrs.Q3, p, r, y)
		}
	}
	p, r, y := ahrs.GetEulerAngles()
	if math.Abs(p) > 2.0 || math.Abs(r) > 2.0 || math.Abs(y) > 2.0 {
		t.Fatalf("PadTest wireframe tilted! Pitch=%f°, Roll=%f°, Yaw=%f°", p, r, y)
	}
}



