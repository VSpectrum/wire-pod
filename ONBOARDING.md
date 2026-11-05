# Wire-Pod Onboarding & Developer Guide

Welcome to **`wire-pod`**! This document explains what this repository does, how it communicates with your Anki / Digital Dream Labs **Vector** robot, how to build and run the server, how to flash and pair your Vector so he reaches out to this server, and how to customize him (including local LLMs like LM Studio, custom voices, and audio playback).

---

## Table of Contents

1. [What Is `wire-pod` and How Does It Work?](#1-what-is-wire-pod-and-how-does-it-work)
2. [Features & Capabilities of This Repo](#2-features--capabilities-of-this-repo)
3. [How to Build and Run `wire-pod`](#3-how-to-build-and-run-wire-pod)
4. [Flashing Vector & Connecting Him to `wire-pod`](#4-flashing-vector--connecting-him-to-wire-pod)
   - [Path A: Regular Production Vector 1.0 / 2.0 (Escape Pod Firmware)](#path-a-regular-production-vector-10--20-escape-pod-firmware)
   - [Path B: OSKR / Dev-Unlocked / Custom Firmware (WireOS) Vector](#path-b-oskr--dev-unlocked--custom-firmware-wireos-vector)
5. [How to Verify Vector Is Communicating with `wire-pod`](#5-how-to-verify-vector-is-communicating-with-wire-pod)
6. [Integrating Locally Hosted LLMs (LM Studio / Ollama)](#6-integrating-locally-hosted-llms-lm-studio--ollama)
7. [How Updates & Code Changes Work (Server vs. Firmware)](#7-how-updates--code-changes-work-server-vs-firmware)
8. [Concurrency, Deadlock Prevention & Concurrency Testing](#8-concurrency-deadlock-prevention--concurrency-testing)
9. [Conversational Reliability, VAD & Dismissal Detection](#9-conversational-reliability-vad--dismissal-detection)

---

## 1. What Is `wire-pod` and How Does It Work?

Originally, Vector relied on Anki's cloud servers to process voice commands after waking up to *"Hey Vector"*. **`wire-pod` is a complete, self-hosted local cloud replacement** that runs on your own machine (PC, Linux box, Raspberry Pi, or Docker container) with zero subscription fees.

### Repository Architecture

The repo is divided into two main parts:

1. **`chipper/` (The Host Server — runs on your computer)**
   This is the core Go application you run and develop in. When started (`chipper/pkg/initwirepod/startserver.go`), it provides:
   - **mDNS Broadcaster (`chipper/pkg/mdnshandler/mdns.go`)**: Broadcasts the local hostname **`escapepod.local`** on your LAN so Vector can automatically discover your server's IP address.
   - **Chipper gRPC Voice Server (`chipper/pkg/servers/chipper/`)**: Listens on TLS ports **`443`** and **`8084`**. When you say *"Hey Vector"*, Vector streams raw microphone audio here. `wire-pod` transcribes the speech, matches an intent (or queries an LLM), and returns the result.
   - **Token & JDocs gRPC Servers (`chipper/pkg/servers/token/`, `chipper/pkg/servers/jdocs/`)**: Handles robot authentication tokens (`botInfo.json`) and synchronizes robot settings (`chipper/jdocs/`, such as eye color, volume, locale, and timers).
   - **Web UI & SDK Control Server (`chipper/pkg/wirepod/config-ws/`, `chipper/pkg/wirepod/sdkapp/`)**: Serves the web dashboard on port **`8080`** for server configuration, Bluetooth onboarding, custom intents, and real-time remote control.
   - **Outbound Robot SDK Client**: Once a robot is authenticated, `wire-pod` also connects **outward** to Vector's onboard gRPC server (`https://<vector-ip>:443`) to trigger animations, make Vector speak (`SayText`), stream custom audio (`ExternalAudioStreamPlayback`), or capture camera images.

2. **`vector-cloud/` (On-Robot Cloud Daemon — only needed for older OSKR/dev bots)**
   Contains the source and prebuilt ARM binary for `vic-cloud` (the process running inside Vector's Linux OS that talks to `chipper`) plus the `pod-bot-install.sh` onboarding reset script.

### End-to-End Voice Command Lifecycle

```text
[You say "Hey Vector..."]
       │
       ▼
[Vector Robot] ──(1. Streams mic audio over TLS gRPC :443/:8084)──► [wire-pod: Chipper Server]
                                                                           │
                                                                 (2. Local STT: Vosk / Whisper)
                                                                           │
                                                        ┌──────────────────┴──────────────────┐
                                                        ▼                                     ▼
                                           [Built-in / Custom Intent]             [Knowledge Graph / Local LLM]
                                           (Timers, Weather, Tricks,              (Streams tokens from LM Studio,
                                            Lua Scripts, Plugins)                  OpenAI, or Together AI)
                                                        │                                     │
                                                        ▼                                     ▼
[Vector Robot] ◄──(3. Returns IntentResult)─────────────┘                                     │
[Vector Robot] ◄──(4. Connects to <vector-ip>:443 via gRPC SDK to speak & animate)────────────┘
```

---

## 2. Features & Capabilities of This Repo

- **100% Local Speech-to-Text (STT)** (`chipper/cmd/`):
  - **Vosk** (default, fast, lightweight, supports 14 languages with automatic model download in the UI).
  - **Whisper.cpp** (high-accuracy local transcription for machines with stronger CPUs/GPUs).
  - **Coqui STT** and **Picovoice Leopard**.
- **Local & Cloud LLM Integrations ("Knowledge Graph")** (`chipper/pkg/wirepod/ttr/kgsim.go`, `kgsim_cmds.go`):
  - Supports **Custom OpenAI-compatible endpoints** (such as **LM Studio**, **Ollama**, **llama.cpp server**, **vLLM**), as well as OpenAI, Together AI, and Houndify.
  - **Intent-Graph Fallback**: Automatically sends unrecognized voice commands straight to your LLM without needing to say *"I have a question"*.
  - **LLM Robot Actions & Vision**: The LLM can embed commands in its reply (`{{playAnimationWI||happy}}`, `{{getImage||front}}`, `{{newVoiceRequest||now}}`) to express emotions while talking, snap a photo from Vector's camera for multimodal vision analysis, and hold multi-turn conversations.
  - **Per-Robot Chat Memory**: Remembers the last 16 messages of conversation per robot serial number (ESN).
- **Voice & Audio Customization**:
  - **Onboard Speech Synthesizer**: Supports both Vector's signature pitch-modulated voice (`UseVectorVoice: true`) and his raw, flat onboard robotic synthesizer (`UseVectorVoice: false`).
  - **External Audio Streaming**: Can stream arbitrary `8000 Hz` or `16000 Hz` 16-bit mono PCM audio directly to Vector's speaker (`ExternalAudioStreamPlayback` in `chipper/pkg/wirepod/sdkapp/server.go` and `kgsim_cmds.go`).
- **Custom Voice Intents, Lua Scripting & Go Plugins**:
  - Create custom voice triggers in the Web UI (`http://<server-ip>:8080` -> **Custom Intents**) that trigger built-in behaviors, execute host shell scripts, or run embedded **Lua scripts** (`chipper/pkg/scripting/scripting.go`) that drive Vector's motors, head, lift, face screen, and speech.
- **Built-in Web Remote Control (`/sdkapp`)**:
  - Drive Vector with `WASD` keys, view his live camera stream, upload `.wav` files to play through his speaker, make him say text, and customize his eye color and volume.
- **Weather Forecasts** (`chipper/pkg/wirepod/ttr/weather.go`):
  - Connects to OpenWeatherMap or WeatherAPI so *"Hey Vector, what's the weather?"* plays Vector's native weather animations with local temperatures.

---

## 3. How to Build and Run `wire-pod`

> **Important Network Requirement**: `wire-pod` and Vector **must be on the same local subnet/LAN**, and `wire-pod` needs to bind to ports **`80`**, **`443`**, **`8080`**, **`8084`**, and **UDP `5353` (mDNS)**.

### Option A: Running with Docker Compose (Recommended for Windows / macOS / Linux)

#### One-Command Startup on Windows (`start-windows-docker.ps1`)
Because Docker Desktop on Windows runs inside an isolated virtual network (`172.x.x.x`), its internal mDNS broadcast (`escapepod.local`) cannot reach your physical Wi-Fi/LAN on its own. Use the included PowerShell startup script to automatically detect your LAN IP, pass `HOST_IP` into Docker, and start the Windows Bonjour (`dns-sd.exe`) mDNS proxy in the background:

```powershell
# Start wire-pod and advertise escapepod.local on your LAN:
.\start-windows-docker.ps1

# Rebuild the container after editing Go code in chipper/ and restart:
.\start-windows-docker.ps1 -Build
```

#### Manual Docker Compose Commands
1. From the root of this repository, build and start the container:
   ```bash
   docker compose up -d --build
   ```
2. **If running Docker Desktop on Windows manually**: Run this built-in Windows Bonjour command in PowerShell (replacing `192.168.1.155` with your PC's LAN IPv4 address) so Vector can find `escapepod.local`:
   ```powershell
   dns-sd.exe -P escapepod _app-proto._tcp local 8084 escapepod.local 192.168.1.155 txtv=0 lo=1 la=2
   ```
3. Open **`http://localhost:8080`** in your browser to complete the first-time setup wizard.
4. Whenever you edit Go code in `chipper/`, rebuild and restart with:
   ```bash
   docker compose up -d --build
   ```

### Option B: Running Natively on Linux (Ubuntu/Debian/Arch/Fedora) or WSL2

1. **Install dependencies and configure STT** (run once from the repo root):
   ```bash
   sudo ./setup.sh
   ```
   - When prompted for a Speech-to-Text service, choose **`3` (VOSK)** (recommended) or **`4` (Whisper)**.
   - This installs Go, `libvosk` / `whisper.cpp`, `avahi-daemon` (for mDNS), and creates `./chipper/source.sh`.

2. **Start `wire-pod` interactively** (great for development and seeing live terminal logs):
   ```bash
   sudo ./chipper/start.sh
   ```

3. **(Optional) Install as a background `systemd` service** so `wire-pod` starts automatically on boot:
   ```bash
   sudo ./setup.sh daemon-enable
   sudo systemctl start wire-pod
   ```
   - View logs anytime: `journalctl -fe | grep start.sh`
   - After making code changes in `chipper/`, recompile and restart the service:
     ```bash
     sudo ./setup.sh daemon-enable && sudo systemctl restart wire-pod
     ```

### Completing the First-Time Server Setup (`http://<server-ip>:8080`)

When you launch `wire-pod` for the very first time, open **`http://<server-ip>:8080`** in your browser. It will redirect you to `/initial.html`:
1. **Connection Method**:
   - Choose **Escape Pod** if you have a regular production Vector (recommended). This uses the built-in `./chipper/epod/ep.crt` certificate and broadcasts `escapepod.local` via mDNS.
   - Choose **IP Address** only if you have an OSKR / dev-unlocked / custom firmware robot and want Vector to connect directly to your server's IP using a self-generated TLS certificate (`../certs/cert.crt`).
2. **Speech-to-Text Language**: Select your language (e.g., `English (US)`) and click **Submit Settings**. `wire-pod` will download the Vosk model and immediately start the `chipper` gRPC server.

---

## 4. Flashing Vector & Connecting Him to `wire-pod`

For Vector to talk to `wire-pod` instead of Anki's defunct servers, he must be running firmware that trusts `wire-pod`'s certificate and points to `escapepod.local` (or your server's IP).

Choose **Path A** or **Path B** below depending on your robot:

---

### Path A: Regular Production Vector 1.0 / 2.0 (Escape Pod Firmware)

Any standard retail Vector can be flashed with official **Escape Pod firmware (`2.0.1.6076ep`)** directly from a Chrome/Edge browser over Bluetooth—no unlock code or SSH key required.

#### Step 1: Put Vector into Recovery Mode
1. Place Vector on his charger and make sure he is plugged in.
2. **Press and hold Vector's back button for ~15 seconds.** His backpack lights will turn off—**keep holding the button** until the rectangle lights on his back turn blue/teal and he boots onto the recovery screen (showing `anki.com/v` or a cloud icon).

#### Step 2: Enter Bluetooth Pairing Mode
1. Once Vector has booted into Recovery Mode, **double-press his back button**.
2. A key icon and a robot name (e.g., `Vector A1B2`) will appear on his face screen.

#### Step 3: Flash Escape Pod Firmware & Authenticate
1. Ensure `wire-pod` is running on your server and your server is on the **same 2.4 GHz Wi-Fi network** you will connect Vector to.
2. On a computer or Android device with **Bluetooth** and **Google Chrome or Microsoft Edge**, open either:
   - **`https://wpsetup.keriganc.com`** (recommended web Bluetooth setup portal), **OR**
   - **`http://<server-ip>:8080`** -> **Bot Setup** (if `wire-pod` was built with `USE_INBUILT_BLE=true` on a machine with a Bluetooth adapter).
3. Click **Pair with Vector**, select your `Vector XXXX` from the Bluetooth popup, and enter the **6-digit PIN** shown on Vector's face screen.
4. Select your **2.4 GHz Wi-Fi network** and enter the Wi-Fi password.
5. **OTA Firmware Update**:
   - Because Vector is in Recovery Mode, the setup page will automatically install the Escape Pod `.ota` firmware onto Vector (`2.0.1ep`).
   - Wait ~5–10 minutes while Vector downloads the firmware and reboots.
6. **Final Authentication (Handshaking with `wire-pod`)**:
   - Once Vector reboots into the newly flashed Escape Pod firmware, **double-press his back button again** on the charger to re-enter Bluetooth pairing mode.
   - Pair with him again in the browser and enter the new 6-digit PIN.
   - Click **Authenticate**.
   - During this step, Vector reaches out to **`escapepod.local`** on your network (your `wire-pod` server!), requests a token from `wire-pod`'s Token Server (`chipper/pkg/servers/token/token.go`), and saves it.
   - Click **Wake with wake-up animation**. Vector is now permanently paired with your `wire-pod` instance!

---

### Path B: OSKR / Dev-Unlocked / Custom Firmware (WireOS) Vector

If your Vector is an **OSKR** unit, a **dev-unlocked** bot, or flashed with custom firmware (such as **WireOS** from [ota.keriganc.com](https://ota.keriganc.com) / [viccyware.com](https://viccyware.com)):

1. Put Vector on his charger, double-press his back button, then raise and lower his lift to view his **IP Address** on his face screen.
2. Open **`http://<server-ip>:8080`** -> **Bot Setup**.
3. Scroll to **Configure an OSKR/dev-unlocked robot**:
   - Enter Vector's **IP address** and upload his **SSH private key** (for WireOS / dev firmware, the standard `ssh_root_key` is used; you can also run `sudo ./setup.sh scp <vector-ip>` from the terminal).
   - Click **Set up bot**.
   - Behind the scenes (`chipper/pkg/wirepod/setup/ssh.go`), `wire-pod` connects to Vector over SSH, copies `server_config.json` (which points Vector to your `wire-pod` server's IP/hostname) and `wirepod-cert.crt` onto `/data/data/`, and runs `/data/pod-bot-install.sh` to put Vector in onboarding mode without erasing faces or photos.
4. Once that finishes, complete the standard Bluetooth **Authenticate** step (Step 3.6 in Path A above) at `https://wpsetup.keriganc.com` or `http://<server-ip>:8080` -> **Bot Setup** so Vector and `wire-pod` exchange his GUID token.

---

## 5. How to Verify Vector Is Communicating with `wire-pod`

You can verify that Vector and `wire-pod` are properly connected using this checklist:

1. **Check the `wire-pod` Main Dashboard (`http://<server-ip>:8080`)**:
   - At the top of the home page (and under **Bot Settings** / `/sdkapp`), your Vector's serial number (e.g., `00abcdef`) and battery status icon will appear automatically once authenticated.
2. **Verify `chipper/botInfo.json` Exists**:
   - Once Vector authenticates with `wire-pod`, the server creates `chipper/botInfo.json` containing his `esn`, `ip_address`, and `guid` token, and creates settings files in `chipper/jdocs/vic:<esn>-vic.RobotSettings.json`.
3. **Watch the Live Log (`http://<server-ip>:8080` -> Log)**:
   - Open the **Log** tab in the web UI, check **Show all logs**, and say **"Hey Vector, what time is it?"** or **"Hey Vector, hello"**.
   - You should immediately see incoming audio stream logs, the transcribed text from Vosk/Whisper, and the matched intent sent back to Vector.
4. **Troubleshooting If Vector Fails to Activate or Shows a Cloud/Wi-Fi Error Icon**:
   - **mDNS Discovery (`escapepod.local`)**: From a computer on the same network, run `ping -4 escapepod.local`. It must resolve to the LAN IP of the machine running `wire-pod` (not `172.x.x.x`).
     - On **Windows + Docker Desktop**, run `.\start-windows-docker.ps1` (which launches `dns-sd.exe -P escapepod _app-proto._tcp local 8084 escapepod.local <LAN-IP> txtv=0 lo=1 la=2`).
     - On **Linux**, ensure `avahi-daemon` is running (`sudo systemctl status avahi-daemon`).
   - **Windows Firewall Blocking Inbound LAN Connections (`Public` Network Profile)**:
     - If your Windows Ethernet/Wi-Fi adapter is set to `Public`, Windows Firewall blocks Vector from reaching Docker on ports `80`, `443`, `8080`, and `8084`. Open **PowerShell as Administrator** and run:
       ```powershell
       New-NetFirewallRule -DisplayName "Wire-Pod LAN" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 80,443,8080,8084 -Profile Any
       Set-NetConnectionProfile -InterfaceAlias "Ethernet" -NetworkCategory Private
       ```
   - **VPN Software (e.g., PureVPN / NordVPN / OpenVPN)**:
     - Even when **Split Tunneling** is enabled for your browser (e.g., Chrome), VPN kernel filter services (such as `PureVPNNetworkGuardService`) block Docker Desktop's backend (`com.docker.backend.exe`) from opening outbound TCP sockets to Vector on `192.168.x.x:443` (`connectex: An attempt was made to access a socket in a way forbidden by its access permissions` in `%LOCALAPPDATA%\Docker\log\host\monitor.log`), causing **Bot Settings** to fail with `dial tcp 192.168.x.x:443: connect: connection refused`.
     - Either add `C:\Program Files\Docker\Docker\resources\com.docker.backend.exe` to your VPN's split-tunnel bypass list or disconnect the VPN while using `wire-pod`.
   - **401 (Unauthorized) in Bot Settings**:
     - If **Bot Settings** returns `401 (Unauthorized)`, you can re-sync Vector's SDK token without re-running Bluetooth setup by calling Vector's onboard `user_authentication` endpoint while Vector's IP in `/data/chipper/jdocs/botSdkInfo.json` is temporarily set to Docker's gateway (`172.25.0.1`), or by clicking **Activate** again on `http://localhost:8080` -> **Bot Setup**.
   - **IP Address Changed**: If your router assigns Vector a new DHCP IP address, `wire-pod` automatically updates it the next time you say *"Hey Vector"*. You can also reserve a static DHCP lease for both your `wire-pod` host and Vector in your router settings.

---

## 6. Integrating Locally Hosted LLMs (LM Studio / Ollama)

`wire-pod` has native support for any OpenAI-compatible local LLM server (`chipper/pkg/wirepod/ttr/kgsim.go`).

### Step-by-Step Setup with LM Studio:
1. Open **LM Studio**, load a model (e.g., Llama 3, Qwen, Mistral, or a Vision-capable model if you want camera support), and start the **Local Inference Server** (default port `1234`).
   - *Tip*: If `wire-pod` runs inside Docker or WSL2 while LM Studio runs on the Windows host, enable **"Serve on Local Network"** in LM Studio so it listens on `0.0.0.0:1234`.
2. Open **`http://<server-ip>:8080`** -> **Server Settings** -> **Knowledge Graph**.
3. Set **Knowledge Graph API Provider** to **`Custom`** and fill in:
   - **API Key**: `lm-studio` (any non-empty string)
   - **API Endpoint**: `http://localhost:1234/v1` (or `http://<your-pc-lan-ip>:1234/v1`)
   - **LLM Model Name**: The exact model identifier shown in LM Studio
   - **LLM Prompt**: Customize Vector's personality (or leave blank for the default prompt)
4. Check the optional feature boxes below:
   - **Enable intent-graph**: Sends any unrecognized *"Hey Vector, ..."* command straight to your local LLM.
   - **Allow the LLM to make the robot perform animations and other actions**: Lets the LLM control Vector's animations (`{{playAnimationWI||happy}}`), take camera photos (`{{getImage||front}}`), and keep listening (`{{newVoiceRequest||now}}`).
   - **Enable conversations via "I have a question"**: Enables 16-message chat history memory and multi-turn back-and-forth conversations.
5. Click **Apply Settings**. Changes take effect immediately without restarting `wire-pod`.

---

## 7. How Updates & Code Changes Work (Server vs. Firmware)

A common question when developing in this repo is: *"If I edit code in this repo, do I need to reflash or push an update to Vector?"*

- **No Reflashing Required for `chipper/` Changes**:
  All voice transcription, intent matching, LLM integration, Lua scripting, audio streaming (`ExternalAudioStreamPlayback`), and voice settings (`SayTextRequest` / `UseVectorVoice`) live inside **`chipper/` on the server**. When you modify Go or web files in `chipper/`, simply **rebuild/restart the `wire-pod` server** and Vector will immediately use your new logic on the very next command.
- **`wire-pod` Never Automatically Pushes Firmware to Vector**:
  Once Vector is on Escape Pod firmware (`2.0.1ep`) or custom firmware, `wire-pod` never overwrites or updates Vector's OS in the background. The `/api/get_ota` proxy endpoint in `chipper/pkg/wirepod/config-ws/webserver.go` is only ever called during the manual Recovery Mode Bluetooth setup wizard.

---

## 8. Concurrency, Deadlock Prevention & Concurrency Testing

Under high request loads or intermittent network dropouts, asynchronous gRPC streams between `wire-pod` and Vector must not deadlock or leak goroutines.

### Concurrency Primitives (`chipper/pkg/wirepod/ttr/deadlock_guard.go`)

- **`SafeSendBool(ch, val)` & `SafeSendString(ch, val)`**:
  Performs non-blocking channel dispatch using `select { case ch <- val: ... default: ... }`. Protects goroutines from blocking indefinitely if a receiving consumer has abandoned the channel or timed out.
- **`WaitBoolWithTimeout(ctx, ch, duration, opName)`**:
  Guarantees that waiting on channel signals is bounded by both context cancellation and explicit duration timeouts, logging diagnostic warnings upon missed deadlines.
- **`Watchdog`**:
  A lightweight operation monitor (`NewWatchdog(opName, deadline)`). If `watchdog.Stop()` is not called before the deadline expires, the watchdog dumps full runtime goroutine stack traces (`runtime.Stack`) to logs for immediate triage.

### Behavior Control Invariants (`chipper/pkg/wirepod/ttr/bcontrol.go`)

- Every gRPC call to Vector's Behavior Control service must dispatch `SafeSendBool(start, false)` on failure so callers waiting for control never hang.
- Busy-spinning polling loops (`default: continue`) have been eliminated in favor of clean blocking `select { case <-stop: case <-ctx.Done(): }`.

### Running Concurrency Tests

Automated tests in `chipper/pkg/wirepod/ttr/deadlock_test.go` verify all concurrency paths:
```bash
# Inside docker or Linux host:
cd chipper
go test -v -race ./pkg/wirepod/ttr/...
```

Tests cover:
- Channel unblocking on simulated network disconnects.
- Watchdog stack capture verification.
- 100-worker high-concurrency channel stress under the Go race detector.

---

## 9. Conversational Reliability, VAD & Dismissal Detection

When using LLM back-and-forth conversations (`save_chat: true` / `{{newVoiceRequest||now}}`), Vector previously could become stuck listening or loop back into the LLM on farewells. Several mechanisms now guarantee clean session termination:

### 1. Pure Silence Cutoff (`chipper/pkg/wirepod/speechrequest/speechrequest.go`)
Standard VAD only checked inactive frame count if sufficient active voice frames were recorded (`ActiveFrames > 18`). When a user remained silent, `ActiveFrames` stayed `0`, keeping Vector listening for up to 30 seconds.
- A dedicated silence threshold (`ActiveFrames < 8 && InactiveFrames >= 250`, or 2.5 seconds) immediately signals end-of-speech and concludes the voice request.

### 2. Conversation Dismissal Parser (`chipper/pkg/wirepod/ttr/conversation_exit.go`)
Common phrases to end a conversation:
- *"that's all"*, *"never mind"*, *"goodbye"*, *"bye"*, *"i'm good"*, *"no thanks"*, *"cancel"*, *"stop"*
These are matched via regex prior to routing to the LLM. Vector triggers his native confirmation or goodbye intent and immediately ends the voice session without incurring LLM latency or triggering follow-up listening.

### 3. Prompt Guard (`chipper/pkg/wirepod/ttr/kgsim_cmds.go`)
The system prompt for LLM conversations explicitly instructs the model never to output `{{newVoiceRequest||now}}` when acknowledging farewells or dismissals.

### 4. SDK AudioFeed Isolation
Background continuous `AudioFeed` streaming on Vector's SDK connection has been removed from `watcher.go` to keep the robot's hardware microphone queue unblocked for STT voice capture.


