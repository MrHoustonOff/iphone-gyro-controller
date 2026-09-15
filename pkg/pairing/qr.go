package pairing

import (
	"fmt"
	"net"

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

// GenerateQRPNG generates a standard PNG image byte slice for the given URL.
func GenerateQRPNG(url string, size int) ([]byte, error) {
	if size <= 0 {
		size = 256
	}
	return qrcode.Encode(url, qrcode.Medium, size)
}

// IsPrivateLAN checks if the IP address belongs to RFC 1918 private LAN ranges.
func IsPrivateLAN(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil || ip.IsLoopback() {
		return false
	}
	// 192.168.0.0/16
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	// 10.0.0.0/8
	if ip4[0] == 10 {
		return true
	}
	// 172.16.0.0 - 172.31.255.255
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	return false
}

// GetLocalIPv4s returns all active private LAN IPv4 addresses.
func GetLocalIPv4s() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && IsPrivateLAN(ip) {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

// GetPrimaryIP prioritizes standard 192.168.x.x home Wi-Fi subnet.
func GetPrimaryIP(lanIPs []net.IP) string {
	for _, ip := range lanIPs {
		ip4 := ip.To4()
		if ip4 != nil && ip4[0] == 192 && ip4[1] == 168 {
			return ip.String()
		}
	}
	if len(lanIPs) > 0 {
		return lanIPs[0].String()
	}
	return "127.0.0.1"
}
