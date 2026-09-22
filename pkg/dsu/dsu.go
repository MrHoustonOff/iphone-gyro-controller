package dsu

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gyrobridge/pkg/server"
)

const (
	DefaultPort = 26760
	MagicServer = "DSUS"
	MagicClient = "DSUC"
	ProtocolVer = 1001

	MsgTypeVersion  = 0x100000
	MsgTypeListPorts = 0x100001
	MsgTypePadData  = 0x100002

	SlotStateDisconnected = 0
	SlotStateConnected    = 2
	ModelFullGamepad      = 2
	ConnTypeBluetooth     = 2
	BatteryFull           = 5
)

// ClientSub represents an active client (Cemu / PadTest / Dolphin) subscribed to motion stream.
type ClientSub struct {
	Addr     *net.UDPAddr
	LastSeen time.Time
}

var padPacketPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 100)
		return &b
	},
}

// Server implements Cemuhook DSU motion protocol over UDP.
type Server struct {
	port          int
	conn          *net.UDPConn
	serverID      uint32
	packetCounter uint32
	macAddr       [6]byte

	clientsMu sync.RWMutex
	clients   map[string]*ClientSub

	lastFrameMu    sync.RWMutex
	lastMotionTime time.Time
	lastFrame      server.MotionFrame

	stopChan chan struct{}
	running  atomic.Bool

	// Lifecycle event hooks for clean, non-spammy logging
	OnClientConnect    func(addr *net.UDPAddr)
	OnClientDisconnect func(addr *net.UDPAddr)
}

// GenerateRandomMAC generates a randomized 6-byte Cemuhook MAC with standard prefix 00:13:37.
func GenerateRandomMAC() [6]byte {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return [6]byte{0x00, 0x13, 0x37, byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256))}
}

// NewServer creates a new Cemuhook DSU server.
// If port < 0, it defaults to 26760. If port == 0, OS allocates a dynamic port.
// Optional mac parameter specifies the fixed MAC address. If omitted or zero, a random MAC is generated.
func NewServer(port int, mac ...[6]byte) *Server {
	if port < 0 {
		port = DefaultPort
	}

	var m [6]byte
	if len(mac) > 0 && mac[0] != [6]byte{} {
		m = mac[0]
	} else {
		m = GenerateRandomMAC()
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	s := &Server{
		port:     port,
		serverID: r.Uint32(),
		macAddr:  m,
		clients:  make(map[string]*ClientSub),
		stopChan: make(chan struct{}),
	}

	return s
}

// SetMACAddress dynamically updates the server MAC address.
func (s *Server) SetMACAddress(mac [6]byte) {
	s.clientsMu.Lock()
	s.macAddr = mac
	s.clientsMu.Unlock()
}

// MACAddress returns current server MAC address.
func (s *Server) MACAddress() [6]byte {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.macAddr
}

// ClientInfo describes a connected DSU emulator client with detailed network metrics.
type ClientInfo struct {
	Address    string `json:"address"`
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	LastSeenMs int64  `json:"lastSeenMs"`
	Active     bool   `json:"active"`
}

// ActiveClientCount returns the number of currently active DSU subscribers.
func (s *Server) ActiveClientCount() int {
	if s == nil {
		return 0
	}
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// GetClientsInfo returns a snapshot list of currently subscribed emulator clients.
func (s *Server) GetClientsInfo() []ClientInfo {
	if s == nil {
		return nil
	}
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	now := time.Now()
	res := make([]ClientInfo, 0, len(s.clients))
	for _, c := range s.clients {
		ms := now.Sub(c.LastSeen).Milliseconds()
		res = append(res, ClientInfo{
			Address:    c.Addr.String(),
			IP:         c.Addr.IP.String(),
			Port:       c.Addr.Port,
			LastSeenMs: ms,
			Active:     ms < 3500,
		})
	}
	return res
}

// Start opens UDP socket and begins listening for Cemu requests.
func (s *Server) Start() error {
	addr := &net.UDPAddr{Port: s.port, IP: net.IPv4zero}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind DSU UDP port %d: %w", s.port, err)
	}

	_ = conn.SetReadBuffer(64 * 1024)
	_ = conn.SetWriteBuffer(64 * 1024)

	s.conn = conn
	s.running.Store(true)

	go s.listenLoop()
	go s.cleanupLoop()
	go s.heartbeatLoop()

	return nil
}

// Stop terminates the DSU server.
func (s *Server) Stop() {
	if s.running.CompareAndSwap(true, false) {
		close(s.stopChan)
		if s.conn != nil {
			s.conn.Close()
		}
	}
}

// SendMotion converts MotionFrame into a 100-byte Cemuhook DSU packet and sends to all active clients.
func (s *Server) SendMotion(frame server.MotionFrame) {
	if !s.running.Load() {
		return
	}

	s.lastFrameMu.Lock()
	s.lastFrame = frame
	s.lastMotionTime = time.Now()
	s.lastFrameMu.Unlock()

	s.clientsMu.RLock()
	if len(s.clients) == 0 {
		s.clientsMu.RUnlock()
		return // Fast path: zero emulators subscribed, zero allocations, zero packet work!
	}

	packetNum := atomic.AddUint32(&s.packetCounter, 1)

	// Acquire pooled buffer (Zero heap allocations in the hot path!)
	bufPtr := padPacketPool.Get().(*[]byte)
	pkt := *bufPtr
	s.fillPadDataPacket(pkt, packetNum, frame)

	for _, client := range s.clients {
		_, _ = s.conn.WriteToUDP(pkt, client.Addr)
	}
	s.clientsMu.RUnlock()

	padPacketPool.Put(bufPtr)
}

func (s *Server) fillPadDataPacket(buf []byte, packetNum uint32, frame server.MotionFrame) {
	// Total size: 100 bytes (20 bytes header + 80 bytes payload)
	// --- 1. Header (20 bytes) ---
	copy(buf[0:4], MagicServer)
	binary.LittleEndian.PutUint16(buf[4:6], ProtocolVer)
	binary.LittleEndian.PutUint16(buf[6:8], 80+4) // length: payload (80) + msgType (4)
	binary.LittleEndian.PutUint32(buf[8:12], 0)   // CRC32 initialized to 0
	binary.LittleEndian.PutUint32(buf[12:16], s.serverID)
	binary.LittleEndian.PutUint32(buf[16:20], MsgTypePadData)

	// --- 2. Payload (80 bytes, offsets relative to 20) ---
	p := buf[20:]
	p[0] = 0                    // Slot 0
	p[1] = SlotStateConnected  // 2 = Connected
	p[2] = ModelFullGamepad    // 2 = Full Gyro Gamepad
	p[3] = ConnTypeBluetooth   // 2 = Bluetooth / Wireless
	copy(p[4:10], s.macAddr[:]) // MAC Address
	p[10] = BatteryFull         // 5 = Full Battery
	p[11] = 1                   // Active state

	binary.LittleEndian.PutUint32(p[12:16], packetNum)

	// Buttons & Joysticks (centered at 128)
	p[20] = 128 // Left Stick X
	p[21] = 128 // Left Stick Y
	p[22] = 128 // Right Stick X
	p[23] = 128 // Right Stick Y

	// Timestamp in microseconds (offset 48..56)
	micros := uint64(time.Now().UnixNano() / 1000)
	binary.LittleEndian.PutUint64(p[48:56], micros)

	// Accelerometer in g: AccX, AccY, AccZ (offsets 56..68)
	binary.LittleEndian.PutUint32(p[56:60], float32ToBits(frame.AccX))
	binary.LittleEndian.PutUint32(p[60:64], float32ToBits(frame.AccY))
	binary.LittleEndian.PutUint32(p[64:68], float32ToBits(frame.AccZ))

	// Gyroscope in °/s: Pitch (X), Yaw (Y), Roll (Z) (offsets 68..80)
	binary.LittleEndian.PutUint32(p[68:72], float32ToBits(frame.RotX))
	binary.LittleEndian.PutUint32(p[72:76], float32ToBits(frame.RotY))
	binary.LittleEndian.PutUint32(p[76:80], float32ToBits(frame.RotZ))

	// --- 3. Compute IEEE 802.3 CRC32 over entire 100 bytes ---
	crc := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(buf[8:12], crc)
}

// BuildPadDataPacket builds and returns a newly allocated 100-byte packet (useful for tests).
func (s *Server) BuildPadDataPacket(packetNum uint32, frame server.MotionFrame) []byte {
	buf := make([]byte, 100)
	s.fillPadDataPacket(buf, packetNum, frame)
	return buf
}

// LastMotionFrame returns the latest received telemetry frame.
func (s *Server) LastMotionFrame() server.MotionFrame {
	s.lastFrameMu.RLock()
	defer s.lastFrameMu.RUnlock()
	return s.lastFrame
}

func (s *Server) listenLoop() {
	buf := make([]byte, 1024)

	for s.running.Load() {
		n, remoteAddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if !s.running.Load() {
				return
			}
			continue
		}

		if n < 16 {
			continue
		}

		// Fast 32-bit integer magic header check ("DSUC" = 0x44535543)
		if binary.BigEndian.Uint32(buf[0:4]) != 0x44535543 {
			continue
		}

		// Verify incoming CRC32
		expectedCRC := binary.LittleEndian.Uint32(buf[8:12])
		binary.LittleEndian.PutUint32(buf[8:12], 0)
		calculatedCRC := crc32.ChecksumIEEE(buf[:n])
		if expectedCRC != calculatedCRC {
			continue
		}

		if n < 20 {
			continue
		}

		msgType := binary.LittleEndian.Uint32(buf[16:20])
		s.handleRequest(msgType, buf[20:n], remoteAddr)
	}
}

func (s *Server) handleRequest(msgType uint32, payload []byte, remoteAddr *net.UDPAddr) {
	// Register / keepalive client subscription
	s.touchClient(remoteAddr)

	switch msgType {
	case MsgTypeVersion:
		s.sendVersionRsp(remoteAddr)
	case MsgTypeListPorts:
		s.sendPortInfoRsp(remoteAddr, payload)
	case MsgTypePadData:
		// Client is actively querying / subscribing to pad data.
		// Send the latest frame immediately as an acknowledgment!
		s.sendLatestPadDataTo(remoteAddr)
	}
}

func (s *Server) sendLatestPadDataTo(remoteAddr *net.UDPAddr) {
	if !s.running.Load() {
		return
	}

	s.lastFrameMu.RLock()
	frame := s.lastFrame
	s.lastFrameMu.RUnlock()

	// If no motion frame has ever been received, provide neutral gravity down
	if frame.AccX == 0 && frame.AccY == 0 && frame.AccZ == 0 {
		frame.AccY = -1.0
	}

	packetNum := atomic.AddUint32(&s.packetCounter, 1)

	bufPtr := padPacketPool.Get().(*[]byte)
	pkt := *bufPtr
	s.fillPadDataPacket(pkt, packetNum, frame)

	_, _ = s.conn.WriteToUDP(pkt, remoteAddr)

	padPacketPool.Put(bufPtr)
}

// heartbeatLoop maintains active DSU client subscriptions when no live motion stream is flowing.
// When an emulator is subscribed but no device is transmitting, it emits a neutral frame at 60 Hz.
func (s *Server) heartbeatLoop() {
	ticker := time.NewTicker(16 * time.Millisecond) // ~60 Hz
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case now := <-ticker.C:
			s.clientsMu.RLock()
			clientCount := len(s.clients)
			s.clientsMu.RUnlock()

			if clientCount == 0 {
				continue // No emulator listening: zero packets, zero CPU
			}

			s.lastFrameMu.RLock()
			lastTime := s.lastMotionTime
			lastF := s.lastFrame
			s.lastFrameMu.RUnlock()

			// If active frames were received within the last 35ms, live stream handles it
			if !lastTime.IsZero() && now.Sub(lastTime) < 35*time.Millisecond {
				continue
			}

			// Prepare neutral frame
			idleFrame := lastF
			// Zero out angular velocities during idle
			idleFrame.RotX = 0
			idleFrame.RotY = 0
			idleFrame.RotZ = 0
			if idleFrame.AccX == 0 && idleFrame.AccY == 0 && idleFrame.AccZ == 0 {
				idleFrame.AccY = -1.0
			}

			packetNum := atomic.AddUint32(&s.packetCounter, 1)

			bufPtr := padPacketPool.Get().(*[]byte)
			pkt := *bufPtr
			s.fillPadDataPacket(pkt, packetNum, idleFrame)

			s.clientsMu.RLock()
			for _, client := range s.clients {
				_, _ = s.conn.WriteToUDP(pkt, client.Addr)
			}
			s.clientsMu.RUnlock()

			padPacketPool.Put(bufPtr)
		}
	}
}

func (s *Server) sendVersionRsp(remoteAddr *net.UDPAddr) {
	buf := make([]byte, 24) // 20 bytes header + 2 bytes version + 2 bytes padding (aligned to 4 bytes)
	copy(buf[0:4], MagicServer)
	binary.LittleEndian.PutUint16(buf[4:6], ProtocolVer)
	binary.LittleEndian.PutUint16(buf[6:8], 4+4) // 4 bytes payload + 4 bytes msgType
	binary.LittleEndian.PutUint32(buf[12:16], s.serverID)
	binary.LittleEndian.PutUint32(buf[16:20], MsgTypeVersion)
	binary.LittleEndian.PutUint16(buf[20:22], ProtocolVer)
	buf[22] = 0
	buf[23] = 0

	crc := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(buf[8:12], crc)

	_, _ = s.conn.WriteToUDP(buf, remoteAddr)
}

func (s *Server) sendPortInfoRsp(remoteAddr *net.UDPAddr, payload []byte) {
	slots := []byte{0}
	if len(payload) >= 4 {
		count := int(binary.LittleEndian.Uint32(payload[0:4]))
		if count > 0 && count <= 4 && len(payload) >= 4+count {
			slots = make([]byte, count)
			copy(slots, payload[4:4+count])
		}
	}

	for _, slot := range slots {
		buf := make([]byte, 32) // 20 bytes header + 12 bytes port info
		copy(buf[0:4], MagicServer)
		binary.LittleEndian.PutUint16(buf[4:6], ProtocolVer)
		binary.LittleEndian.PutUint16(buf[6:8], 12+4) // 12 bytes payload + 4 bytes msgType
		binary.LittleEndian.PutUint32(buf[12:16], s.serverID)
		binary.LittleEndian.PutUint32(buf[16:20], MsgTypeListPorts)

		p := buf[20:]
		p[0] = slot
		if slot == 0 {
			p[1] = SlotStateConnected // Connected
			p[2] = ModelFullGamepad   // Full Gyro Gamepad
			p[3] = ConnTypeBluetooth  // Wireless
			s.clientsMu.RLock()
			copy(p[4:10], s.macAddr[:])
			s.clientsMu.RUnlock()
			p[10] = BatteryFull
			p[11] = 1 // Active state
		} else {
			p[1] = SlotStateDisconnected // Disconnected
			p[2] = 0
			p[3] = 0
			p[10] = 0
			p[11] = 0
		}

		crc := crc32.ChecksumIEEE(buf)
		binary.LittleEndian.PutUint32(buf[8:12], crc)

		_, _ = s.conn.WriteToUDP(buf, remoteAddr)
	}
}

func (s *Server) touchClient(addr *net.UDPAddr) {
	key := addr.String()
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	client, exists := s.clients[key]
	if !exists {
		s.clients[key] = &ClientSub{
			Addr:     addr,
			LastSeen: time.Now(),
		}
		if s.OnClientConnect != nil {
			connectCb := s.OnClientConnect
			go connectCb(addr)
		}
	} else {
		client.LastSeen = time.Now()
	}
}

func (s *Server) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.clientsMu.Lock()
			now := time.Now()
			for key, client := range s.clients {
				// Timeout after 5 seconds of inactivity from emulator
				if now.Sub(client.LastSeen) > 5*time.Second {
					expiredAddr := client.Addr
					delete(s.clients, key)
					if s.OnClientDisconnect != nil {
						disconnectCb := s.OnClientDisconnect
						go disconnectCb(expiredAddr)
					}
				}
			}
			s.clientsMu.Unlock()
		}
	}
}

// ActiveClients returns the number of emulators currently listening for motion.
func (s *Server) ActiveClients() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

func float32ToBits(f float32) uint32 {
	return math.Float32bits(f)
}
