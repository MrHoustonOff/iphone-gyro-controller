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

	lastFrameMu sync.RWMutex
	lastFrame   server.MotionFrame

	stopChan chan struct{}
	running  atomic.Bool

	// Lifecycle event hooks for clean, non-spammy logging
	OnClientConnect    func(addr *net.UDPAddr)
	OnClientDisconnect func(addr *net.UDPAddr)
}

// NewServer creates a new Cemuhook DSU server.
// If port < 0, it defaults to 26760. If port == 0, OS allocates a dynamic port.
func NewServer(port int) *Server {
	if port < 0 {
		port = DefaultPort
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	s := &Server{
		port:     port,
		serverID: r.Uint32(),
		macAddr:  [6]byte{0x00, 0x13, 0x37, byte(r.Intn(255)), byte(r.Intn(255)), byte(r.Intn(255))},
		clients:  make(map[string]*ClientSub),
		stopChan: make(chan struct{}),
	}

	return s
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

	s.lastFrameMu.Lock()
	s.lastFrame = frame
	s.lastFrameMu.Unlock()
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
		s.sendPortInfoRsp(remoteAddr)
	case MsgTypePadData:
		// Client is actively listening for pad data
	}
}

func (s *Server) sendVersionRsp(remoteAddr *net.UDPAddr) {
	buf := make([]byte, 22) // 20 bytes header + 2 bytes version
	copy(buf[0:4], MagicServer)
	binary.LittleEndian.PutUint16(buf[4:6], ProtocolVer)
	binary.LittleEndian.PutUint16(buf[6:8], 2+4) // 2 bytes payload + 4 bytes msgType
	binary.LittleEndian.PutUint32(buf[12:16], s.serverID)
	binary.LittleEndian.PutUint32(buf[16:20], MsgTypeVersion)
	binary.LittleEndian.PutUint16(buf[20:22], ProtocolVer)

	crc := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(buf[8:12], crc)

	_, _ = s.conn.WriteToUDP(buf, remoteAddr)
}

func (s *Server) sendPortInfoRsp(remoteAddr *net.UDPAddr) {
	buf := make([]byte, 36) // 20 bytes header + 16 bytes port info
	copy(buf[0:4], MagicServer)
	binary.LittleEndian.PutUint16(buf[4:6], ProtocolVer)
	binary.LittleEndian.PutUint16(buf[6:8], 16+4) // 16 bytes payload + 4 bytes msgType
	binary.LittleEndian.PutUint32(buf[12:16], s.serverID)
	binary.LittleEndian.PutUint32(buf[16:20], MsgTypeListPorts)

	p := buf[20:]
	p[0] = 0                   // Slot 0
	p[1] = SlotStateConnected // Connected
	p[2] = ModelFullGamepad   // Full Gyro Gamepad
	p[3] = ConnTypeBluetooth  // Wireless
	copy(p[4:10], s.macAddr[:])
	p[10] = BatteryFull

	crc := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(buf[8:12], crc)

	_, _ = s.conn.WriteToUDP(buf, remoteAddr)
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
