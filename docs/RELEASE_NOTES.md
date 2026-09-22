# GyroBridge v1.1.3

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators via the Cemuhook DSU protocol.

---

## What's New in v1.1.3

### Sound Effects Mixer & New Audio Cues
- **Detailed Sound Mixer**: Expandable drawer with individual volume sliders (0x to 3x) and play-test buttons for every event:
  - Phone Connection (`connect`)
  - Phone Disconnection (`disconnect`)
  - Emulator Client Subscription (`dsu`)
  - Orientation Recenter (`recenter`)
  - Test Bench Goal / Target Hit (`goal`)
  - Test Bench Ball Abyss Fall (`defeat`)
- **New Audio Cues**: Distinct chime synthesized on Cemuhook DSU client connection and Recenter reset.
- **Synthesizer Tuning**: Overhauled ball fall defeat sound synthesis with unblocked playback.

### Action-Driven Settings & UX
- **Instant Auto-Save**: Action-driven settings engine with immediate live persistence on change.
- **Modified Field Indicators**: Subtle indicator dot next to any setting modified from its default value.
- **CSS Zoom Normalization**: Floating tooltips and custom select dropdowns properly compensate for display zoom (0.8x - 1.4x).
- **Clean Audio Controls**: Streamlined audio test buttons without intrusive tooltips.

---

## What's New in v1.1.2

### System Tray & Background Operation
- **Minimize to System Tray**: GyroBridge can now run unobtrusively in the Windows notification area with near-zero resource consumption (~15 MB RAM, 0% CPU).
- **Tray Context Menu**: Right-click the tray icon to quickly show/hide the main window, pause/resume motion streaming, or exit the application cleanly.
- **Dynamic Tray Status**: The tray icon reflects connection state, indicating whether a streaming session is active or idle.

### Cemuhook DSU Protocol Compliance & Multi-Slot Fixes
- **Strict Packet Sizing**: Fixed Cemuhook `PortInfo` response packet length strictly to 32 bytes (eliminating 4 extra trailing bytes) and `VersionResponse` to 24 bytes, resolving CRC32 checksum rejections (`PortInfo is invalid!`) in Cemu 2.x and other emulators.
- **Multi-Slot Port Querying**: Full support for multi-slot `ListPorts` requests (slots 0..3) with instantaneous responses for each requested index, eliminating 3-second connection timeouts in Cemu.

### UI & Telemetry Refinements
- **Dual-State DSU Connection Banner**:
  - Waiting state: subtle amber notification (`Waiting for emulators`) when listening on port 26760 with zero connected clients.
  - Active state: green indicator (`Emulator Connected`) displaying live client IP, ephemeral port, and real-time pulse indicator once subscribed.
- **Zero Layout Shifts**: Main interface card height locked strictly at 68px across all connection state transitions.
- **Live Connected Client Monitoring**: Real-time connected emulator diagnostics in both the main window and LiveDebug window with instant connect/disconnect callbacks.

### Comprehensive User Documentation
- **New Guides**: Published complete, visual guides in Russian ([`docs/guide.ru.md`](guide.ru.md)) and English ([`docs/guide.en.md`](guide.en.md)).
- **Cemu Socket Lifecycle Documentation**: Detailed breakdown of Cemu's UDP socket lifecycle when starting before or after GyroBridge, with both instant in-game and startup solutions.

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
- **System Tray Integration**: Background operation with tray menu, show/hide shortcuts, and quit confirmation.
- **Interactive Test Bench**: Real-time oscilloscope, 3D target viewfinder, and apparatus tilt mini-bench.
- **Advanced Settings Hub**: Custom network ports, gyro noise filtering, and theme customization.
- **Global UI Scaling**: Dynamic zoom (`0.80x` - `1.40x`) with `Ctrl +/-/0` shortcut support.
- **Audio Feedback**: Melodic chimes or native Windows sounds on connect and disconnect.
- **3D Gesture Calibration**: Guided motion wizard with resting gravity capture and validation.
