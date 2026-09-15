package server

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"gyrobridge/internal/ca"
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
	caManager     *ca.CertificateManager
	httpPort      int
	httpsPort     int
	webContent    []byte
	httpServer    *http.Server
	httpsServer   *http.Server
	upgrader      websocket.Upgrader
	onFrame       func(frame MotionFrame)
	packetCount   atomic.Uint64
	currentHzBits atomic.Uint64
	activeClient  atomic.Int32
	stopChan      chan struct{}
}

// NewServer initializes HTTP and HTTPS server instances.
func NewServer(caMgr *ca.CertificateManager, httpPort, httpsPort int, webHTML []byte, onFrame func(MotionFrame)) *Server {
	s := &Server{
		caManager:  caMgr,
		httpPort:   httpPort,
		httpsPort:  httpsPort,
		webContent: webHTML,
		onFrame:    onFrame,
		stopChan:   make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // allow LAN connections
			},
		},
	}

	return s
}

// Start launches both servers.
func (s *Server) Start() error {
	// 1. Plain HTTP server for iOS .mobileconfig and raw CA distribution
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/ca.mobileconfig", s.handleMobileConfig)
	httpMux.HandleFunc("/ca.crt", s.handleRawCACert)
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Redirect root HTTP requests to HTTPS
		target := fmt.Sprintf("https://%s:%d%s", r.URL.Hostname(), s.httpsPort, r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.httpPort),
		Handler: httpMux,
	}

	// 2. HTTPS + WSS server for secure web client and sensor streaming
	httpsMux := http.NewServeMux()
	httpsMux.HandleFunc("/ca.mobileconfig", s.handleMobileConfig)
	httpsMux.HandleFunc("/ca.crt", s.handleRawCACert)
	httpsMux.HandleFunc("/ws", s.handleWebSocket)
	httpsMux.HandleFunc("/", s.handleWebClient)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*s.caManager.LeafCert},
		MinVersion:   tls.VersionTLS12,
	}

	s.httpsServer = &http.Server{
		Addr:      fmt.Sprintf(":%d", s.httpsPort),
		Handler:   httpsMux,
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
	w.WriteHeader(http.StatusOK)
	w.Write(s.webContent)
}

const (
	wsReadDeadline = 10 * time.Second // connection dies if no client frame in this window
	wsPingInterval = 2 * time.Second  // server→client keepalive ping interval
	wsPingText     = "PING"           // client listens for this and resets its own watchdog
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	s.activeClient.Add(1)
	defer s.activeClient.Add(-1)

	// Arm read deadline — refreshed on every incoming frame.
	// If the phone goes silent (network drop, Safari backgrounded, etc.)
	// this will unblock ReadMessage and cleanly close the connection,
	// triggering the client's onclose→reconnect path.
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
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, []byte(wsPingText)); err != nil {
					return // connection dead, exit silently; defer conn.Close() handles cleanup
				}
			}
		}
	}()

	// Read telemetry frames (binary or JSON)
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}
		// Fresh lease: client is alive, extend deadline
		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

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
