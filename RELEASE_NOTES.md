# GyroBridge Release v0.3.0 — End-to-End Motion Pipeline

**Release Date:** September 17, 2026  
**Build Target:** Windows x64 (`GyroBridge-GUI.exe`)  
**Status:** Stable / Milestone Release  

---

## Executive Summary

GyroBridge v0.3.0 marks the completion of the full end-to-end motion pipeline: turning any modern smartphone (iOS Safari / Android Chrome) into a zero-latency, ultra-precise motion controller for PC emulators and games via the Cemuhook DSU protocol.

From the automated in-browser pairing and local Certificate Authority to the 4-step 3D calibration wizard, real-time Madgwick AHRS sensor fusion, and jitter-free DSU telemetry streaming, the complete workflow operates autonomously in a single static executable without external servers, drivers, or runtime dependencies.

---

## Key Features & Highlights

### 1. Interactive 4-Step 3D Calibration Wizard
- **Step 0 — Stillness & Gravity Vector Acquisition:** Captures static gravity vector while resting flat on the table and calculates zero-rate gyro drift bias.
- **Step 1 & 2 — Pitch and Roll Gesture Recognition:** 60 Hz unconstrained motion capture with directional confidence gating. Automatically detects the principal movement axis regardless of whether the phone is held in portrait or landscape.
- **Step 3 — Orthogonal Matrix Verification:** Constructs transformation matrix `M` with strict determinant validation (`det(M) = +1`), guaranteeing a pure 3D rotation without coordinate inversion, reflection, or axis skewing.
- **Multi-Slot Profile Engine:** 4 independent configuration slots with persistent storage in `%APPDATA%`, live slot switching, and custom naming.
- **Interactive 3D Visualizer:** Built-in Three.js preview rendered directly inside the calibration window with real-time feedback and responsive Apple Human Interface Guidelines styling.

### 2. Standalone 3D Live Debug Window
- **Dedicated Telemetry Viewport:** Secondary viewport available as a native child window or served independently over HTTP/WebSocket (`livedebug.html`).
- **Tri-Camera System:**
  - **Static Mode:** Locked orthographic front perspective for precise level verification.
  - **Quad View Mode:** Synchronized 2x2 multi-angle view (Front, Top, Right, Perspective) for holistic motion validation.
  - **Dynamic Orbit Mode:** Smooth Arcball camera controls with zoom and rotation.
- **Eco Rendering Mode:** Pauses WebGL rendering loop when the window is minimized or out of focus, reducing GPU utilization to 0.0%.
- **2D Bubble Reticle & Euler Readout:** High-precision reticle indicator and live numerical Pitch / Roll / Yaw degree indicators with Dark/Light theme synchronization.

### 3. Jitter-Free Cemuhook DSU Motion Streaming
- **Resting Table Lock:** When the device rests flat on the table, accelerometer output locks strictly to `[0.0, -1.0, 0.0]g`. This zeroes the error gradient in client-side AHRS filters (such as PadTest's Madgwick implementation), completely eliminating 60 Hz limit-cycle oscillation and wireframe jitter.
- **Soft-Knee Gyroscope Deadband (0.25 deg/s):** Rejects ambient MEMS electrical/thermal noise in stationary state while employing a smooth continuous linear ramp on movement onset, eliminating snap artifacts and preserving micro-aiming precision.
- **Dynamic EMA Filtering:** Exponential moving average low-pass filter (`alpha = 0.20`) suppresses high-frequency sensor noise during active motion without introducing noticeable latency.
- **DSU Protocol Compliance:** Ingests 60-100 Hz sensor frames and maps them directly to standard Cemuhook DSU conventions (Port 26760), delivering plug-and-play compatibility with Cemu, Dolphin, Ryujinx, Yuzu forks, and PadTest.

### 4. Zero-Config Local Certificate Authority & Pairing
- **Built-in crypto/x509 CA:** Generates a 10-year root certificate authority and automatic SAN leaf certificates for `gamepad.local` and LAN IP addresses.
- **Single-Tap Apple Configuration Profile:** Generates `.mobileconfig` payload for frictionless iOS Safari root trust setup.
- **Dual Pairing QR Engine:** Dynamic QR codes for zero-typing connection on both iOS and Android browsers.
- **Embedded Web Assets:** UI, styles, icons, and client scripts embedded directly into the Go executable via `//go:embed`.

---

## Technical Specifications

| Parameter | Value |
|---|---|
| Binary Architecture | Windows x64 (Single Standalone Executable) |
| Transport Latency | 1 - 3 ms (Local Wi-Fi WSS) |
| Ingest Protocol | WebSocket (`wss://gamepad.local:8443`) |
| Output Protocol | Cemuhook DSU Protocol (`UDP 26760`) |
| Target Client Frequency | 60 - 100 Hz |
| Stationary Gyro Drift | 0.000 deg/s (Active Bias Cancellation + Soft-Knee Deadband) |
| Stationary Accel Jitter | 0.000g (Table Resting Lock) |
| Coordinate System | Right-Handed Cartesian (`det = +1.0`) |

---

## Verification & Integrity

- **Go Test Suite:** All 12 unit and integration tests passing (`go test -v ./...`).
- **Binary Hash Parity:** Verified SHA-256 match between root executable and build artifact:
  - `GyroBridge-GUI.exe`: `1A91ECAB714B90CC18FCC45CCA3D577D0AC5AE912F08B3DC115EF8A4C4D9D3D2`
  - `gui/build/bin/GyroBridge-GUI.exe`: `1A91ECAB714B90CC18FCC45CCA3D577D0AC5AE912F08B3DC115EF8A4C4D9D3D2`
