# TPPV4 — Telepresence Robot

A WebRTC-based telepresence robot. A remote pilot connects via browser, controls drive/camera/laser in real-time, and sees/hears the robot's environment. The robot auto-accepts connections and displays the pilot's video + audio on its own screen.

## Development Setup

- **demeter** (macOS) — main development machine; `make build` / `make run` work without any extra deps
- **fuego** (Ubuntu 24.04) — Linux test machine; requires system libs before building:

```bash
# On fuego (first time only):
sudo apt install libvpx-dev libasound2-dev
```

Deploy source from demeter and build+run on fuego:
```bash
# From demeter:
make deploy-src        # rsyncs source and builds on fuego (mattmc@fuego by default)

# Or SSH to fuego and run directly:
make run
# Open pilot UI: http://fuego:8080
```

> **RPi optimisation note:** VP8 (libvpx software encoding) works on both x86 and Raspberry Pi OS. When development moves to the real RPi, swap to the Broadcom MMAL hardware H.264 encoder for better performance — see `robot/internal/media/media.go` for instructions.


## Hardware

- **SBC**: Raspberry Pi 4B (4GB)
- **Camera**: RPi Camera Module v3 (MIPI CSI)
- **Servos/Laser**: [Pololu Maestro](https://www.pololu.com/category/102/maestro-usb-servo-controllers) USB servo controller
  - Channel 0: camera pan servo
  - Channel 1: camera tilt servo
  - Channel 2: laser pointer (digital on/off via pulse width)
- **Drive motors**: TBD UART/USB motor driver (behind `MotorController` interface)
- **Robot display**: HDMI screen running Chromium kiosk mode
- **Speaker**: USB or 3.5mm audio output

## Configuration

Copy and edit the config file:
```bash
cp robot/config/config.example.yaml config.yaml
# Edit MAESTRO_PORT, MOTOR_PORT, etc.
```

Or use environment variables:
```bash
MAESTRO_PORT=/dev/ttyACM0 MOTOR_DRIVER=uart MOTOR_PORT=/dev/ttyUSB0 ./tppv4-robot
```

## Deploy to Raspberry Pi

```bash
make deploy ROBOT_HOST=pi@192.168.1.100
```

## Install User Services on Raspberry Pi

The project includes user-level systemd service files for the robot process and Chromium kiosk display:

- `deploy/systemd/user/tppv4-robot.service`
- `deploy/systemd/user/tppv4-kiosk.service`
- `deploy/bin/tppv4-kiosk`
- `deploy/bin/tppv4-kiosk-postcheck`

Install them on the Pi after cloning/syncing this repo to `~/tppv4`:

```bash
make deploy-services ROBOT_HOST=mattmc@tpp.local
```

Manual install steps (equivalent to `make deploy-services`):

```bash
ssh mattmc@tpp.local
cd ~/tppv4

# Install kiosk helper scripts.
install -Dm755 deploy/bin/tppv4-kiosk ~/.local/bin/tppv4-kiosk
install -Dm755 deploy/bin/tppv4-kiosk-postcheck ~/.local/bin/tppv4-kiosk-postcheck

# Install user services.
install -Dm644 deploy/systemd/user/tppv4-robot.service ~/.config/systemd/user/tppv4-robot.service
install -Dm644 deploy/systemd/user/tppv4-kiosk.service ~/.config/systemd/user/tppv4-kiosk.service

# Keep user services running after reboot without an active login shell.
sudo loginctl enable-linger "$USER"

systemctl --user daemon-reload
systemctl --user enable --now tppv4-robot.service
systemctl --user enable --now tppv4-kiosk.service

# Verify
systemctl --user status tppv4-robot.service --no-pager
systemctl --user status tppv4-kiosk.service --no-pager
```

Operational notes:

- `make deploy` rebuilds the robot binary and restarts `tppv4-robot.service`.
- The kiosk service runs Chromium in kiosk mode at `http://localhost:8080/display`.
- The kiosk launcher waits for the robot HTTP server on `:8080` before opening Chromium, to avoid boot-time "site can't be reached" pages.
- The kiosk postcheck waits for startup and restarts kiosk once if display health reports a disconnected browser while relay video exists.

## Architecture

See [`docs/webrtc-what-is-the-best-architecture-and-framework.md`](docs/webrtc-what-is-the-best-architecture-and-framework.md) for the full research report and architecture decisions.

### Directory Structure

```
tppv4/
├── deploy/                # Deployment artifacts for Raspberry Pi user services
│   ├── bin/
│   │   ├── tppv4-kiosk
│   │   └── tppv4-kiosk-postcheck
│   └── systemd/
│       └── user/
│           ├── tppv4-kiosk.service
│           └── tppv4-robot.service
├── robot/                 # Go robot process (WebRTC + hardware control)
│   ├── cmd/robot/         # main.go entry point
│   ├── internal/
│   │   ├── signaling/     # WebSocket SDP/ICE exchange
│   │   ├── webrtc/        # Peer connection management
│   │   ├── media/         # Camera + mic capture, speaker output
│   │   ├── hardware/
│   │   │   ├── maestro/   # Pololu Maestro serial driver
│   │   │   └── motor/     # MotorController interface + UART implementation
│   │   └── control/       # Command dispatcher (data channel → hardware)
│   └── config/            # Config struct (env vars + YAML)
└── pilot/                 # Browser UI (Vanilla JS)
    ├── index.html         # Pilot control interface
    ├── display.html       # Robot screen display page (Chromium kiosk)
    ├── js/
    │   ├── webrtc.js      # RTCPeerConnection + signaling
    │   ├── controls.js    # Joystick, keyboard, gamepad
    │   ├── ui.js          # Status display
    │   └── protocol.js    # Data channel message helpers
    └── css/style.css
```

## Pilot Controls

| Input | Action |
|-------|--------|
| Left joystick (touch) | Drive |
| Right joystick (touch) | Camera pan/tilt |
| LASER button | Toggle laser |
| WASD / Arrow keys | Drive |
| Space | Emergency stop |
| L key | Toggle laser |
| Gamepad left stick | Drive |
| Gamepad right stick | Camera |
| Gamepad LB (button 4) | Toggle laser |
