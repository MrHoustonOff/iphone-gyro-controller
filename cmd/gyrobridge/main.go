package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"gyrobridge/internal/ca"
	"gyrobridge/internal/dsu"
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

	ipFlag  := flag.String("ip",  "", "Override LAN IP address (e.g. -ip 192.168.31.82)")
	logFlag := flag.String("log", "", "Write session CSV log to this file (e.g. -log session.csv)")
	flag.Parse()

	localIPs := getLocalIPv4s()
	primaryIP := *ipFlag
	if primaryIP == "" {
		primaryIP = getPrimaryIP(localIPs)
	}

	// ── Session Logger ──────────────────────────────────────────────────────────
	// Activated only when --log <file> is given. Writes every incoming WSS frame
	// and the corresponding DSU output to a CSV. Zero overhead when disabled.
	var (
		sessionLogger *sessionLog
	)
	if *logFlag != "" {
		var lerr error
		sessionLogger, lerr = newSessionLog(*logFlag)
		if lerr != nil {
			fmt.Printf("[-] Cannot open log file %q: %v\n", *logFlag, lerr)
			os.Exit(1)
		}
		defer sessionLogger.Close()
		fmt.Printf("[+] Session logging → %s\n", *logFlag)
		fmt.Println("    Columns: t_ms, in_rotX, in_rotY, in_rotZ, in_accX, in_accY, in_accZ, in_qx, in_qy, in_qz, in_qw, dsu_rotX, dsu_rotY, dsu_rotZ, dsu_accX, dsu_accY, dsu_accZ")
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

	dsuSrv := dsu.NewServer(dsu.DefaultPort)
	if err := dsuSrv.Start(); err != nil {
		fmt.Printf("[-] Warning: Failed to bind Cemuhook DSU port %d: %v\n", dsu.DefaultPort, err)
	} else {
		defer dsuSrv.Stop()
		fmt.Printf("[+] Cemuhook DSU Server running on UDP port %d (Ready for Cemu / PadTest / Dolphin)\n", dsu.DefaultPort)
	}

	srv := server.NewServer(caMgr, HTTPPort, HTTPSPort, web.IndexHTML, func(frame server.MotionFrame) {
		// Pure fast-path: zero allocations, forward frame directly to Cemuhook DSU clients
		dsuSrv.SendMotion(frame)
		// Session logging (no-op when --log not set)
		if sessionLogger != nil {
			sessionLogger.Write(frame)
		}
	})

	stopHUD := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond) // 4 Hz decoupled HUD refresh
		defer ticker.Stop()

		for {
			select {
			case <-stopHUD:
				return
			case <-ticker.C:
				count, clients, hz := srv.PacketStats()
				if count > 0 {
					frame := dsuSrv.LastMotionFrame()
					dsuClients := dsuSrv.ActiveClients()
					fmt.Printf("\r[DSU: %d clients | WSS: %d | RATE: %5.1f Hz | #%06d] Rot: (%5.1f, %5.1f, %5.1f)°/s | Quat: (%4.2f, %4.2f, %4.2f, %4.2f) | Acc: (%4.2f, %4.2f, %4.2f)g",
						dsuClients, clients, hz, count, frame.RotX, frame.RotY, frame.RotZ, frame.Qx, frame.Qy, frame.Qz, frame.Qw, frame.AccX, frame.AccY, frame.AccZ)
				}
			}
		}
	}()

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

	close(stopHUD)
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

// ── Session Logger ──────────────────────────────────────────────────────────
// sessionLog writes every incoming motion frame to a CSV file.
// Header:
//
//	t_ms         – milliseconds since first frame
//	in_rotX/Y/Z  – gyro from phone (°/s) — what arrived at server
//	in_accX/Y/Z  – accelerometer (g)
//	in_qx/y/z/w  – quaternion from JS orientation
//	rot_mag      – sqrt(rotX²+rotY²+rotZ²) — easy drift signal
//	dsu_rotX/Y/Z – what actually goes into DSU packets (same values currently)
//	dsu_accX/Y/Z – accel in DSU
type sessionLog struct {
	f      *os.File
	bw     *bufio.Writer
	mu     sync.Mutex
	t0     time.Time
	frames uint64
}

func newSessionLog(path string) (*sessionLog, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	sl := &sessionLog{f: f, bw: bufio.NewWriterSize(f, 64*1024), t0: time.Now()}
	// Write CSV header
	fmt.Fprintln(sl.bw,
		"t_ms,"+
			"in_rotX,in_rotY,in_rotZ,"+
			"in_accX,in_accY,in_accZ,"+
			"in_qx,in_qy,in_qz,in_qw,"+
			"rot_mag,"+
			"dsu_rotX,dsu_rotY,dsu_rotZ,"+
			"dsu_accX,dsu_accY,dsu_accZ")
	return sl, nil
}

// Write is called from the hot-path frame callback. Uses a mutex + buffered
// writer so it never blocks the sensor goroutine for more than a few µs.
func (sl *sessionLog) Write(f server.MotionFrame) {
	tMs := time.Since(sl.t0).Milliseconds()
	rotMag := math.Sqrt(float64(f.RotX*f.RotX + f.RotY*f.RotY + f.RotZ*f.RotZ))

	sl.mu.Lock()
	fmt.Fprintf(sl.bw,
		"%d,%.3f,%.3f,%.3f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.4f,%.3f,%.3f,%.3f,%.3f,%.4f,%.4f,%.4f\n",
		tMs,
		f.RotX, f.RotY, f.RotZ,
		f.AccX, f.AccY, f.AccZ,
		f.Qx, f.Qy, f.Qz, f.Qw,
		rotMag,
		f.RotX, f.RotY, f.RotZ, // DSU sends the same values (server-side processing lives here later)
		f.AccX, f.AccY, f.AccZ,
	)
	sl.frames++
	// Flush to disk every 300 frames (~5 s at 60 Hz) to avoid losing data
	if sl.frames%300 == 0 {
		sl.bw.Flush()
	}
	sl.mu.Unlock()
}

func (sl *sessionLog) Close() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.bw.Flush()
	sl.f.Close()
	fmt.Printf("\n[+] Session log saved: %d frames → %s\n", sl.frames, sl.f.Name())
}
