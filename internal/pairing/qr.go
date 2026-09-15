package pairing

import (
	"fmt"

	"github.com/skip2/go-qrcode"
)

// PrintTerminalQR renders a QR code directly into the terminal.
func PrintTerminalQR(title, url string) {
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		fmt.Printf("[%s]: %s\n", title, url)
		return
	}

	// Invert colours for dark terminals
	art := qr.ToSmallString(false)
	fmt.Printf("\n--- [ %s ] ---\n", title)
	fmt.Println(art)
	fmt.Printf("URL: %s\n\n", url)
}
