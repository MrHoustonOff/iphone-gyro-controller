# GyroBridge v1.1.1

GyroBridge turns your smartphone (iOS or Android) into a high-precision, low-latency motion controller for PC games and emulators via the Cemuhook DSU protocol.

---

## What's New in v1.1.1

### Zelda Aim Target Shooting Mini-Game
- **30-Second Target Challenge**: Added a target shooting mini-game directly into the Aim Test Bench. Hit targets in succession to build the highest score within a 30-second window.
- **Immediate Synchronous Respawn**: Fixed a CSS syntax token issue in `calc()` and removed async delays; targets now respawn immediately upon hit.
- **Center-Anchored Layout**: Targets and floating score indicators are anchored to `50% / 50%` coordinates with pixel offsets, guaranteeing complete visual stability during fullscreen transitions, window switches, and DPI zooming.
- **Dual Mode Support**: Targets are available both in the compact settings preview widget and in full-screen expanded view.
- **Interactive Shot Feedback**: Cyan reticle flash, Web Audio procedural harmonic chime, target explosion animation, and floating score popups.
- **Persistent High-Score Tracking**: Highest score tracked in local storage and displayed in the fullscreen HUD and Game Over screen.
- **Keyboard Controls**: `Space` to center gyro or restart, `Enter` to play again, `Esc` to toggle fullscreen.

### Aim Orientation & Pitch Alignment
- **Natural Bow Aiming**: Adjusted pitch direction so tilting the phone downward lowers the reticle and tilting upward raises it, matching standard first-person and third-person console gyro controls (e.g. Zelda: Tears of the Kingdom).

### UI Resilience & Stability
- **Auto-Recovery on Window Switching**: Automatically restores targets if the viewport loses focus, changes resolution, or switches between bench tabs.
- **LiveDebug 3D Window Hardening**: Set enforced minimum dimensions (`880x520`) to prevent card clipping and overflow during window resizing.
- **Shortcut Hints**: Added interface hints regarding `Ctrl +/-/0` zoom hotkeys in settings.

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
