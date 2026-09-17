**English** | [Русский](docs/README_RU.md)

# GyroBridge

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
