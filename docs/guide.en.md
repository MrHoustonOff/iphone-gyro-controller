# GyroBridge User Guide

> **Important:** This guide should not be taken as dry or boring documentation. I strongly recommend reading it completely to avoid issues and save tons of time in the future. **It takes literally 5 minutes!**

---

## Contents

1. [Step 1. Download & Initial Setup](#step-1-download--initial-setup)
   - [Security & Antivirus Warnings](#security--antivirus-warnings)
   - [Smartphone Setup: Android](#smartphone-setup-android)
   - [Smartphone Setup: iOS (iPhone)](#smartphone-setup-ios-iphone)
   - [Why all this hassle with certificates?](#why-all-this-hassle-with-certificates)
2. [Step 2. Smartphone Connection & Mobile Interface](#step-2-smartphone-connection--mobile-interface)
   - [Mobile Interface Breakdown](#mobile-interface-breakdown)
   - [Disconnect & SafeScreen Buttons](#disconnect--safescreen-buttons)
   - [Install as Web App (PWA) & Static IP](#install-as-web-app-pwa--static-ip)
3. [Step 3. Profiles, Calibration & Centering](#step-3-profiles-calibration--centering)
   - [Why do profiles matter?](#why-do-profiles-matter)
   - [Step-by-Step Calibration](#step-by-step-calibration)
   - [Calibration vs Centering (Recenter): Key Difference](#calibration-vs-centering-recenter-key-difference)
4. [PC Telemetry & Settings](#pc-telemetry--settings)
5. [Emulator Setup (Cemu Example)](#emulator-setup-cemu-example)
6. [Known Issues & Solutions](#known-issues--solutions)
   - [Cemu Launch Order Quirk](#cemu-launch-order-quirk)

---

## Step 1. Download & Initial Setup

Download the latest version of GyroBridge from GitHub Releases:  
**[Download GyroBridge (GitHub Releases)](https://github.com/MrHoustonOff/iphone-gyro-controller/releases/latest)**

### Security & Antivirus Warnings

Most likely, your antivirus or built-in <span style="color: #007aff">**Windows Defender (SmartScreen)**</span> will complain about the downloaded `.exe` file or silently block it.

> [!NOTE]
> I have emphasized the security of my code many times. **GyroBridge is a 100% transparent open-source project (<span style="color: #34c759">Open Source</span>).** If you have even the slightest doubt:
> 
> 1. You can inspect every single line of code directly in this repository.
> 2. Build the executable yourself from scratch using Go and Wails.
> 
> If you trust me — simply restore the file from your antivirus quarantine and/or click in Windows SmartScreen: <kbd>More info</kbd> → <kbd>Run anyway</kbd>.

On the first launch on your PC, click the <kbd>Initial Setup</kbd> button. A QR code will open to establish a secure local communication channel between your phone and PC.

---

### Smartphone Setup: Android

On Android, everything is remarkably simple and requires zero digging into system settings:

1. Scan the Step 1 QR code with your camera and open the local `https://...` link.
2. Chrome will display a standard self-signed certificate warning: <span style="color: #ff9500">*«Your connection is not private»*</span>.
3. Tap at the bottom: <kbd>Advanced</kbd> → <kbd>Proceed to ... (unsafe)</kbd>.
4. **Done!** The browser will permanently remember this exception for your home IP, and the page will open instantly with a single tap in the future.

![Android SSL Setup](imgs/setup-android-ssl.png)

---

### Smartphone Setup: iOS (iPhone)

iOS setup is explained in step-by-step detail **directly inside the app** on PC — under the <kbd>Initial Setup</kbd> tab or in <kbd>Help</kbd> → *Initial Setup* (complete with visual screenshots for each step in iOS Settings).

---

### Why all this hassle with certificates?

> **Technical Fact:**  
> Modern mobile web browsers (especially Safari on iOS and Chrome on Android) strictly **block gyroscope and accelerometer sensor APIs over insecure plain `http://` connections** for security reasons.
> 
> Buying an official public TLS certificate for a private home LAN makes no technical or financial sense. This application is **completely local and runs 100% offline**.
> 
> You generate a local root certificate (for iPhone) or explicitly trust **YOUR OWN LOCAL IP ADDRESS** (for Android). This is completely safe, keeps everything local without third-party servers, and grants sensor access. **Thanks to this solution, you can turn literally any smartphone into a motion controller!**

---

## Step 2. Smartphone Connection & Mobile Interface

Now that your smartphone trusts your PC, scan the **main QR code** on the GyroBridge home screen.

The mobile web app will open in your mobile browser.

![Mobile Interface](imgs/mobile-ui-overview.png)

### Mobile Interface Breakdown

* **Top Bar:** Quick toggle between light and dark theme, and interface language switcher (<kbd>RU</kbd> / <kbd>EN</kbd>).
* **Sensor Permission (Motion Permission):**  
  If you are visiting for the first time or *«iOS decides to pull its classic Apple quirks»* (clearing permissions in a Safari tab) — a motion prompt will appear. Just tap the large <kbd>Grant Permission</kbd> button.
* **Digital 2D Level (Level Indicator):**  
  Provides an instant visual test: tilt the phone in your hands — the bubble should glide smoothly across the screen. If it reacts, sensors are transmitting live data at 60–120 Hz.
* **Interactive Status Pill:**  
  Displays current real-time network state:
  - <span style="color: #ff9500">🟡 **Connecting...**</span> — performing handshake and opening secure WSS channel.
  - <span style="color: #34c759">🟢 **Streaming (XX Hz, X ms)**</span> — sensors online, telemetry streaming to PC with zero lag. **Tapping this pill pauses the stream!**
  - <span style="color: #ff9500">⏸️ **Paused**</span> — transmission temporarily frozen (convenient if you need to set your phone down without disconnecting). Tapping it again instantly resumes streaming.
  - <span style="color: #ff3b30">🔴 **Disconnected**</span> — lost connection to server (ensure phone and PC are connected to the same Wi-Fi network).

---

### Disconnect & SafeScreen Buttons

At the bottom of the mobile screen are two critical action buttons:

1. **Disconnect:**  
   Gracefully terminates the current transmission session and frees the network stream.
2. **SafeScreen (Display Protection):**  
   > **I STRONGLY RECOMMEND ENABLING THIS FIRST THING EVERY SESSION!**
   
   > [!IMPORTANT]
   > **Crucial feature for long gaming sessions:**
   > - **Burn-in Protection & Battery Saver:** Switches the screen into a pitch-black minimalist mode (on OLED/AMOLED displays, black pixels are physically powered off, saving battery and preventing burn-in).
   > - **Sleep Prevention (<span style="color: #007aff">WakeLock</span>):** SafeScreen **guarantees your phone screen will never turn off or go to sleep** during gameplay. The phone will continuously stream gyro data for as many hours as your session lasts.

---

### Install as Web App (PWA) & Static IP

You don't need to scan the QR code with your camera every time — you can save the site as a home screen web app:

* **On iOS (Safari):** Tap the *Share* button (square with arrow pointing up) → select <kbd>Add to Home Screen</kbd>.
* **On Android (Chrome):** Tap the three dots in the top-right corner → select <kbd>Add to Home screen</kbd> (or *«Install app»*).

> [!TIP]
> **Will the IP address change?**  
> In most home Wi-Fi networks, the router assigns a stable local IP to your PC for weeks at a time (DHCP Lease). Therefore, the saved icon on your phone will reconnect seamlessly. You only need to rescan the QR code if your router assigns a new local IP to your computer.

---

## Step 3. Profiles, Calibration & Centering

After connecting your smartphone for the first time, you need to create an orientation profile and calibrate the device.

### Why do profiles matter?

Every player holds their controller differently: some mount their phone horizontally on a gamepad clip on top, some keep it vertical beside them, and some hold the phone directly in two hands as a standalone steering wheel.

**The profile memorizes geometry:** how your smartphone's physical axes align with your monitor. You can create different profiles for different setups (e.g. *«Zelda (Gamepad Clip)»*, *«Racing (Horizontal Wheel)»*).

![Calibration Window](imgs/calibration-step.png)

### Step-by-Step Calibration

The calibration wizard on your PC will guide you through three simple steps:

1. **Step 1: Rest (Zero-Bias)** — place your smartphone completely stationary in its neutral resting position for 2–3 seconds. GyroBridge measures thermal sensor noise and eliminates drift (*unwanted cursor or crosshair crawling*).
2. **Step 2: Pitch Forward** — smoothly tilt the phone forward away from you and return. The algorithm identifies the vertical pitch axis.
3. **Step 3: Turn Right (Yaw / Roll)** — rotate the device to the right. The program locks in the horizontal turning axis.

---

### Calibration vs Centering (Recenter): Key Difference

It is vital to pause here and understand the fundamental physical difference:

> [!NOTE]
> **Emulators (Cemu, Dolphin, Ryujinx) do not care about the phone's absolute tilt angle relative to the horizon!**  
> Games care strictly about **angular velocity** (how fast and in which direction your hands are turning right now).
> 
> - **Calibration** is performed once: it teaches GyroBridge your personal coordinate system (where *«forward»* and *«right»* are relative to your grip).
> - **Centering (<span style="color: #007aff">Recenter / Reset 3D View</span>)** exists **exclusively for your visual convenience INSIDE THIS APP**. If the 3D model in the PC preview gets out of sync with the physical phone on your desk — simply hold your hands comfortably and click Recenter.

---

## PC Telemetry & Settings

* **«Statistics» Tab (Telemetry):**  
  Enables real-time monitoring of connection quality: polling rate (FPS), bitrate, network ping (typically a solid <span style="color: #34c759">**1–3 ms**</span> over local Wi-Fi), and the live list of connected emulator clients with their exact IP and port.
* **«Settings» Tab:**  
  Configure DSU port (`26760`), select network interfaces, customize close-button behavior (minimize to system tray with ultra-low RAM usage), and switch UI language.

![Statistics and Settings](imgs/pc-stats-and-settings.png)

---

## Emulator Setup (Cemu Example)

GyroBridge runs on the industry-standard **Cemuhook DSU (UDP)** protocol. To any emulator, your smartphone behaves identically to a genuine 6-axis motion controller (DualShock 4 / DualSense / Nintendo Switch Pro Controller).

### Step-by-Step Configuration in Cemu 2.0+

1. Open Cemu → navigate to <kbd>Options</kbd> → <kbd>Input Settings</kbd>.
2. Select the **Controller 1** tab (or the player slot you are playing on).
3. **Emulated Controller:** strictly select <span style="color: #007aff">**Wii U GamePad**</span>.
   > [!WARNING]
   > **DO NOT select Wii U Pro Controller!** On the original Wii U console, the Pro Controller physically **did not have a gyroscope**. In games like *The Legend of Zelda: Breath of the Wild*, motion aiming and apparatus shrines read motion data **exclusively from the Wii U GamePad**!
4. **Controller (Input Source):**
   - Click the **`+`** button next to the controller list.
   - In the **API** dropdown, select **`DSUClient`** (or `DSUController`).
   - Ensure the IP is set to `127.0.0.1` and port is `26760`.
   - In the discovered device list, choose `Controller 1` and click <kbd>Add</kbd>.
5. Select the added `Controller 1 [DSUController]`, click the <kbd>Settings</kbd> button below it, and verify that <span style="color: #34c759">**«Use motion»**</span> is checked.
6. Click <kbd>Save</kbd> profile.

![Cemu Input Settings](imgs/cemu-input-settings.png)

---

## Known Issues & Solutions

### Cemu Launch Order Quirk

If you launch Cemu **BEFORE** launching GyroBridge:

- Cemu sends an initial UDP probe packet to `127.0.0.1:26760` upon startup.
- Because GyroBridge was **not running yet**, the Windows network stack responds with an ICMP *port unreachable* error.
- Due to the absence of a background retry timer in Cemu's UDP client, the socket worker thread **falls asleep**. In Cemu's UI, the controller plug icon may still appear connected, but motion in-game stays frozen and GyroBridge displays: <span style="color: #ff9500">*«Waiting for emulators»*</span>.

### Solutions:

* **Method 1 (Recommended & Easiest):**  
  Always launch **GyroBridge BEFORE launching Cemu** (or enable GyroBridge autostart to system tray on Windows boot — it consumes less than 20 MB of RAM and 0% CPU).
* **Method 2 (On the fly without closing the game):**  
  If your game is already running and you don't want to close it:
  1. In Cemu, open <kbd>Options</kbd> → <kbd>Input Settings</kbd>.
  2. On your controller line, click **`-`** (remove DSUController) and immediately click **`+`** (re-add: `DSUClient` → `Controller 1`).
  3. This forcefully reinitializes Cemu's UDP socket, and motion data connects instantly without restarting the game!
