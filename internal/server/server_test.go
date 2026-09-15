package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gyrobridge/internal/ca"
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
		Timestamp: time.Now().UnixMilli(),
		Alpha:     12.34,
		Beta:      -45.67,
		Gamma:     89.01,
		AccX:      0.1,
		AccY:      9.8,
		AccZ:      0.2,
	}

	data, _ := json.Marshal(testFrame)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	select {
	case received := <-frameChan:
		if received.Alpha != testFrame.Alpha || received.Beta != testFrame.Beta {
			t.Errorf("frame mismatch: got %+v, want %+v", received, testFrame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for motion frame")
	}

	pkts, _ := srv.PacketStats()
	if pkts != 1 {
		t.Errorf("expected 1 packet, got %d", pkts)
	}
}
