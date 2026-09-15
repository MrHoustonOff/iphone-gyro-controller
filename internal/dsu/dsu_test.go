package dsu

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"net"
	"testing"
	"time"

	"gyrobridge/internal/server"
)

func TestDSU_PadDataPacketLayoutAndCRC32(t *testing.T) {
	dsuSrv := NewServer(26760)

	testFrame := server.MotionFrame{
		Timestamp: 123456,
		RotX:      15.5,
		RotY:      -30.2,
		RotZ:      45.0,
		AccX:      0.05,
		AccY:      0.98,
		AccZ:      0.12,
	}

	packet := dsuSrv.BuildPadDataPacket(1, testFrame)

	// 1. Total length must be exactly 100 bytes
	if len(packet) != 100 {
		t.Fatalf("expected packet length 100, got %d", len(packet))
	}

	// 2. Magic must be DSUS
	if string(packet[0:4]) != "DSUS" {
		t.Errorf("expected magic DSUS, got %s", string(packet[0:4]))
	}

	// 3. Protocol version must be 1001
	protoVer := binary.LittleEndian.Uint16(packet[4:6])
	if protoVer != 1001 {
		t.Errorf("expected proto version 1001, got %d", protoVer)
	}

	// 4. Message type must be 0x100002
	msgType := binary.LittleEndian.Uint32(packet[16:20])
	if msgType != 0x100002 {
		t.Errorf("expected msgType 0x100002, got 0x%X", msgType)
	}

	// 5. CRC32 verification (IEEE 802.3)
	packetCRC := binary.LittleEndian.Uint32(packet[8:12])
	// Zero out CRC field
	binary.LittleEndian.PutUint32(packet[8:12], 0)
	computedCRC := crc32.ChecksumIEEE(packet)

	if packetCRC != computedCRC {
		t.Fatalf("CRC32 mismatch! stored=0x%08X, computed=0x%08X", packetCRC, computedCRC)
	}

	// 6. Verify data payload fields
	p := packet[20:]
	// Accel X (offset 55..59)
	accX := math.Float32frombits(binary.LittleEndian.Uint32(p[55:59]))
	if accX != 0.05 {
		t.Errorf("accX mismatch: got %f, want 0.05", accX)
	}

	// Gyro Pitch/RotX (offset 67..71)
	gyroX := math.Float32frombits(binary.LittleEndian.Uint32(p[67:71]))
	if gyroX != 15.5 {
		t.Errorf("gyroX mismatch: got %f, want 15.5", gyroX)
	}
}

func TestDSU_ClientHandshakeAndStreaming(t *testing.T) {
	// Bind to a free local port
	srv := NewServer(0)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start DSU server: %v", err)
	}
	defer srv.Stop()

	serverAddr := srv.conn.LocalAddr().(*net.UDPAddr)

	clientConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		t.Fatalf("failed to connect UDP client: %v", err)
	}
	defer clientConn.Close()

	// 1. Send DSUC_VersionReq (0x100000)
	req := make([]byte, 20)
	copy(req[0:4], "DSUC")
	binary.LittleEndian.PutUint16(req[4:6], 1001)
	binary.LittleEndian.PutUint16(req[6:8], 4) // msgType length
	binary.LittleEndian.PutUint32(req[12:16], 0xABCD)
	binary.LittleEndian.PutUint32(req[16:20], 0x100000)
	crc := crc32.ChecksumIEEE(req)
	binary.LittleEndian.PutUint32(req[8:12], crc)

	if _, err := clientConn.Write(req); err != nil {
		t.Fatalf("failed to send VersionReq: %v", err)
	}

	// 2. Receive DSUS_VersionRsp
	buf := make([]byte, 256)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buf)
	if err != nil {
		t.Fatalf("failed to receive VersionRsp: %v", err)
	}

	if string(buf[0:4]) != "DSUS" {
		t.Errorf("expected DSUS response, got %s", string(buf[0:4]))
	}
	rspType := binary.LittleEndian.Uint32(buf[16:20])
	if rspType != 0x100000 {
		t.Errorf("expected version response type 0x100000, got 0x%X", rspType)
	}

	// 3. Send motion frame from server
	testFrame := server.MotionFrame{
		RotX: 10.0, RotY: 20.0, RotZ: 30.0,
		AccX: 0.1, AccY: 0.2, AccZ: 0.98,
	}

	srv.SendMotion(testFrame)

	// 4. Client should receive the 100-byte PadData packet
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = clientConn.Read(buf)
	if err != nil {
		t.Fatalf("failed to receive PadData: %v", err)
	}

	if n != 100 {
		t.Fatalf("expected 100 bytes PadData, got %d", n)
	}

	pData := buf[:n]
	padCRC := binary.LittleEndian.Uint32(pData[8:12])
	binary.LittleEndian.PutUint32(pData[8:12], 0)
	if crc32.ChecksumIEEE(pData) != padCRC {
		t.Error("received PadData CRC32 failed verification")
	}
}

func BenchmarkFillPadDataPacket(b *testing.B) {
	srv := NewServer(0)
	frame := server.MotionFrame{
		Timestamp: 123456,
		RotX:      15.5,
		RotY:      -30.2,
		RotZ:      45.0,
		AccX:      0.05,
		AccY:      0.98,
		AccZ:      0.12,
	}
	buf := make([]byte, 100)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		srv.fillPadDataPacket(buf, uint32(i), frame)
	}
}

func BenchmarkSendMotion(b *testing.B) {
	srv := NewServer(0)
	if err := srv.Start(); err != nil {
		b.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	// Register a dummy client so SendMotion actually executes write
	dummyAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	srv.clientsMu.Lock()
	srv.clients["127.0.0.1:0"] = &ClientSub{
		Addr:     dummyAddr,
		LastSeen: time.Now(),
	}
	srv.clientsMu.Unlock()

	frame := server.MotionFrame{
		Timestamp: 123456,
		RotX:      15.5,
		RotY:      -30.2,
		RotZ:      45.0,
		AccX:      0.05,
		AccY:      0.98,
		AccZ:      0.12,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		srv.SendMotion(frame)
	}
}

