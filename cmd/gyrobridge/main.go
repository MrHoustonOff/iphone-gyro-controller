package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gyrobridge/internal/ca"
	"gyrobridge/internal/pairing"
	"gyrobridge/internal/server"
	"gyrobridge/web"
)

const (
	HTTPPort  = 8080
	HTTPSPort = 8443
)

func main() {
	fmt.Println("==========================================================")
	fmt.Println("             GyroBridge - Motion Gamepad (Go)             ")
	fmt.Println("==========================================================")

	ipFlag := flag.String("ip", "", "Override LAN IP address (e.g. -ip 192.168.31.82)")
	flag.Parse()

	localIPs := getLocalIPv4s()
	primaryIP := *ipFlag
	if primaryIP == "" {
		primaryIP = getPrimaryIP(localIPs)
	}

	fmt.Printf("[*] Detected LAN IP: %s\n", primaryIP)
	for _, ip := range localIPs {
		if ip.String() != primaryIP {
			fmt.Printf("    (Also available LAN: %s)\n", ip)
		}
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = "."
	}
	caDir := filepath.Join(appData, "gyrobridge", "ca")

	fmt.Printf("[*] Initializing Local Certificate Authority (%s)...\n", caDir)
	caMgr, err := ca.NewCertificateManager(caDir, localIPs, []string{"gamepad.local"})
	if err != nil {
		fmt.Printf("[-] Failed to initialize CA: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[+] Local CA and Leaf certificate ready.")

	var lastLoggedPacket uint64
	var srv *server.Server
	srv = server.NewServer(caMgr, HTTPPort, HTTPSPort, web.IndexHTML, func(frame server.MotionFrame) {
		nowCount, _, hz := srv.PacketStats()
		if nowCount-lastLoggedPacket >= 15 {
			lastLoggedPacket = nowCount
			fmt.Printf("\r[RATE: %5.1f Hz | STREAM #%06d] Yaw: %6.1f° | Pitch: %6.1f° | Roll: %6.1f° | Acc: (%5.2f, %5.2f, %5.2f)",
				hz, nowCount, frame.Alpha, frame.Beta, frame.Gamma, frame.AccX, frame.AccY, frame.AccZ)
		}
	})

	if err := srv.Start(); err != nil {
		fmt.Printf("[-] Failed to start server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Stop()

	fmt.Println("[+] HTTPS + WSS Server running on port", HTTPSPort)
	fmt.Println("[+] HTTP Setup Server running on port", HTTPPort)

	// Display Onboarding & QR codes
	setupURL := fmt.Sprintf("http://%s:%d/ca.mobileconfig", primaryIP, HTTPPort)
	appURL := fmt.Sprintf("https://%s:%d", primaryIP, HTTPSPort)

	fmt.Println("\n==========================================================")
	fmt.Println("                 SETUP INSTRUCTIONS                       ")
	fmt.Println("==========================================================")
	fmt.Println("📱 iOS (First-time only):")
	fmt.Println("   1. Open Safari on iPhone and scan QR #1 (or open URL):")
	fmt.Println("      -->", setupURL)
	fmt.Println("   2. Tap 'Allow' -> Profile Downloaded.")
	fmt.Println("   3. Go to iOS Settings -> Profile Downloaded -> Install.")
	fmt.Println("   4. Go to iOS Settings -> General -> About -> Certificate Trust Settings.")
	fmt.Println("      Turn ON the switch for 'GyroBridge Root CA'.")
	fmt.Println("----------------------------------------------------------")
	fmt.Println("🎮 ALL PHONES (Game controller):")
	fmt.Println("   Scan QR #2 (or open in Safari/Chrome):")
	fmt.Println("      -->", appURL)
	fmt.Println("   (On Android: click 'Advanced' -> 'Proceed to site')")
	fmt.Println("==========================================================")

	pairing.PrintTerminalQR("QR #1: iOS Setup Profile", setupURL)
	pairing.PrintTerminalQR("QR #2: Controller Gamepad", appURL)

	fmt.Println("[*] Listening for controller telemetry... (Press Ctrl+C to stop)")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n[*] Shutting down GyroBridge...")
}

func isPrivateLAN(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil || ip.IsLoopback() {
		return false
	}
	// 192.168.0.0/16 (typical home Wi-Fi)
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

func getLocalIPv4s() []net.IP {
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
			if ip != nil && isPrivateLAN(ip) {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func getPrimaryIP(lanIPs []net.IP) string {
	// Prioritize standard 192.168.x.x home Wi-Fi subnet
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
