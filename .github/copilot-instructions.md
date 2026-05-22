# Copilot Instructions

TPPV4 is a WebRTC-based telepresence robot. A remote pilot connects via browser to control a Raspberry Pi 4B robot in real-time — driving, panning/tilting a camera, and toggling a laser pointer. The robot auto-accepts connections and displays the pilot's video/audio on its own screen.

## Build, Test & Lint

```bash
# Build robot binary (local)
make build

# Run locally (no hardware — uses NoOp motor driver)
make run

# Run all Go tests
make test          # or: cd robot && go test ./...

# Run a single test package
cd robot && go test ./internal/hardware/maestro/ -run TestSetTarget

# Lint
make lint          # or: cd robot && go vet ./...

# Cross-compile for Raspberry Pi (ARM64)
make build-arm

# Deploy to RPi
make deploy ROBOT_HOST=pi@192.168.1.100
```

## Architecture

This is a **monorepo**: `robot/` (Go backend) + `pilot/` (Vanilla JS browser UI).

**Data flow:**
1. Robot Go process starts an HTTP server (`robot/cmd/robot/main.go`)
2. Robot serves the pilot UI at `/` and handles WebSocket signaling at `/ws`
3. Pilot browser connects via WebSocket, exchanges SDP offer/answer + ICE candidates
4. WebRTC peer connection established:
   - Robot camera (V4L2 → MMAL H.264) + mic (ALSA → Opus) stream to pilot
   - Pilot camera + mic stream to robot (displayed via Chromium kiosk at `/display`)
   - Three data channels carry control commands: `drive` (unreliable), `servo` (unreliable), `laser` (reliable)
5. Data channel messages → `control.Dispatcher` → Pololu Maestro (servos/laser) or `MotorController` (drive motors)

**Hardware control layer:**
- `robot/internal/hardware/maestro/` — Pololu Maestro USB servo controller (Compact Protocol over serial)
  - Channel 0 = camera pan, Channel 1 = camera tilt, Channel 2 = laser
- `robot/internal/hardware/motor/` — `MotorController` interface; `UARTMotorController` for real hardware, `NoOpMotorController` for dev
- Config loaded at startup from `config.yaml` (or env vars), see `robot/config/`

## Key Conventions

- **Maestro is nil-safe**: the zero-value `&maestro.Maestro{}` silently no-ops all commands — safe for dev machines without hardware.
- **Motor driver is swapped via config**: `motor_driver: noop` (default) vs `motor_driver: uart`. Add new drivers by implementing `motor.MotorController`.
- **Data channels created before the SDP offer**: `pilot/js/webrtc.js` creates all four channels (`drive`, `servo`, `laser`, `status`) before calling `createOffer()` so they appear in SDP negotiation.
- **Drive/servo channels are unreliable** (`ordered: false, maxRetransmits: 0`): stale commands are useless. Laser and status channels are reliable.
- **Config precedence**: YAML file → env var overrides (see `robot/config/config.go`).
- **Differential drive math**: `left = y - x`, `right = y + x` (clamped ±1.0) in `motor.DifferentialDrive()`.
- **Servo angles**: dispatcher tracks current `panDeg`/`tiltDeg` state for `servo_delta` incremental commands.
