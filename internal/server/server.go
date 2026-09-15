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
	"gyrobridge/internal/ca"
)

// MotionFrame represents telemetry received from mobile device sensors.
type MotionFrame struct {
	Timestamp int64   `json:"ts"`
	Alpha     float64 `json:"alpha"`
	Beta      float64 `json:"beta"`
	Gamma     float64 `json:"gamma"`
	AccX      float64 `json:"ax"`
	AccY      float64 `json:"ay"`
	AccZ      float64 `json:"az"`
}

// Server encapsulates both HTTP (for certificate distribution) and HTTPS+WSS for gamepad traffic.
type Server struct {
	caManager       *ca.CertificateManager
	httpPort        int
	httpsPort       int
	webContent      []byte
	httpServer      *http.Server
	httpsServer     *http.Server
	upgrader        websocket.Upgrader
	onFrame         func(frame MotionFrame)
	packetCount     uint64
	packetsInWindow uint64
	lastHzTime      time.Time
	currentHz       float64
	activeClient    atomic.Int32
	hzMu            sync.RWMutex
}

// NewServer initializes HTTP and HTTPS server instances.
func NewServer(caMgr *ca.CertificateManager, httpPort, httpsPort int, webHTML []byte, onFrame func(MotionFrame)) *Server {
	s := &Server{
		caManager:  caMgr,
		httpPort:   httpPort,
		httpsPort:  httpsPort,
		webContent: webHTML,
		onFrame:    onFrame,
		upgrader: websocket.Upgrader{
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

	return nil
}

// Stop gracefully shuts down both servers.
func (s *Server) Stop() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
	if s.httpsServer != nil {
		s.httpsServer.Close()
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

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	s.activeClient.Add(1)
	defer s.activeClient.Add(-1)

	// Read telemetry frames (binary or JSON)
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		frame, ok := s.parseFrame(msgType, message)
		if !ok {
			continue
		}

		total := atomic.AddUint64(&s.packetCount, 1)

		// Frequency calculation window (every 500ms)
		s.hzMu.Lock()
		s.packetsInWindow++
		now := time.Now()
		elapsed := now.Sub(s.lastHzTime)
		if elapsed >= 500*time.Millisecond {
			if s.lastHzTime.IsZero() {
				s.lastHzTime = now
				s.packetsInWindow = 0
			} else {
				s.currentHz = float64(s.packetsInWindow) / elapsed.Seconds()
				s.packetsInWindow = 0
				s.lastHzTime = now
			}
		}
		s.hzMu.Unlock()

		_ = total
		if s.onFrame != nil {
			s.onFrame(frame)
		}
	}
}

func (s *Server) parseFrame(msgType int, data []byte) (MotionFrame, bool) {
	// Fast binary decoding (32 bytes: uint64 ts, 6x float32)
	if msgType == websocket.BinaryMessage && len(data) >= 32 {
		ts := int64(binary.LittleEndian.Uint64(data[0:8]))
		alpha := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[8:12])))
		beta := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[12:16])))
		gamma := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[16:20])))
		ax := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[20:24])))
		ay := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[24:28])))
		az := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[28:32])))

		return MotionFrame{
			Timestamp: ts,
			Alpha:     alpha,
			Beta:      beta,
			Gamma:     gamma,
			AccX:      ax,
			AccY:      ay,
			AccZ:      az,
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
	total := atomic.LoadUint64(&s.packetCount)
	clients := s.activeClient.Load()
	s.hzMu.RLock()
	hz := s.currentHz
	s.hzMu.RUnlock()
	return total, clients, hz
}
