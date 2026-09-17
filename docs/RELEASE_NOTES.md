# GyroBridge v1.0.0

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators via the Cemuhook DSU protocol.

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
   - **iOS**: Click **Initial Setup** in the app and follow the step-by-step guide to install the local certificate (required by Safari for sensor access).
   - **Android**: Scan the QR code on the main screen with your camera and open the link in Chrome.
4. **Calibrate**: Place your smartphone flat on your desk and click **Calibrate**. Calibration takes ~5 seconds and is stored permanently.
5. **Configure Emulator**: Set your emulator's Cemuhook DSU motion server to `127.0.0.1:26760`.

---

## Key Features

- **Native Multi-Architecture Support**: Official release builds for both Windows x86_64 and ARM64.
- **Pure GUI Application**: Clean startup with zero console pop-up (`GyroBridge.exe`).
- **3D Gesture Calibration**: Guided motion wizard with resting gravity capture and validation.
- **6 Profile Slots**: Store independent device profiles with automatic persistence.
- **Stationary Tilt Warning**: Discreet banner when the phone rests stationary on an unlevel surface.
- **Dedicated 3D Telemetry Viewport**: Real-time orientation and DSU stream health diagnostics.
- **RAM Monitor**: Live working set memory tracking.
- **Bilingual Interface**: Full English and Russian support.
- **Open-Source**: Licensed under the MIT License.

---

## Security & False Positive Notice

GyroBridge is 100% free, open-source software under the MIT license. It does not collect telemetry, contains no analytics or ads, and makes zero external network connections (all communication is strictly restricted to your local Wi-Fi network between your PC and phone).

Because GyroBridge is an independent open-source project without a paid enterprise digital signature certificate ($400+/year EV Code Signing), automated machine-learning heuristics in some antivirus engines (such as Microsoft Defender generic `!ml` tags) may initially flag freshly compiled Go binaries.

If you have any security reservations:
1. **Audit the Code**: Every line of code is open in this repository. You can inspect it yourself or pass it to any AI assistant (ChatGPT, Claude, Gemini) for an independent audit.
2. **Build from Source**: You can compile `GyroBridge.exe` directly on your PC using the official Go and Wails toolchains in just a couple of minutes.

