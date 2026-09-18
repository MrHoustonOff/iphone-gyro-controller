package server

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"gyrobridge/pkg/ca"
)

// MotionFrame represents telemetry received from mobile device sensors (46-byte binary payload).
type MotionFrame struct {
	Timestamp uint32  `json:"ts"`
	// Angular velocity in °/s for Cemuhook DSU
	RotX float32 `json:"rx"`
	RotY float32 `json:"ry"`
	RotZ float32 `json:"rz"`
	// Unit Quaternion (X, Y, Z, W) for SLERP interpolation and 3D visualization without Gimbal Lock
	Qx float32 `json:"qx"`
	Qy float32 `json:"qy"`
	Qz float32 `json:"qz"`
	Qw float32 `json:"qw"`
	// Acceleration in g (1g = 9.80665 m/s²) for Cemuhook DSU
	AccX float32 `json:"ax"`
	AccY float32 `json:"ay"`
	AccZ float32 `json:"az"`
	// Buttons and control flags bitmask
	Buttons uint16 `json:"buttons"`
}

// Server encapsulates both HTTP (for certificate distribution) and HTTPS+WSS for gamepad traffic.
type Server struct {
	caManager   *ca.CertificateManager
	httpPort    int
	httpsPort   int
	webContent  []byte
	httpServer  *http.Server
	httpsServer *http.Server
	upgrader    websocket.Upgrader
	onFrame     func(frame MotionFrame)
	packetCount   atomic.Uint64
	currentHzBits atomic.Uint64
	activeClient  atomic.Int32
	stopChan      chan struct{}

	// HTTPMux and HTTPSMux are created at construction time so that external
	// modules (e.g. the 3D visualizer) can register additional routes before Start().
	HTTPMux  *http.ServeMux
	HTTPSMux *http.ServeMux

	// Lifecycle event hooks for clean, non-spammy logging
	OnClientConnect    func(remoteAddr string)
	OnClientDisconnect func(remoteAddr string)
	OnClientPause      func(isPaused bool)
	OnClientDevice     func(device string)
	OnClientVisibility func(visible bool)
	GetIsPaused        func() bool

	clientMu    sync.Mutex
	clientConns map[*websocket.Conn]*sync.Mutex
}

// NewServer initializes HTTP and HTTPS server instances.
func NewServer(caMgr *ca.CertificateManager, httpPort, httpsPort int, webHTML []byte, onFrame func(MotionFrame)) *Server {
	httpMux  := http.NewServeMux()
	httpsMux := http.NewServeMux()

	s := &Server{
		caManager:   caMgr,
		httpPort:    httpPort,
		httpsPort:   httpsPort,
		webContent:  webHTML,
		onFrame:     onFrame,
		stopChan:    make(chan struct{}),
		HTTPMux:     httpMux,
		HTTPSMux:    httpsMux,
		clientConns: make(map[*websocket.Conn]*sync.Mutex),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // allow LAN connections
			},
		},
	}

	// Register core routes
	httpMux.HandleFunc("/ca.mobileconfig", s.handleMobileConfig)
	httpMux.HandleFunc("/ca.crt", s.handleRawCACert)
	httpMux.HandleFunc("/api/pause", s.handleAPIPause)

	httpsMux.HandleFunc("/ca.mobileconfig", s.handleMobileConfig)
	httpsMux.HandleFunc("/ca.crt", s.handleRawCACert)
	httpsMux.HandleFunc("/api/pause", s.handleAPIPause)
	httpsMux.HandleFunc("/ws", s.handleWebSocket)
	httpsMux.HandleFunc("/", s.handleWebClient)

	// HTTP root catch-all: redirect to HTTPS (registered last so specific routes win)
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		target := fmt.Sprintf("https://%s:%d%s", r.URL.Hostname(), s.httpsPort, r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})

	return s
}

// Start launches both servers. All routes must be registered before calling Start.
func (s *Server) Start() error {
	tlsConfig := &tls.Config{
		Certificates:   []tls.Certificate{*s.caManager.LeafCert},
		GetCertificate: s.caManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.httpPort),
		Handler: s.HTTPMux,
	}
	s.httpsServer = &http.Server{
		Addr:      fmt.Sprintf(":%d", s.httpsPort),
		Handler:   s.HTTPSMux,
		TLSConfig: tlsConfig,
	}

	httpListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to bind HTTP port %d: %w", s.httpPort, err)
	}

	httpsListener, err := tls.Listen("tcp", s.httpsServer.Addr, tlsConfig)
	if err != nil {
		httpListener.Close()
		return fmt.Errorf("failed to bind HTTPS port %d: %w", s.httpsPort, err)
	}

	go s.httpServer.Serve(httpListener)
	go s.httpsServer.Serve(httpsListener)
	go s.rateMonitorLoop()

	return nil
}

// Stop gracefully shuts down both servers.
func (s *Server) Stop() {
	select {
	case <-s.stopChan:
	default:
		close(s.stopChan)
	}
	if s.httpServer != nil {
		s.httpServer.Close()
	}
	if s.httpsServer != nil {
		s.httpsServer.Close()
	}
}

func (s *Server) rateMonitorLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastPackets uint64
	lastTime := time.Now()

	for {
		select {
		case <-s.stopChan:
			return
		case now := <-ticker.C:
			curr := s.packetCount.Load()
			diff := curr - lastPackets
			elapsed := now.Sub(lastTime).Seconds()
			if elapsed > 0 {
				hz := float64(diff) / elapsed
				s.currentHzBits.Store(math.Float64bits(hz))
			}
			lastPackets = curr
			lastTime = now
		}
	}
}

func (s *Server) handleMobileConfig(w http.ResponseWriter, r *http.Request) {
	configBytes, err := s.caManager.GenerateMobileConfig()
	if err != nil {
		http.Error(w, "Failed to generate profile: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Mandatory Apple MIME-type to trigger "Profile Downloaded" sheet in Safari
	w.Header().Set("Content-Type", "application/x-apple-as-config")
	w.Header().Set("Content-Disposition", "attachment; filename=\"gyrobridge.mobileconfig\"")
	w.WriteHeader(http.StatusOK)
	w.Write(configBytes)
}

func (s *Server) handleRawCACert(w http.ResponseWriter, r *http.Request) {
	pemBytes := s.caManager.RootCertPEM()
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", "attachment; filename=\"gyrobridge-ca.crt\"")
	w.WriteHeader(http.StatusOK)
	w.Write(pemBytes)
}

func (s *Server) handleWebClient(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	w.Write(s.webContent)
}

const (
	wsReadDeadline = 30 * time.Second // connection dies if no client frame in this window
	wsPingInterval = 2 * time.Second  // server→client keepalive ping interval
	wsPingText     = "PING"           // client listens for this and resets its own watchdog
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.EnableWriteCompression(false)

	// Optimize underlying TCP connection for ultra-low latency & keepalive.
	// For WSS connections, conn.UnderlyingConn() is *tls.Conn, which wraps the raw *net.TCPConn.
	rawConn := conn.UnderlyingConn()
	if tlsConn, ok := rawConn.(*tls.Conn); ok {
		rawConn = tlsConn.NetConn()
	}
	if tcpConn, ok := rawConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second)
		_ = tcpConn.SetReadBuffer(64 * 1024)
		_ = tcpConn.SetWriteBuffer(64 * 1024)
	}

	remoteAddr := r.RemoteAddr
	s.activeClient.Add(1)
	if s.OnClientConnect != nil {
		s.OnClientConnect(remoteAddr)
	}
	if dev := r.URL.Query().Get("device"); dev != "" && s.OnClientDevice != nil {
		s.OnClientDevice(dev)
	}

	writeMu := &sync.Mutex{}
	s.clientMu.Lock()
	s.clientConns[conn] = writeMu
	s.clientMu.Unlock()

	defer func() {
		s.clientMu.Lock()
		delete(s.clientConns, conn)
		s.clientMu.Unlock()

		s.activeClient.Add(-1)
		if s.OnClientDisconnect != nil {
			s.OnClientDisconnect(remoteAddr)
		}
	}()

	// Send current pause state immediately upon connection (dual binary + text)
	if s.GetIsPaused != nil {
		isPaused := s.GetIsPaused()
		initMsg, _ := json.Marshal(map[string]interface{}{
			"type":     "pause",
			"isPaused": isPaused,
		})
		var pByte byte = 0
		if isPaused {
			pByte = 1
		}
		writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{0x50, pByte})
		_ = conn.WriteMessage(websocket.TextMessage, initMsg)
		writeMu.Unlock()
	}

	// Arm initial read deadline
	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

	// Server→client keepalive goroutine.
	// Sends "PING" text frames so the client can detect server-side silence
	// independent of the OS TCP keepalive timer (~2 min default).
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := conn.WriteMessage(websocket.TextMessage, []byte(wsPingText))
				writeMu.Unlock()
				if err != nil {
					return // connection dead, exit silently; defer conn.Close() handles cleanup
				}
			}
		}
	}()

	// Read telemetry frames (binary or JSON)
	lastDeadlineRenew := time.Now()
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		// Refresh read deadline periodically (every 2s) instead of every frame
		// Eliminates ~100 redundant timer/syscalls per second while streaming!
		now := time.Now()
		if now.Sub(lastDeadlineRenew) >= 2*time.Second {
			_ = conn.SetReadDeadline(now.Add(wsReadDeadline))
			lastDeadlineRenew = now
		}

		// Check for client keepalive PONG
		if msgType == websocket.TextMessage && string(message) == "PONG" {
			_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
			continue
		}

		// Check for binary control messages (e.g. 2-byte [0x50, 0x01/0x00])
		if msgType == websocket.BinaryMessage && len(message) == 2 && message[0] == 0x50 {
			if s.OnClientPause != nil {
				s.OnClientPause(message[1] == 1)
			}
			continue
		}

		// Check for text control messages (e.g. phone clicked pause/resume or reported device model)
		if msgType == websocket.TextMessage {
			var ctrl struct {
				Type     string `json:"type"`
				IsPaused bool   `json:"isPaused"`
				Model    string `json:"model"`
				Visible  *bool  `json:"visible"`
			}
			if err := json.Unmarshal(message, &ctrl); err == nil {
				if ctrl.Type == "pause" && s.OnClientPause != nil {
					s.OnClientPause(ctrl.IsPaused)
					continue
				}
				if ctrl.Type == "device" && ctrl.Model != "" && s.OnClientDevice != nil {
					s.OnClientDevice(ctrl.Model)
					continue
				}
				if ctrl.Type == "visibility" && ctrl.Visible != nil && s.OnClientVisibility != nil {
					s.OnClientVisibility(*ctrl.Visible)
					continue
				}
			}
		}

		frame, ok := s.parseFrame(msgType, message)
		if !ok {
			continue
		}

		s.packetCount.Add(1)

		if s.onFrame != nil {
			s.onFrame(frame)
		}
	}
}

func (s *Server) parseFrame(msgType int, data []byte) (MotionFrame, bool) {
	// Fast binary decoding (46 bytes: uint32 ts, 3x float32 rotRate, 4x float32 quat, 3x float32 accel, uint16 buttons)
	if msgType == websocket.BinaryMessage && len(data) >= 46 {
		ts := binary.LittleEndian.Uint32(data[0:4])
		rx := math.Float32frombits(binary.LittleEndian.Uint32(data[4:8]))
		ry := math.Float32frombits(binary.LittleEndian.Uint32(data[8:12]))
		rz := math.Float32frombits(binary.LittleEndian.Uint32(data[12:16]))

		qx := math.Float32frombits(binary.LittleEndian.Uint32(data[16:20]))
		qy := math.Float32frombits(binary.LittleEndian.Uint32(data[20:24]))
		qz := math.Float32frombits(binary.LittleEndian.Uint32(data[24:28]))
		qw := math.Float32frombits(binary.LittleEndian.Uint32(data[28:32]))

		ax := math.Float32frombits(binary.LittleEndian.Uint32(data[32:36]))
		ay := math.Float32frombits(binary.LittleEndian.Uint32(data[36:40]))
		az := math.Float32frombits(binary.LittleEndian.Uint32(data[40:44]))

		buttons := binary.LittleEndian.Uint16(data[44:46])

		return MotionFrame{
			Timestamp: ts,
			RotX:      rx,
			RotY:      ry,
			RotZ:      rz,
			Qx:        qx,
			Qy:        qy,
			Qz:        qz,
			Qw:        qw,
			AccX:      ax,
			AccY:      ay,
			AccZ:      az,
			Buttons:   buttons,
		}, true
	}

	// JSON fallback
	var frame MotionFrame
	if err := json.Unmarshal(data, &frame); err == nil {
		return frame, true
	}

	return frame, false
}

// PacketStats returns total received packets, current active connections, and current polling rate in Hz.
func (s *Server) PacketStats() (uint64, int32, float64) {
	total := s.packetCount.Load()
	clients := s.activeClient.Load()
	hz := math.Float64frombits(s.currentHzBits.Load())
	return total, clients, hz
}

// BroadcastPause sends a pause state notification to all currently connected WebSocket clients.
func (s *Server) BroadcastPause(isPaused bool) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	msg, _ := json.Marshal(map[string]interface{}{
		"type":     "pause",
		"isPaused": isPaused,
	})

	var pByte byte = 0
	if isPaused {
		pByte = 1
	}
	binMsg := []byte{0x50, pByte}

	for conn, mu := range s.clientConns {
		mu.Lock()
		conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		_ = conn.WriteMessage(websocket.BinaryMessage, binMsg)
		_ = conn.WriteMessage(websocket.TextMessage, msg)
		mu.Unlock()
	}
}

// QuaternionToEuler converts Cemuhook SO(3) unit quaternion (Qx=Pitch, Qy=Yaw, Qz=-Roll, Qw=W)
// to intuitive Euler angles (Pitch, Roll, Yaw) in degrees:
// - Pitch > 0: phone tilted forward (nose down); Pitch < 0: phone tilted backward (nose up)
// - Roll  > 0: phone tilted right; Roll < 0: phone tilted left
// - Yaw: rotation around vertical Y axis (compass heading / spin on table)
func QuaternionToEuler(qx, qy, qz, qw float32) (pitch, roll, yaw float64) {
	// Pitch (rotation around X axis: forward/backward tilt)
	sinp := 2 * (float64(qw)*float64(qx) - float64(qy)*float64(qz))
	if math.Abs(sinp) >= 1 {
		pitch = math.Copysign(90.0, sinp)
	} else {
		pitch = math.Asin(sinp) * 180 / math.Pi
	}

	// Roll (rotation around Z axis: left/right tilt; positive = tilt right)
	sinr := 2 * (float64(qw)*float64(qz) + float64(qx)*float64(qy))
	cosr := 1 - 2*(float64(qz)*float64(qz) + float64(qx)*float64(qx))
	roll = -math.Atan2(sinr, cosr) * 180 / math.Pi

	// Yaw (rotation around Y axis: spin on table; positive = turning right)
	siny := 2 * (float64(qw)*float64(qy) + float64(qx)*float64(qz))
	cosy := 1 - 2*(float64(qx)*float64(qx) + float64(qy)*float64(qy))
	yaw = -math.Atan2(siny, cosy) * 180 / math.Pi

	return pitch, roll, yaw
}

// handleAPIPause provides a REST endpoint for checking and toggling pause state.
// Acts as a failsafe layer alongside WebSockets for mobile browsers.
func (s *Server) handleAPIPause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			IsPaused bool `json:"isPaused"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if s.OnClientPause != nil {
				s.OnClientPause(body.IsPaused)
			}
			s.BroadcastPause(body.IsPaused)
		}
	}

	isPaused := false
	if s.GetIsPaused != nil {
		isPaused = s.GetIsPaused()
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"isPaused": isPaused,
	})
}
