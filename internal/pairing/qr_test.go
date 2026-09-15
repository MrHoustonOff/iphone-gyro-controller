package pairing

import (
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
