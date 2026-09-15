package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gyrobridge/pkg/ca"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	tmpDir, err := os.MkdirTemp("", "gyro_srv_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cm, err := ca.NewCertificateManager(tmpDir, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	dummyHTML := []byte("<html><body>Test Client</body></html>")

	srv := NewServer(cm, 0, 0, dummyHTML, nil)

	cleanup := func() {
		srv.Stop()
		os.RemoveAll(tmpDir)
	}

	return srv, cleanup
}

func TestServer_MobileConfigEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/ca.mobileconfig", nil)
	w := httptest.NewRecorder()

	srv.handleMobileConfig(w, req)
	resp := w.Result()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/x-apple-as-config" {
		t.Errorf("expected Content-Type application/x-apple-as-config, got %s", contentType)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "GyroBridge Root CA") {
		t.Error("body does not contain GyroBridge Root CA")
	}
}

func TestServer_RawCACertEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/ca.crt", nil)
	w := httptest.NewRecorder()

	srv.handleRawCACert(w, req)
	resp := w.Result()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/x-x509-ca-cert" {
		t.Errorf("expected Content-Type application/x-x509-ca-cert, got %s", contentType)
	}
}

func TestServer_WebSocketTelemetry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyro_ws_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cm, err := ca.NewCertificateManager(tmpDir, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	frameChan := make(chan MotionFrame, 5)
	srv := NewServer(cm, 0, 0, []byte("ok"), func(f MotionFrame) {
		frameChan <- f
	})

	// Start httptest TLS server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWebSocket)

	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{*cm.LeafCert},
	}
	ts.StartTLS()
	defer ts.Close()

	// Prepare client with Root CA trusted
	rootPool := x509.NewCertPool()
	rootPool.AddCert(cm.RootCert)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs: rootPool,
		},
	}

	wsURL := "wss" + strings.TrimPrefix(ts.URL, "https") + "/ws"
	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer ws.Close()

	testFrame := MotionFrame{
		Timestamp: uint32(time.Now().UnixMilli() & 0xFFFFFFFF),
		RotX:      12.34,
		RotY:      -45.67,
		RotZ:      89.01,
		Qx:        0.1,
		Qy:        0.2,
		Qz:        0.3,
		Qw:        0.9,
		AccX:      0.1,
		AccY:      0.98,
		AccZ:      0.2,
		Buttons:   1,
	}

	data, _ := json.Marshal(testFrame)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	select {
	case received := <-frameChan:
		if received.RotX != testFrame.RotX || received.RotY != testFrame.RotY {
			t.Errorf("frame mismatch: got %+v, want %+v", received, testFrame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for motion frame")
	}

	// Test binary frame (46 bytes)
	binBuf := make([]byte, 46)
	// ts = 12345
	binary.LittleEndian.PutUint32(binBuf[0:4], 12345)
	// rotX = 45.5, rotY = -10.5, rotZ = 30.0
	binary.LittleEndian.PutUint32(binBuf[4:8], math.Float32bits(45.5))
	binary.LittleEndian.PutUint32(binBuf[8:12], math.Float32bits(-10.5))
	binary.LittleEndian.PutUint32(binBuf[12:16], math.Float32bits(30.0))
	// qx = 0.1, qy = 0.2, qz = 0.3, qw = 0.9
	binary.LittleEndian.PutUint32(binBuf[16:20], math.Float32bits(0.1))
	binary.LittleEndian.PutUint32(binBuf[20:24], math.Float32bits(0.2))
	binary.LittleEndian.PutUint32(binBuf[24:28], math.Float32bits(0.3))
	binary.LittleEndian.PutUint32(binBuf[28:32], math.Float32bits(0.9))
	// accX = 0.05, accY = 0.98, accZ = 0.12
	binary.LittleEndian.PutUint32(binBuf[32:36], math.Float32bits(0.05))
	binary.LittleEndian.PutUint32(binBuf[36:40], math.Float32bits(0.98))
	binary.LittleEndian.PutUint32(binBuf[40:44], math.Float32bits(0.12))
	// buttons = 3
	binary.LittleEndian.PutUint16(binBuf[44:46], 3)

	if err := ws.WriteMessage(websocket.BinaryMessage, binBuf); err != nil {
		t.Fatalf("failed to send binary message: %v", err)
	}

	select {
	case received := <-frameChan:
		if received.RotX != 45.5 || received.RotY != -10.5 || received.Qw != 0.9 || received.Buttons != 3 {
			t.Errorf("binary frame mismatch: got %+v", received)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for binary motion frame")
	}

	pkts, clients, _ := srv.PacketStats()
	if pkts != 2 {
		t.Errorf("expected 2 packets, got %d", pkts)
	}
	if clients != 1 {
		t.Errorf("expected 1 active client, got %d", clients)
	}
}

func TestServer_PauseSynchronization(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gyro_pause_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cm, err := ca.NewCertificateManager(tmpDir, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		t.Fatalf("NewCertificateManager failed: %v", err)
	}

	pauseEventChan := make(chan bool, 5)
	srv := NewServer(cm, 0, 0, []byte("ok"), nil)
	srv.GetIsPaused = func() bool { return false }
	srv.OnClientPause = func(p bool) {
		pauseEventChan <- p
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWebSocket)

	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{*cm.LeafCert},
	}
	ts.StartTLS()
	defer ts.Close()

	rootPool := x509.NewCertPool()
	rootPool.AddCert(cm.RootCert)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			RootCAs: rootPool,
		},
	}

	wsURL := "wss" + strings.TrimPrefix(ts.URL, "https") + "/ws"
	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer ws.Close()

	// Drain initial on-connect messages (binary [0x50, byte] and text JSON)
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 2; i++ {
		_, _, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("failed reading initial connect message: %v", err)
		}
	}

	// 1. Client sends pause true via JSON text
	pausePayload, _ := json.Marshal(map[string]interface{}{
		"type":     "pause",
		"isPaused": true,
	})
	if err := ws.WriteMessage(websocket.TextMessage, pausePayload); err != nil {
		t.Fatalf("failed to send pause message: %v", err)
	}

	select {
	case p := <-pauseEventChan:
		if !p {
			t.Errorf("expected pause true, got %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client pause event")
	}

	// 2. Client sends pause false via 2-byte binary message [0x50, 0x00]
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte{0x50, 0x00}); err != nil {
		t.Fatalf("failed to send binary pause message: %v", err)
	}

	select {
	case p := <-pauseEventChan:
		if p {
			t.Errorf("expected pause false, got %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for binary pause event")
	}

	// 3. Server broadcasts pause true to client
	srv.BroadcastPause(true)

	// Wait for broadcast messages (binary 2-byte and text JSON)
	sawBinary := false
	sawText := false
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for !sawBinary || !sawText {
		msgType, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read broadcast message: %v", err)
		}
		if string(msg) == "PING" {
			continue
		}
		if msgType == websocket.BinaryMessage && len(msg) == 2 && msg[0] == 0x50 {
			if msg[1] != 1 {
				t.Errorf("expected binary pause 1, got %d", msg[1])
			}
			sawBinary = true
			continue
		}
		var parsed struct {
			Type     string `json:"type"`
			IsPaused bool   `json:"isPaused"`
		}
		if err := json.Unmarshal(msg, &parsed); err == nil && parsed.Type == "pause" {
			if !parsed.IsPaused {
				t.Errorf("expected isPaused=true, got %v", parsed.IsPaused)
			}
			sawText = true
		}
	}
}

func TestServer_APIPauseEndpoint(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	pauseState := false
	srv.GetIsPaused = func() bool { return pauseState }
	srv.OnClientPause = func(p bool) { pauseState = p }

	// 1. GET /api/pause -> returns isPaused: false
	req := httptest.NewRequest("GET", "/api/pause", nil)
	w := httptest.NewRecorder()
	srv.handleAPIPause(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	var getResp struct {
		IsPaused bool `json:"isPaused"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&getResp)
	if getResp.IsPaused != false {
		t.Errorf("expected isPaused=false, got %v", getResp.IsPaused)
	}

	// 2. POST /api/pause -> sets isPaused: true
	postReq := httptest.NewRequest("POST", "/api/pause", strings.NewReader(`{"isPaused":true}`))
	wPost := httptest.NewRecorder()
	srv.handleAPIPause(wPost, postReq)
	respPost := wPost.Result()
	var postResp struct {
		IsPaused bool `json:"isPaused"`
	}
	_ = json.NewDecoder(respPost.Body).Decode(&postResp)
	if postResp.IsPaused != true || pauseState != true {
		t.Errorf("expected isPaused=true after POST, got resp=%v, state=%v", postResp.IsPaused, pauseState)
	}
}

func TestServer_QuaternionToEuler(t *testing.T) {
	// 1. Identity quaternion (0, 0, 0, 1) -> all zeros
	pitch, roll, yaw := QuaternionToEuler(0, 0, 0, 1)
	if math.Abs(pitch) > 0.01 || math.Abs(roll) > 0.01 || math.Abs(yaw) > 0.01 {
		t.Errorf("expected near zero angles, got P:%.2f R:%.2f Y:%.2f", pitch, roll, yaw)
	}

	// 2. Pitch tilt forward (+20 deg): rotation around X
	deg20 := 20.0 * math.Pi / 180.0
	qx := float32(math.Sin(deg20 / 2))
	qw := float32(math.Cos(deg20 / 2))
	p, r, y := QuaternionToEuler(qx, 0, 0, qw)
	if math.Abs(p-20.0) > 0.1 || math.Abs(r) > 0.1 || math.Abs(y) > 0.1 {
		t.Errorf("pitch test failed: expected P:20.0 R:0 Y:0, got P:%.2f R:%.2f Y:%.2f", p, r, y)
	}

	// 3. Pitch tilt backward (-20 deg): rotation around X
	degNeg20 := -20.0 * math.Pi / 180.0
	qxNeg := float32(math.Sin(degNeg20 / 2))
	qwNeg := float32(math.Cos(degNeg20 / 2))
	pNeg, _, _ := QuaternionToEuler(qxNeg, 0, 0, qwNeg)
	if math.Abs(pNeg-(-20.0)) > 0.1 {
		t.Errorf("pitch backward failed: expected P:-20.0, got P:%.2f", pNeg)
	}

	// 4. Roll tilt right (+15 deg): phone tilts right, outQz is negative (-sin(15/2))
	deg15 := 15.0 * math.Pi / 180.0
	qz := float32(-math.Sin(deg15 / 2)) // Cemuhook frame inverts roll axis
	qw15 := float32(math.Cos(deg15 / 2))
	pRoll, rRoll, yRoll := QuaternionToEuler(0, 0, qz, qw15)
	if math.Abs(rRoll-15.0) > 0.1 || math.Abs(pRoll) > 0.1 || math.Abs(yRoll) > 0.1 {
		t.Errorf("roll right test failed: expected R:+15.0, got P:%.2f R:%.2f Y:%.2f", pRoll, rRoll, yRoll)
	}

	// 5. Yaw (+45 deg): clockwise turn around Y axis (qy is negative in device orientation)
	deg45 := 45.0 * math.Pi / 180.0
	qy := float32(-math.Sin(deg45 / 2))
	qw45 := float32(math.Cos(deg45 / 2))
	pYaw, rYaw, yYaw := QuaternionToEuler(0, qy, 0, qw45)
	if math.Abs(yYaw-45.0) > 0.1 || math.Abs(pYaw) > 0.1 || math.Abs(rYaw) > 0.1 {
		t.Errorf("yaw test failed: expected Y:45.0, got P:%.2f R:%.2f Y:%.2f", pYaw, rYaw, yYaw)
	}
}

