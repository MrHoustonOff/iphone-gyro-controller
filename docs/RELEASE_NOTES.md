# GyroBridge v1.1.0

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators via the Cemuhook DSU protocol.

---

## What's New in v1.1.0

### Advanced Settings Hub
- **Custom Network Ports**: Configure custom ports for the Cemuhook DSU motion server (default `26760`), HTTP pairing server (default `8080`), and secure HTTPS controller server (default `8443`). Strict port range validation (`1024-65535`) and collision detection prevent misconfigurations.
- **Adjustable Gyroscope Deadzone**: Selectable noise deadzone threshold (`0.00 deg/s` raw to `0.35 deg/s` high) to eliminate resting sensor micro-jitter in sensitive titles.
- **Stationary Tilt Warning Toggle**: Enable or disable the dropdown hint when the smartphone rests on an unlevel surface (> 10 degrees).
- **Calibration Disconnect Alert Toggle**: Option to toggle visual disconnect warning during calibration.
- **Apple-Style Floating Infotips**: Compact single-line settings rows with glassmorphism floating tooltips (`backdrop-filter: blur(20px)`), automatic edge bounding, and reset-to-defaults functionality.
- **Graceful Service Restart**: Apple-style notification indicating service restart on save with automatic page reload.

### Connection Audio Cues
- **Audio Feedback**: Audible notification when your smartphone connects or suddenly disconnects.
- **Synthesized Celesta Melodies**: Pleasant synthesized arpeggio generated in real-time via Web Audio API (C5-E5-G5-C6 on connect, G5-Eb5-C5 on disconnect) without requiring external audio files.
- **Windows System Sounds**: Native non-blocking Windows hardware sound integration (`PlaySoundW` via `winmm.dll`) for users preferring classic system alerts.
- **Sound Preview & Silent Mode**: Instant preview button in Settings and full silent mode support.

### Refined Disconnect Experience
- **Modal-Scoped Calibration Alert**: Disconnect warning during calibration is now scoped strictly within the calibration modal card with frosted glass blur, keeping the main application window and header navigation crisp.
- **Safe Capture Recovery**: Active recording automatically pauses safely if the phone screen turns off, with instant resume on reconnection.

### RAM & Performance Optimizations
- **Live Memory Tracking**: Integrated RAM monitor showing real-time working set memory usage.
- **Adaptive Telemetry Rendering**: Inclinometer and 3D telemetry loops with exponential lerp smoothing for buttery-smooth 60/120/144Hz displays.

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
- **Advanced Settings Hub**: Custom network ports, gyro noise filtering, and theme customization.
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
