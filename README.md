**English** | [Русский](docs/README_RU.md)

# GyroBridge

> 📖 **Comprehensive User Guide**: [English User Guide](docs/guide.en.md) | [Русскоязычное руководство](docs/guide.ru.md)

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators using the Cemuhook DSU protocol.

![GyroBridge Core Interface](docs/imgs/core%20screen.jpg)

Compatible with Cemu, RPCS3, Ryujinx, Yuzu, Dolphin, PCSX2, and any game or tool supporting Cemuhook DSU.

---

## Quick Start

### 1. Download
Download `GyroBridge.exe` (or `GyroBridge-windows-arm64.exe` for ARM-based Windows devices) from the latest [GitHub Releases](https://github.com/MrHoustonOff/iphone-gyro-controller/releases).

### 2. Launch
Make sure your PC and smartphone are connected to the **same Wi-Fi network**. Launch `GyroBridge.exe`.

### 3. Connect Phone
- **iOS (iPhone / iPad)**: Click **Initial Setup** in the app and follow the step-by-step guide to install the local profile certificate required by Safari to access motion sensors over HTTPS.
- **Android**: Scan the QR code on the main screen with your camera and open the controller web app in Google Chrome.

### 4. Calibrate
Once connected, place your smartphone flat on your desk in gaming grip and click **Calibrate**. The calibration takes ~5 seconds and needs to be done only once per device.

### 5. Configure Emulator
In your emulator's input/controller settings, configure the motion server:
- **Server IP**: `127.0.0.1`
- **Server Port**: `26760`
- **Protocol**: Cemuhook DSU

---

## Live 3D Telemetry & Diagnostics

GyroBridge includes a dedicated real-time 3D telemetry window to verify sensor response, orientation stability, and DSU packet delivery rate:

![3D Telemetry & Diagnostics](docs/imgs/3d%20view%20screen.jpg)

---

## Advanced Settings & Customization

Click the **Settings** button in the header to access advanced options:
- **Interactive Test Bench**: Live multi-axis oscilloscope, 3D target viewfinder, and apparatus tilt mini-bench to test response and filters before gaming.
- **Custom Ports**: Modify Cemuhook DSU (`26760`), HTTP pairing (`8080`), and HTTPS controller (`8443`) ports with real-time collision checks.
- **Gyro Deadzone & 1-Euro Filter**: Filter out resting sensor micro-jitters with customizable noise deadzone levels and adaptive smoothing.
- **Global UI Scaling**: Scale the interface and fonts (`0.80x` - `1.40x`) with desktop shortcuts (`Ctrl +`, `Ctrl -`, `Ctrl 0`).
- **Audio Feedback**: Choose between cute synthesized celesta chimes, classic Windows system sounds, or silent mode.
- **Appearance**: Switch between Apple-inspired dark and light interfaces.

---

## Security, Antivirus & Transparency

GyroBridge is 100% open-source software under the MIT license. It contains zero trackers, no telemetry, and makes no external internet connections whatsoever - all communication is strictly between your phone and your PC over your local home Wi-Fi.

### Antivirus False Positives Notice
Independent open-source developers rarely purchase proprietary EV (Extended Validation) code signing certificates due to exorbitant recurring costs ($400+/year). Because of this, automated machine-learning heuristics in certain antivirus software (e.g., Microsoft Defender generic `!ml` tags) might flag freshly compiled binaries as unfamiliar.

We regularly scan release binaries against VirusTotal (69+ engines clean). If you have any security reservations:
- **Inspect the Source**: Review the entire codebase yourself or pass it to any AI agent (Claude, ChatGPT, Gemini, etc.) to perform an independent security review.
- **Build from Source**: Follow the instructions below to compile the binary directly on your own machine using Go and Wails.

---

## Building from Source

Prerequisites:
- [Go](https://go.dev/) 1.21+
- [Node.js](https://nodejs.org/) 18+
- [Wails CLI v2](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

```bash
# Clone the repository
git clone https://github.com/MrHoustonOff/iphone-gyro-controller.git
cd iphone-gyro-controller/gui

# Build Windows x86_64
wails build -o GyroBridge.exe

# Build Windows ARM64
wails build -platform windows/arm64 -o GyroBridge-arm64.exe
```

The compiled binaries will be placed in `gui/build/bin/`.

---

## License

This project is open-source and licensed under the [MIT License](LICENSE).
