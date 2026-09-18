# GyroBridge v1.1.0

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators via the Cemuhook DSU protocol.

---

## What's New in v1.1.0

### Real-Time Response Test Bench
- **Interactive Multi-Mode Bench**: Integrated into Settings to verify sensor response and filter tuning live before launching games.
- **Oscilloscope Waveform Visualizer**: Real-time canvas oscilloscope tracking Pitch, Roll, and Yaw curves with 3-axis stacked view and responsive grid scaling. Supports instant switching between raw sensor input and filtered DSU output streams.
- **Interactive 3D Target Aiming Bench**: Fullscreen-capable 3D viewfinder with customizable sensitivity multipliers, axis inversion toggles, and direct in-game translation advice.
- **Physics Apparatus Platform**: Real-time 3D tilt stage featuring simulated ball physics, goal detection, confetti fanfare, and disconnect safe-leveling.

### Advanced Settings Hub & Sensor Pipeline
- **Adaptive 1-Euro Filter & Deadband**: Tunable noise suppression to eliminate resting sensor jitter while preserving high-speed hand motion fidelity.
- **Configurable Network Ports**: Independent port assignments for Cemuhook DSU (`26760`), HTTP pairing (`8080`), and HTTPS controller (`8443`) with range validation (`1024-65535`) and collision detection.
- **Stationary Tilt & Disconnect Warning Toggles**: Configurable threshold alerts when the smartphone rests on an uneven surface or disconnects during calibration.
- **Floating Infotips**: Glassmorphic parameter tooltips with automated boundary clamping and reset-to-defaults functionality.

### Global UI Scaling & Desktop Shortcuts
- **Universal Scale Engine**: Global UI and font zoom adjustment (`0.80x` to `1.40x`) with continuous synchronization across both the main application window and the dedicated 3D telemetry window.
- **Standard Desktop Shortcuts**: Fast scaling using `Ctrl +`, `Ctrl -`, and `Ctrl 0` (reset), as well as `Ctrl + MouseWheel`, with captured-phase event priority.
- **Transient HUD Indicator**: Lightweight pill toast notification displaying the active scale factor on adjustment.

### In-App 3D Gyro Recenter
- **Dynamic Orientation Re-centering**: On-demand 1-second countdown trigger and modal with backdrop blur to re-align tracking without altering stored baseline sensor calibration.
- **Orientation Continuity**: AHRS quaternion continuity preserved across transient network reconnections without wiping center.

### Audio Cues & Connection Feedback
- **Connection Events Audio**: Audible feedback on device connection and disconnect.
- **Synthesized Celesta Melodies**: Real-time procedural arpeggios via Web Audio API (C5-E5-G5-C6 on connect, G5-Eb5-C5 on disconnect).
- **Native Windows Alerts**: Non-blocking system audio via `winmm.dll` with instant preview and silent mode.

### Performance & UI Hardening
- **Responsive Header Collapse**: Automatic navigation transition into compact 36px icon mode for default window launch dimensions (`880x620`) and viewports `<= 1020px`.
- **Viewport Layout Resilience**: Replaced relative viewport units with percentage-based sizing in 3D telemetry window to eliminate clipping artifacts under browser zoom.
- **IPC & DOM Throttling**: Decoupled DOM updates and throttled telemetry IPC to 60Hz, preventing layout thrashing and reducing CPU usage.
- **Live Memory Tracking**: Working set RAM monitor in header with live polling.

---

## Downloads

| File | Architecture | Description |
|---|---|---|
| `GyroBridge.exe` | x86_64 (`amd64`) | Standard Windows 64-bit standalone executable |
| `GyroBridge-windows-amd64.exe` | x86_64 (`amd64`) | Same binary with explicit architecture label |
| `GyroBridge-windows-arm64.exe` | ARM64 (`arm64`) | Native executable for Windows on ARM devices |

No installation required. Download, run, and connect.

---

## Getting Started

1. **Local Network**: Ensure your PC and smartphone are on the same Wi-Fi network.
2. **Launch**: Run `GyroBridge.exe`.
3. **Connect Phone**:
   - **iOS**: Click **Initial Setup** in the app and follow the step-by-step guide to install the local certificate (required by Safari for motion sensor access over HTTPS).
   - **Android**: Scan the QR code on the main screen with your camera and open the link in Google Chrome.
4. **Calibrate**: Place your smartphone flat on your desk and click **Calibrate**. Calibration takes ~5 seconds and is stored permanently.
5. **Configure Emulator**: Set your emulator's Cemuhook DSU motion server to `127.0.0.1:26760` (or your custom configured DSU port).

---

## Key Features

- **Native Multi-Architecture Support**: Official release builds for both Windows x86_64 and ARM64.
- **Pure GUI Application**: Clean startup with zero console pop-up (`GyroBridge.exe`).
- **Interactive Test Bench**: Real-time oscilloscope, 3D target viewfinder, and apparatus tilt mini-bench.
- **Advanced Settings Hub**: Custom network ports, gyro noise filtering, and theme customization.
- **Global UI Scaling**: Dynamic zoom (`0.80x` - `1.40x`) with `Ctrl +/-/0` shortcut support.
- **Audio Feedback**: Melodic chimes or native Windows sounds on connect and disconnect.
- **3D Gesture Calibration**: Guided motion wizard with resting gravity capture and validation.
- **6 Profile Slots**: Store independent device profiles with automatic persistence.
- **Stationary Tilt Warning**: Discreet banner when the phone rests stationary on an unlevel surface.
- **Dedicated 3D Telemetry Viewport**: Real-time orientation and DSU stream health diagnostics.
- **RAM Monitor**: Live working set memory tracking.
- **Bilingual Interface**: Full English and Russian support with 100% key parity.
- **Open-Source**: Licensed under the MIT License.

---

## Security, Antivirus & Transparency

GyroBridge is 100% free, open-source software under the MIT license. It does not collect telemetry, contains no analytics or ads, and makes zero external network connections (all communication is strictly restricted to your local Wi-Fi network between your PC and phone).

Because GyroBridge is an independent open-source project without a paid enterprise digital signature certificate ($400+/year EV Code Signing), automated machine-learning heuristics in some antivirus engines (such as Microsoft Defender generic `!ml` tags) may initially flag freshly compiled Go binaries.

If you have any security reservations:
1. **Audit the Code**: Every line of code is open in this repository. You can inspect it yourself or pass it to any AI assistant (ChatGPT, Claude, Gemini) for an independent audit.
2. **Build from Source**: You can compile `GyroBridge.exe` directly on your PC using the official Go and Wails toolchains in just a couple of minutes.
