package pairing

import (
	"bytes"
	"net"
	"testing"

	"github.com/skip2/go-qrcode"
)

func TestQRCode_Generation(t *testing.T) {
	testURL := "https://gamepad.local:8443"
	qr, err := qrcode.New(testURL, qrcode.Medium)
	if err != nil {
		t.Fatalf("failed to generate QR: %v", err)
	}

	str := qr.ToSmallString(false)
	if len(str) == 0 {
		t.Error("QR string is empty")
	}
}

func TestQRCode_PNGGeneration(t *testing.T) {
	testURL := "https://gamepad.local:8443"
	pngBytes, err := GenerateQRPNG(testURL, 128)
	if err != nil {
		t.Fatalf("failed to generate QR PNG: %v", err)
	}

	// PNG magic header: \x89PNG\r\n\x1a\n
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if len(pngBytes) < 8 || !bytes.Equal(pngBytes[:8], pngMagic) {
		t.Error("generated data is not a valid PNG image")
	}
}

func TestQRCode_LANFilters(t *testing.T) {
	if !IsPrivateLAN(net.ParseIP("192.168.1.100")) {
		t.Error("expected 192.168.1.100 to be private LAN")
	}
	if !IsPrivateLAN(net.ParseIP("10.0.0.1")) {
		t.Error("expected 10.0.0.1 to be private LAN")
	}
	if !IsPrivateLAN(net.ParseIP("172.20.0.1")) {
		t.Error("expected 172.20.0.1 to be private LAN")
	}
	if IsPrivateLAN(net.ParseIP("8.8.8.8")) {
		t.Error("expected 8.8.8.8 to NOT be private LAN")
	}
	if IsPrivateLAN(net.ParseIP("127.0.0.1")) {
		t.Error("expected loopback 127.0.0.1 to NOT be considered private LAN")
	}
}
