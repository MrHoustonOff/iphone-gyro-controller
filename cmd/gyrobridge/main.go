package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"gyrobridge/pkg/ca"
	"gyrobridge/pkg/dsu"
	"gyrobridge/pkg/pairing"
	"gyrobridge/pkg/server"
	"gyrobridge/pkg/vis"
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

	ipFlag      := flag.String("ip", "", "Override LAN IP address (e.g. -ip 192.168.31.82)")
	logFlag     := flag.String("log", "", "Write session CSV log to this file (e.g. -log session.csv)")
	visFlag     := flag.Bool("vis", false, "Enable 3D Web visualizer monitor at /vis (diagnostic tool)")
	visOpenFlag := flag.Bool("vis-open", false, "Automatically open browser when -vis is enabled")
	hudFlag     := flag.Bool("hud", false, "Enable verbose 4 Hz terminal HUD (debug only)")
	flag.Parse()

	localIPs := pairing.GetLocalIPv4s()
	primaryIP := *ipFlag
	if primaryIP == "" {
		primaryIP = pairing.GetPrimaryIP(localIPs)
	}

	// ── Session Logger ──────────────────────────────────────────────────────────
	// Activated only when --log <file> is given. Writes every incoming WSS frame
	// and the corresponding DSU output to a CSV. Zero overhead when disabled.
	var sessionLogger *sessionLog
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
	dsuSrv.OnClientConnect = func(addr *net.UDPAddr) {
		fmt.Printf("[+] DSU motion client subscribed: %s\n", addr.String())
	}
	dsuSrv.OnClientDisconnect = func(addr *net.UDPAddr) {
		fmt.Printf("[-] DSU motion client disconnected: %s\n", addr.String())
	}

	if err := dsuSrv.Start(); err != nil {
		fmt.Printf("[-] Warning: Failed to bind Cemuhook DSU port %d: %v\n", dsu.DefaultPort, err)
	} else {
		defer dsuSrv.Stop()
		fmt.Printf("[+] Cemuhook DSU Server running on UDP port %d (Ready for Cemu / PadTest / Dolphin)\n", dsu.DefaultPort)
	}

	// Server-side gyro hysteresis filter.
	// Fixes two root causes found from session data analysis:
	//   1. Startup burst: first ~10 frames carry raw MEMS bias before JS ZUPT converges → zeroed.
	//   2. Isolated tremor bursts: 4-5.6°/s spikes between ZUPT zero-frames → zeroed by hysteresis.
	gf := &gyroHysteresis{}

	// ── 3D Visualizer (Diagnostic Tool) ─────────────────────────────────────────
	// Fully isolated: only initialized when -vis flag is supplied.
	// Broadcaster runs asynchronously in a dedicated worker and drops frames on lag,
	// guaranteeing zero stalls or memory allocations in the gamepad hot path.
	var visBc *vis.Broadcaster
	if *visFlag {
		visBc = vis.New(web.VisHTML)
		defer visBc.Stop()
	}

	srv := server.NewServer(caMgr, HTTPPort, HTTPSPort, web.IndexHTML, func(frame server.MotionFrame) {
		// Apply server-side hysteresis (zero-copy, pure arithmetic, no alloc)
		gf.apply(&frame)

		// Forward to Cemuhook DSU clients
		dsuSrv.SendMotion(frame)

		// Broadcast to 3D visualizer viewers (only if -vis enabled)
		if visBc != nil {
			visBc.Publish(vis.Frame{
				Qx: frame.Qx, Qy: frame.Qy, Qz: frame.Qz, Qw: frame.Qw,
				Rx: frame.RotX, Ry: frame.RotY, Rz: frame.RotZ,
				Ax: frame.AccX, Ay: frame.AccY, Az: frame.AccZ,
			})
		}

		// Session logging (no-op when -log not set)
		if sessionLogger != nil {
			sessionLogger.Write(frame)
		}
	})

	srv.OnClientConnect = func(remoteAddr string) {
		fmt.Printf("[+] Mobile controller connected: %s\n", remoteAddr)
	}
	srv.OnClientDisconnect = func(remoteAddr string) {
		fmt.Printf("[-] Mobile controller disconnected: %s\n", remoteAddr)
	}

	// Register vis routes on HTTP mux if enabled
	if visBc != nil {
		visBc.Register(srv.HTTPMux, "/vis")
	}

	// Verbose terminal HUD (only when -hud flag is explicitly supplied)
	stopHUD := make(chan struct{})
	if *hudFlag {
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
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
						var viewers int
						if visBc != nil {
							viewers = visBc.ViewerCount()
						}
						fmt.Printf("\r[DSU: %d | WSS: %d | VIS: %d | RATE: %5.1f Hz | #%06d] Rot: (%5.1f, %5.1f, %5.1f)°/s | Quat: (%4.2f, %4.2f, %4.2f, %4.2f) | Acc: (%4.2f, %4.2f, %4.2f)g",
							dsuClients, clients, viewers, hz, count, frame.RotX, frame.RotY, frame.RotZ, frame.Qx, frame.Qy, frame.Qz, frame.Qw, frame.AccX, frame.AccY, frame.AccZ)
					}
				}
			}
		}()
	}

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

	if *visFlag {
		visURL := fmt.Sprintf("http://localhost:%d/vis", HTTPPort)
		fmt.Printf("[+] 3D visualizer monitor enabled → %s\n", visURL)
		if *visOpenFlag {
			go func() {
				time.Sleep(800 * time.Millisecond)
				openBrowser(visURL)
			}()
		}
	}

	fmt.Println("[*] Listening for controller telemetry... (Press Ctrl+C to stop)")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	if *hudFlag {
		close(stopHUD)
	}
	fmt.Println("\n[*] Shutting down GyroBridge...")
}

// openBrowser opens the given URL in the system default browser.
// Non-blocking: failure is silently ignored (browser open is best-effort).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux, bsd, ...
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start() // fire-and-forget
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

// ── Server-side Gyro Hysteresis Filter ─────────────────────────────────────
//
// Two-state machine tuned from real session data (session.csv):
//
//	STILL:  rot_mag < LOW_DEG for ≥ STILL_FRAMES consecutive → gyro zeroed.
//	        Once in STILL, stays STILL until rot_mag > HIGH_DEG.
//	        During first WARMUP_FRAMES frames, also forces STILL (JS ZUPT window
//	        hasn't converged yet so raw MEMS bias would leak through).
//
//	MOVING: rot_mag > HIGH_DEG → pass gyro through unchanged.
//
// Calibrated values (updated from session 2 data):
//   - Isolated hand-tremor bursts observed: 1.1 – 7.4 °/s  →  LOW = 3.0
//   - Minimum intentional motion observed:  ~8 °/s          →  HIGH = 8.0
//   - Frames to confirm still:              2 (~33ms @60Hz)  →  STILL_FRAMES = 2
//   - JS ZUPT warmup window size:           ~10 samples      →  WARMUP = 12
const (
	gyroLow         = 3.0 // °/s – below this, start counting toward STILL
	gyroHigh        = 8.0 // °/s – above this, exit STILL state
	gyroStillFrames = 2   // consecutive low frames before declaring STILL
	gyroWarmup      = 12  // startup frames to suppress (JS ZUPT not converged)
)

type gyroHysteresis struct {
	isStill    bool
	lowCount   int
	frameCount int
}

func (g *gyroHysteresis) apply(f *server.MotionFrame) {
	g.frameCount++

	// Force STILL during startup warmup (before JS ZUPT window is full)
	if g.frameCount <= gyroWarmup {
		f.RotX = 0
		f.RotY = 0
		f.RotZ = 0
		return
	}

	rotMag := math.Sqrt(float64(f.RotX*f.RotX + f.RotY*f.RotY + f.RotZ*f.RotZ))

	if g.isStill {
		if rotMag > gyroHigh {
			// Definitive motion: exit STILL immediately
			g.isStill = false
			g.lowCount = 0
			// Let this frame through unchanged
		} else {
			// Stay STILL: suppress gyro (accel still passes — it's needed by AHRS)
			f.RotX = 0
			f.RotY = 0
			f.RotZ = 0
		}
	} else {
		// MOVING state
		if rotMag < gyroLow {
			g.lowCount++
			if g.lowCount >= gyroStillFrames {
				g.isStill = true
				f.RotX = 0
				f.RotY = 0
				f.RotZ = 0
			}
			// Frames below LOW but not yet declared STILL still pass through
			// (they're part of real deceleration — only suppress after N frames)
		} else {
			// Clearly moving: reset counter
			g.lowCount = 0
		}
	}
}
