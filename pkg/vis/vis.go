// Package vis provides a real-time 3D orientation visualizer for GyroBridge.
//
// Architecture:
//
//	Phone → WSS → Server → DSU
//	                     ↘ vis.Broadcaster → ws://localhost/vis/ws → PC Browser (Three.js)
//
// The Broadcaster accepts MotionFrames via Publish() and fans them out to all
// connected browser viewers over plain WebSocket (no TLS — viewers run on localhost).
// The Three.js HTML page is embedded at build time and served at /vis.
package vis

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Frame is the subset of sensor data needed by the 3D visualizer.
// Kept minimal to reduce marshal overhead at 60 Hz.
type Frame struct {
	Qx float32 `json:"qx"`
	Qy float32 `json:"qy"`
	Qz float32 `json:"qz"`
	Qw float32 `json:"qw"`
	Rx float32 `json:"rx"` // Gyro °/s
	Ry float32 `json:"ry"`
	Rz float32 `json:"rz"`
	Ax float32 `json:"ax"` // Accel g
	Ay float32 `json:"ay"`
	Az float32 `json:"az"`
}

// Broadcaster receives motion frames and streams them to all connected browser viewers.
type Broadcaster struct {
	mu       sync.RWMutex
	clients  map[*websocket.Conn]struct{}
	upgrader websocket.Upgrader
	htmlPage []byte
	frameCh  chan Frame
	stopCh   chan struct{}
}

// New creates a Broadcaster that serves the given HTML page at /vis.
func New(htmlPage []byte) *Broadcaster {
	b := &Broadcaster{
		clients:  make(map[*websocket.Conn]struct{}),
		htmlPage: htmlPage,
		frameCh:  make(chan Frame, 2),
		stopCh:   make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  256,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // localhost only in practice
			},
		},
	}
	go b.worker()
	return b
}

// Stop cleanly shuts down the broadcaster worker and active viewer connections.
func (b *Broadcaster) Stop() {
	if b == nil {
		return
	}
	select {
	case <-b.stopCh:
		return
	default:
		close(b.stopCh)
	}

	b.mu.Lock()
	for conn := range b.clients {
		_ = conn.Close()
	}
	b.clients = make(map[*websocket.Conn]struct{})
	b.mu.Unlock()
}

// Register wires the broadcaster into the given HTTP mux at the given base path.
// Registers:
//
//	GET  <base>      → Three.js HTML page
//	GET  <base>/ws   → WebSocket data stream
//
// Designed to be called on the Server's HTTPMux (plain HTTP, no cert) so the
// PC browser can open it without certificate hassle.
func (b *Broadcaster) Register(mux *http.ServeMux, base string) {
	if b == nil {
		return
	}
	mux.HandleFunc(base, b.serveHTML)
	mux.HandleFunc(base+"/ws", b.serveWS)
}

// Publish queues frame for asynchronous broadcast.
// Non-blocking: if the broadcaster worker is busy, the frame is dropped immediately,
// ensuring the sensor/gamepad hot path is NEVER stalled by the 3D visualizer.
func (b *Broadcaster) Publish(f Frame) {
	if b == nil {
		return
	}
	select {
	case b.frameCh <- f:
	default:
		// Worker is busy broadcasting: drop frame to preserve sensor throughput
	}
}

func (b *Broadcaster) worker() {
	for {
		select {
		case <-b.stopCh:
			return
		case f := <-b.frameCh:
			b.broadcast(f)
		}
	}
}

func (b *Broadcaster) broadcast(f Frame) {
	b.mu.RLock()
	if len(b.clients) == 0 {
		b.mu.RUnlock()
		return
	}
	clients := make([]*websocket.Conn, 0, len(b.clients))
	for conn := range b.clients {
		clients = append(clients, conn)
	}
	b.mu.RUnlock()

	msg, err := json.Marshal(f)
	if err != nil {
		return
	}

	deadline := time.Now().Add(16 * time.Millisecond) // 1-frame write budget
	var dead []*websocket.Conn
	for _, conn := range clients {
		conn.SetWriteDeadline(deadline)
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			dead = append(dead, conn)
		}
	}

	if len(dead) > 0 {
		b.mu.Lock()
		for _, conn := range dead {
			delete(b.clients, conn)
			conn.Close()
		}
		b.mu.Unlock()
	}
}

// ViewerCount returns the number of currently connected browser viewers.
func (b *Broadcaster) ViewerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// serveHTML serves the embedded Three.js visualization page.
func (b *Broadcaster) serveHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(b.htmlPage)
}

// serveWS upgrades the connection and keeps it alive, reading ping/close frames.
// Writing is handled exclusively via Publish() from the sensor pipeline goroutine.
func (b *Broadcaster) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	b.mu.Lock()
	b.clients[conn] = struct{}{}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.clients, conn)
		b.mu.Unlock()
	}()

	// Keep the read loop alive so that close/ping frames are handled.
	// Actual data is pushed by Publish() — this goroutine just waits.
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
