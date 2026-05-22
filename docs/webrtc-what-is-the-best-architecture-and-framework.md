# WebRTC Telepresence Robot — Architecture & Framework Research Report

> **Scope**: A WebRTC-based telepresence robot where a remote pilot connects to the robot over the internet (different towns). The pilot has on-screen controls for drive, camera pan/tilt, and laser pointer. The robot auto-accepts connections and has a screen/speakers to show the pilot's video/audio. **No ROS — direct hardware control only.**

---

## Executive Summary

The best architecture for this project pairs **Raspberry Pi 4B** hardware with either **Go + pion/webrtc** (recommended for production, lowest latency) or **Python + aiortc** (fastest prototyping path, best Adafruit ecosystem support) on the robot side. The pilot uses a **browser-based vanilla JS UI** with WebRTC data channels to send drive, servo, and laser commands. **TURN relay infrastructure (Coturn) is mandatory** for reliable cross-internet connections — approximately 15–30% of real-world connections will fail without it. For signaling, two viable patterns exist: a **full SFU via LiveKit** (batteries-included, handles TURN/signaling/auto-accept automatically) or a **lean custom WebSocket signaling server + P2P WebRTC** (more control, lower infrastructure cost). The clearest real-world reference implementation is [`dhonanhibatullah/panzerbot`](https://github.com/dhonanhibatullah/panzerbot) — a Go/pion robot on RPi 4B with GPIO motors, sysfs PWM servos, pion/mediadevices MMAL H.264 encoding, and WebSocket signaling.

---

## Table of Contents

1. [System Architecture Overview](#1-system-architecture-overview)
2. [Signaling Architecture: Two Viable Patterns](#2-signaling-architecture-two-viable-patterns)
3. [Robot Hardware](#3-robot-hardware)
4. [Robot Software Stack](#4-robot-software-stack)
5. [Pilot Browser UI](#5-pilot-browser-ui)
6. [WebRTC Data Channel Command Protocol](#6-webrtc-data-channel-command-protocol)
7. [Motor Control (Drive)](#7-motor-control-drive)
8. [Camera Pan/Tilt Servo Control](#8-camera-pantilt-servo-control)
9. [Laser Pointer Control](#9-laser-pointer-control)
10. [Robot Screen & Speaker Output](#10-robot-screen--speaker-output)
11. [TURN/STUN Infrastructure](#11-turnstun-infrastructure)
12. [Key Repositories](#12-key-repositories)
13. [Confidence Assessment](#13-confidence-assessment)
14. [Footnotes](#footnotes)

---

## 1. System Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                       CLOUD / VPS                                    │
│                                                                      │
│  ┌──────────────────────────┐   ┌───────────────────────────────┐   │
│  │  Signaling Server        │   │  Coturn TURN Server           │   │
│  │  (WebSocket or LiveKit)  │   │  port 3478 (UDP/TCP)          │   │
│  │  Exchanges SDP offers/   │   │  port 443  (TURNS/TLS)        │   │
│  │  answers + ICE candidates│   │  Relay when P2P fails         │   │
│  └──────────────────────────┘   └───────────────────────────────┘   │
└────────────────────────────────────────────────────────────────────--┘
         ↕ WebSocket signaling        ↕ TURN relay (if NAT fails)
┌─────────────────────────────────┐    ┌─────────────────────────────┐
│  ROBOT (Raspberry Pi 4B)        │    │  PILOT (Browser)            │
│                                 │    │                             │
│  Go (pion) or Python (aiortc)   │    │  Vanilla JS RTCPeerCon-     │
│  • Camera → V4L2 → MMAL H.264   │◄───┤  nection                    │
│  • Mic → ALSA → Opus            │    │  • Sees robot video/audio   │
│  • Recv pilot video → Chromium  │───►│  • On-screen drive joystick │
│  • Recv pilot audio → speaker   │    │  • Camera pan/tilt control  │
│                                 │    │  • Laser toggle button      │
│  DataChannel → motors, servos,  │◄───│  • Sends own video/audio    │
│               laser             │    │    to robot screen          │
└─────────────────────────────────┘    └─────────────────────────────┘
         ↓ GPIO / I2C / PWM
┌─────────────────────────────────┐
│  Hardware Layer (on robot)      │
│  • L298N/TB6612 motor driver    │
│  • Left + right drive motors    │
│  • Pan servo (GPIO 18, PWM0)    │
│  • Tilt servo (GPIO 19, PWM1)   │
│  • Laser (GPIO 17, digital)     │
│  • RPi Camera Module v3 (CSI)   │
│  • USB microphone or CSI mic    │
│  • HDMI screen + USB speaker    │
└─────────────────────────────────┘
```

**Data flow summary:**
- Robot camera/mic → WebRTC media tracks → pilot browser (live video/audio)
- Pilot camera/mic → WebRTC media tracks → robot Chromium display (bidirectional presence)
- Pilot control input → WebRTC **unreliable** data channel → robot GPIO (drive/servo)
- Laser toggle → WebRTC **reliable** data channel → robot GPIO pin

---

## 2. Signaling Architecture: Two Viable Patterns

### Pattern A: LiveKit SFU (Recommended for Production)

LiveKit is a full-stack SFU + signaling server in a single binary. It uniquely provides the **Agents framework** — a Python SDK designed for programmatic robot "participants" that auto-join rooms and react to media.[^1]

```
[Pilot Browser]  ──WebSocket──▶  [LiveKit Server (SFU)]  ◀──WebSocket──  [Robot Process]
                                         │
                             JWT Token Auth + Room model
                             Built-in TURN/ICE traversal
```

**Robot auto-accept with LiveKit Agents (Python):**[^2]
```python
from livekit.agents import AgentServer, JobContext

server = AgentServer()

@server.rtc_session()
async def entrypoint(ctx: JobContext):
    # Called automatically when pilot joins the room
    await ctx.connect()   # robot joins and publishes camera
    # subscribe to pilot video/audio, publish robot camera
```

**Why LiveKit for robots:**
- Robot holds a permanent JWT token; auto-called when pilot joins
- Built-in TURN handling — no separate Coturn needed (or bring your own)
- Python + Go + Rust + C++ SDKs (C++ SDK available for embedded boards)
- `participant_joined` webhook triggers robot streaming start
- Single binary Docker deploy: `docker run -p 7880:7880 livekit/livekit-server --dev`
- Self-hosted or LiveKit Cloud (free tier with bandwidth credits)
- **Media latency:** ~50–150ms (SFU adds ~15–30ms over raw P2P)[^1]

**Pros:** Most complete solution; eliminates all manual SDP/ICE/TURN management.  
**Cons:** SFU means media routes through server, adding ~15ms; more infrastructure than bare P2P.

---

### Pattern B: Custom WebSocket Signaling + pion/Peerjs (Lean & Full-Control)

A minimal Node.js or Go WebSocket server exchanges SDP offers/answers. Media goes P2P (STUN) or via a separate Coturn TURN server. This is the pattern used by `panzerbot`.[^3]

```
┌──────────────────────────────────────┐
│  Signaling Server (Node.js/Go WS)    │
│  Robot registers: {"id": "robot-01"} │
│  Pilot sends offer to robot-01       │
│  Robot auto-responds with answer     │
└────────┬───────────────┬─────────────┘
         │               │
    ┌────▼────┐   ┌───────▼──────────────┐
    │  Pilot  │   │  Robot (pion/aiortc) │
    │         │◄──│  OnTrack(autoAccept) │
    └─────────┘   └──────────────────────┘
         ↕                 ↕
    ┌──────────────────────────────────┐
    │  Coturn TURN Server (separate)   │
    └──────────────────────────────────┘
```

**Robot auto-accept (pion/webrtc — Go):**[^4]
```go
pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
    go func() {
        for { rtp, _, _ := track.ReadRTP() /* → decode, display */ }
    }()
})

pc.OnDataChannel(func(d *webrtc.DataChannel) {
    d.OnMessage(func(msg webrtc.DataChannelMessage) {
        handleRobotCommand(msg.Data) // drive, servo, laser
    })
})

// panzerbot pattern: robot creates offer, browser sends answer
offerSDP, _ := pc.CreateOffer(nil)
pc.SetLocalDescription(offerSDP)
// send over WebSocket → browser answers → connection established
```

**Why panzerbot sends the offer from the robot side (not the browser):**
The robot creates the offer immediately when the pilot's WebSocket connects, avoiding a round-trip for the browser to request media.[^5]

**Pros:** Minimal infrastructure; pure P2P (lowest possible latency ~20–80ms); full control.  
**Cons:** Must manage STUN/TURN manually; more code to write; no high-level room model.

---

### Pattern C: PeerJS (Quick Prototype Only)

```javascript
// Robot Node.js process — ~10 lines to auto-accept
const peer = new Peer("robot-001", { host: "signal.yourdomain.com", port: 9000 });
peer.on("call", call => {
  call.answer(robotMediaStream);  // auto-answer
  call.on("stream", pilotStream => displayPilotFeed(pilotStream));
});
```

PeerJS is good for rapid prototyping but **does not relay media** — if NAT traversal fails, the connection fails entirely.[^6] Use only for local network testing.

---

## 3. Robot Hardware

### Recommended: Raspberry Pi 4B (4GB RAM)

| Factor | RPi 4B | Jetson Nano | Winner |
|--------|--------|-------------|--------|
| H.264 hardware encoding | ✅ MMAL — built-in, zero config | NVENC (needs GStreamer NVIDIA plugins) | **RPi 4B** |
| Benchmarked WebRTC latency | **<500ms at 720p/30fps on RPi 3** | Not benchmarked with pion | **RPi 4B** |
| GPIO ecosystem (Go/Python) | `go-rpio`, `RPi.GPIO`, `gpiozero` — native 40-pin | 3.3V only, more complex | **RPi 4B** |
| I2C (Adafruit Motor HAT) | Works out of the box | Works but more setup | Tie |
| Power draw | ~5W | ~10W | **RPi 4B** |
| Camera | RPi Camera Module v3 (MIPI CSI, hardware ISP) | USB or CSI cameras | Tie |
| Cost | Lower | Higher | **RPi 4B** |

`pion/mediadevices` explicitly lists *"no installation needed, mmal should come built in Raspberry Pi devices"* for its MMAL H.264 codec.[^7]

**Camera recommendation:** RPi Camera Module v3 (12MP, autofocus, MIPI CSI) — `pion/mediadevices` captures it via `/dev/video0` (V4L2). Use RPi 5 if you want H.265 and better power efficiency.

---

## 4. Robot Software Stack

### Option A: Go + pion/webrtc + pion/mediadevices ✅ RECOMMENDED

Confirmed working in `panzerbot` on RPi 4B.[^8]

**go.mod dependencies:**[^9]
```go
require (
    github.com/peergum/go-rpio/v5  v5.0.3   // GPIO (motors, laser)
    github.com/pion/mediadevices   v0.9.4   // camera (MMAL H.264) + mic (Opus)
    github.com/pion/webrtc/v4      v4.2.11  // WebRTC peer connection
    github.com/gopxl/beep          v1.4.1   // audio playback to ALSA speaker
    github.com/gorilla/websocket   v1.5.3   // signaling WebSocket
    github.com/gin-gonic/gin       v1.12.0  // HTTP server
)
```

**Camera + Mic capture with MMAL hardware H.264:**[^10]
```go
import "github.com/pion/mediadevices/pkg/codec/mmal"

mmalParams, _ := mmal.NewParams()
mmalParams.BitRate = 1_000_000   // 1 Mbps

opusParams, _ := opus.NewParams()

codecSelector := mediadevices.NewCodecSelector(
    mediadevices.WithVideoEncoders(&mmalParams),
    mediadevices.WithAudioEncoders(&opusParams),
)

stream, _ := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
    Video: func(c *mediadevices.MediaTrackConstraints) {
        c.DeviceID = prop.String("/dev/video0") // V4L2
    },
    Audio: func(c *mediadevices.MediaTrackConstraints) {
        c.SampleRate   = prop.Int(48000)
        c.ChannelCount = prop.Int(1)
    },
    Codec: codecSelector,
})
```

**Why Go is recommended:**
- MMAL hardware H.264 encoding on RPi — <500ms latency benchmarked on RPi 3[^7]
- Single binary deployment, no runtime interpreter
- Strong concurrency (goroutines) for simultaneous motor control + WebRTC media

---

### Option B: Python + aiortc (Faster Prototyping)

```python
from aiortc import RTCPeerConnection, RTCSessionDescription
from aiortc.contrib.media import MediaPlayer, MediaRelay

# Linux V4L2 camera capture + Opus audio
options = {"framerate": "30", "video_size": "1280x720"}
webcam = MediaPlayer("/dev/video0", format="v4l2", options=options)
relay  = MediaRelay()  # share one camera across multiple peers

async def accept_connection(offer_sdp: str):
    pc = RTCPeerConnection(configuration=ice_config)

    @pc.on("datachannel")
    def on_datachannel(channel):
        @channel.on("message")
        def on_message(msg):
            cmd = json.loads(msg)
            handle_robot_command(cmd)   # drive, servo, laser

    # Add robot camera to outgoing stream
    video_sender = pc.addTrack(relay.subscribe(webcam.video))
    force_codec(pc, video_sender, "video/H264")  # prefer H.264

    # Auto-accept incoming offer
    await pc.setRemoteDescription(RTCSessionDescription(sdp=offer_sdp, type="offer"))
    answer = await pc.createAnswer()
    await pc.setLocalDescription(answer)
    return pc.localDescription.sdp
```

**Trade-off:** Python aiortc uses `PyAV` (libavcodec) for **software** VP8/H.264 — no MMAL, significantly higher CPU load and higher latency than Go on RPi.[^11]  
**When to choose Python:** If using Adafruit Motor HAT (`pip install adafruit-circuitpython-motorkit`) or prototyping quickly. Motor HAT has no native Go binding.

---

## 5. Pilot Browser UI

### Framework Recommendation: Vanilla JS

**Finding from multiple real robot UIs:** The control path (joystick/keyboard → `dc.send()`) must **never** go through a framework's reactive state system. Keep it in raw `requestAnimationFrame` or event handlers.[^12]

| Framework | Control-path latency | Verdict |
|-----------|---------------------|---------|
| **Vanilla JS** | Zero overhead | ✅ Best for hot path |
| **Svelte** | Compiles to vanilla, ~5KB runtime | ✅ Good for UI chrome (status panels, settings) |
| **React** | Virtual DOM reconciliation delays input | ⚠️ OK only if control loop is kept outside React state |

**Pattern:** Vanilla JS for the control loop; optionally Svelte for connection status display.

---

### RTCPeerConnection Setup (Full Pattern)[^13]

```javascript
const pc = new RTCPeerConnection(iceConfig); // see §11 for iceConfig

// --- Data channels (must be created BEFORE offer) ---
const driveDC  = pc.createDataChannel("drive",  { ordered: false, maxRetransmits: 0 });
const laserDC  = pc.createDataChannel("laser",  { ordered: true });
const statusDC = pc.createDataChannel("status", { ordered: true });

// --- Receive robot video/audio ---
pc.ontrack = (event) => {
  if (event.track.kind === "video") {
    const stream = new MediaStream([event.track]);
    robotVideo.srcObject = stream;
  } else if (event.track.kind === "audio") {
    robotAudio.srcObject = new MediaStream([event.track]);
    robotAudio.autoplay = true;
    robotAudio.muted = false;   // MUST NOT be muted
    robotAudio.play();
  }
};

// --- Declare robot tracks to receive ---
pc.addTransceiver("video", { direction: "recvonly" });
pc.addTransceiver("audio", { direction: "recvonly" });

// --- Send pilot's own camera/mic to robot screen ---
const localStream = await navigator.mediaDevices.getUserMedia({
  video: { facingMode: "user", width: { ideal: 640 }, height: { ideal: 480 } },
  audio: true,
});
localVideo.srcObject = localStream;   // local preview — MUST be muted
localStream.getTracks().forEach(track => pc.addTrack(track, localStream));

// --- Offer/Answer (POST to robot's HTTP signaling endpoint) ---
const offer = await pc.createOffer();
await pc.setLocalDescription(offer);

// Wait for ICE gathering to complete before sending
await new Promise(resolve => {
  if (pc.iceGatheringState === "complete") return resolve();
  pc.addEventListener("icegatheringstatechange", function check() {
    if (pc.iceGatheringState === "complete") {
      pc.removeEventListener("icegatheringstatechange", check);
      resolve();
    }
  });
});

const answer = await fetch("/offer", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ sdp: pc.localDescription.sdp, type: "offer" }),
}).then(r => r.json());

await pc.setRemoteDescription(answer);
```

**Critical HTML attributes:**[^13]
```html
<!-- Robot video/audio output -->
<video id="robotVideo" autoplay playsinline></video>
<audio id="robotAudio" autoplay playsinline></audio>   <!-- NOT muted -->

<!-- Pilot's own camera preview — MUST be muted to prevent echo feedback -->
<video id="localVideo" autoplay playsinline muted></video>
```

---

### Virtual Joystick for Drive Control

**Recommendation: Custom pointer-events joystick** (zero dependency, `setPointerCapture` for reliable tracking)[^14]

```javascript
function attachPad(padId, knobId, onMove, onRelease) {
  const pad  = document.getElementById(padId);
  const knob = document.getElementById(knobId);
  let active = false;

  const handleMove = (clientX, clientY) => {
    const rect = pad.getBoundingClientRect();
    const cx = rect.left + rect.width / 2;
    const cy = rect.top  + rect.height / 2;
    const maxR = rect.width * 0.35;
    let dx = clientX - cx, dy = clientY - cy;
    const dist = Math.hypot(dx, dy);
    if (dist > maxR) { dx *= maxR/dist; dy *= maxR/dist; }  // clamp
    const nx = dx / maxR, ny = dy / maxR;   // normalize -1..1
    knob.style.left = `${50 + nx * 35}%`;
    knob.style.top  = `${50 + ny * 35}%`;
    onMove(nx, ny);
  };

  pad.addEventListener("pointerdown", e => {
    active = true;
    pad.setPointerCapture(e.pointerId);  // track even if finger slides off
    handleMove(e.clientX, e.clientY);
  });
  pad.addEventListener("pointermove", e => { if (active) handleMove(e.clientX, e.clientY); });
  const release = () => { if (!active) return; active = false; knob.style.cssText = "left:50%;top:50%"; onRelease(); };
  pad.addEventListener("pointerup", release);
  pad.addEventListener("pointercancel", release);
}

// Attach drive pad (bottom-left) and camera pad (bottom-right)
attachPad("drivePad",  "driveKnob",
  (x, y) => sendControl({ type: "drive", x, y: -y }),
  ()     => sendControl({ type: "stop" })
);
attachPad("cameraPad", "cameraKnob",
  (x, y) => sendControl({ type: "servo", pan: Math.round(-x * 90), tilt: Math.round(y * 90) }),
  ()     => {}   // hold last camera position on release
);
```

**Alternative: nipplejs** (`yoannmoinet/nipplejs`, 5K+ stars) — better for multitouch where two fingers run independent joysticks simultaneously.[^15]

---

### Keyboard (WASD) Control[^14]

```javascript
const heldKeys = new Set();
document.addEventListener("keydown", e => {
  if (["KeyW","KeyA","KeyS","KeyD","ArrowUp","ArrowDown","ArrowLeft","ArrowRight"].includes(e.code)) {
    e.preventDefault();
    heldKeys.add(e.code);
  }
  if (e.code === "KeyL") toggleLaser();     // L = laser on/off
  if (e.code === "Space") sendControl({ type: "stop" });
});
document.addEventListener("keyup", e => heldKeys.delete(e.code));

// requestAnimationFrame loop — only send on change
let lastX = null, lastY = null;
function inputLoop() {
  let x = 0, y = 0;
  if (heldKeys.has("KeyW") || heldKeys.has("ArrowUp"))    y += 1;
  if (heldKeys.has("KeyS") || heldKeys.has("ArrowDown"))  y -= 1;
  if (heldKeys.has("KeyA") || heldKeys.has("ArrowLeft"))  x -= 1;
  if (heldKeys.has("KeyD") || heldKeys.has("ArrowRight")) x += 1;
  if (x !== lastX || y !== lastY) {
    if (x === 0 && y === 0) sendControl({ type: "stop" });
    else sendControl({ type: "drive", x, y });
    lastX = x; lastY = y;
  }
  requestAnimationFrame(inputLoop);
}
requestAnimationFrame(inputLoop);
```

---

### Gamepad API Support[^16]

```javascript
let LX=0, LY=0, RX=0, RY=0;
const DEADZONE = 0.08;

function pollGamepad() {
  const gamepads = navigator.getGamepads ? navigator.getGamepads() : [];
  for (const gp of gamepads) {
    if (!gp) continue;
    // Left stick: drive. Right stick: camera pan/tilt
    const lx = Math.abs(gp.axes[0]) > DEADZONE ? gp.axes[0] : 0;
    const ly = Math.abs(gp.axes[1]) > DEADZONE ? gp.axes[1] : 0;
    const rx = Math.abs(gp.axes[2]) > DEADZONE ? gp.axes[2] : 0;
    const ry = Math.abs(gp.axes[3]) > DEADZONE ? gp.axes[3] : 0;

    if (lx !== LX || ly !== LY) {
      LX = lx; LY = ly;
      sendControl({ type: "drive", x: lx, y: -ly }); // Y axis inverted on gamepads
    }
    if (rx !== RX || ry !== RY) {
      RX = rx; RY = ry;
      sendControl({ type: "servo_delta", pan: rx * 5, tilt: ry * 5 });
    }
    // Button index: 0=A/Cross, 4=LB, 5=RB — map laser to a button
    if (gp.buttons[4]?.pressed) toggleLaser();
  }
  requestAnimationFrame(pollGamepad);
}
window.addEventListener("gamepadconnected", () => requestAnimationFrame(pollGamepad));
```

---

## 6. WebRTC Data Channel Command Protocol

### Channel Configuration[^12][^13]

```javascript
// Unreliable (real-time): stale drive/servo commands are useless — retransmit adds lag
const driveDC  = pc.createDataChannel("drive",  { ordered: false, maxRetransmits: 0 });
const servoDC  = pc.createDataChannel("servo",  { ordered: false, maxRetransmits: 0 });

// Reliable (event): laser toggle and stop must arrive
const laserDC  = pc.createDataChannel("laser",  { ordered: true });
const stopDC   = pc.createDataChannel("stop",   { ordered: true });
```

| Channel | Mode | Rationale |
|---------|------|-----------|
| Drive (continuous) | Unreliable | Stale commands irrelevant; latest frame wins |
| Camera servo (continuous) | Unreliable | Same |
| Laser toggle (event) | Reliable | Delivery must be guaranteed |
| Emergency stop | Reliable | Must always arrive |

### JSON Message Protocol[^12][^14]

```typescript
// Drive — sent on every joystick/key change
{ type: "drive",  x: number,   y: number }
// x: turn -1..1 (left=negative), y: forward -1..1

// Stop — explicit stop command
{ type: "stop" }

// Camera servo — absolute position (degrees)
{ type: "servo",  pan: number, tilt: number }
// pan: -90..90°, tilt: -90..90°

// Camera servo — incremental delta (right stick)
{ type: "servo_delta", pan: number, tilt: number }

// Laser
{ type: "laser", on: boolean }

// Robot → Pilot telemetry
{ type: "state", battery: number, laser: boolean, speed: number }
```

**Performance note:** JSON at 60Hz for ~50-byte messages = ~3KB/s — negligible. Use binary `ArrayBuffer` only if polling at >200Hz or on very low-bandwidth links.[^12]

### Robot-side command handler (Go)[^5]

```go
pc.OnDataChannel(func(d *webrtc.DataChannel) {
    d.OnMessage(func(msg webrtc.DataChannelMessage) {
        var cmd struct {
            Type string          `json:"type"`
            X    float64         `json:"x"`
            Y    float64         `json:"y"`
            Pan  float64         `json:"pan"`
            Tilt float64         `json:"tilt"`
            On   bool            `json:"on"`
        }
        json.Unmarshal(msg.Data, &cmd)
        switch cmd.Type {
        case "drive":
            // Differential drive: y=fwd/back, x=turn
            left  := cmd.Y - cmd.X
            right := cmd.Y + cmd.X
            leftMotor.SetSpeedScale(ctx, math.Max(-1, math.Min(1, left)))
            rightMotor.SetSpeedScale(ctx, math.Max(-1, math.Min(1, right)))
        case "stop":
            leftMotor.SetSpeedScale(ctx, 0)
            rightMotor.SetSpeedScale(ctx, 0)
        case "servo":
            panServo.SetAngle(ctx, cmd.Pan * math.Pi / 180)
            tiltServo.SetAngle(ctx, cmd.Tilt * math.Pi / 180)
        case "laser":
            laserPin.Write(rpio.State(boolToInt(cmd.On)))
        }
    })
})
```

---

## 7. Motor Control (Drive)

### Option A: Direct GPIO + H-Bridge (Go — panzerbot pattern) ✅ RECOMMENDED[^9]

**Hardware:** L298N or TB6612FNG motor driver. Two instances (left and right motor).

```go
// motor struct: 2 direction pins + 1 PWM pin
type motor struct {
    aPin, bPin *rpio.Pin  // direction control
    pwmPin     *rpio.Pin  // speed via PWM
    cycleLen   uint32     // PWM period
}

func (m *motor) SetSpeedScale(ctx context.Context, scale float64) error {
    if scale > 0.0 {
        m.aPin.High(); m.bPin.Low()      // forward
    } else if scale < 0.0 {
        m.aPin.Low(); m.bPin.High()      // reverse
    } else {
        m.aPin.Low(); m.bPin.Low()       // stop
        m.pwmPin.DutyCycle(0, m.cycleLen)
        return nil
    }
    duty := uint32(math.Abs(scale) * float64(m.cycleLen))
    m.pwmPin.DutyCycle(duty, m.cycleLen)
    return nil
}
```

### Option B: Adafruit Motor HAT / MotorKit (Python)[^17]

**Hardware:** Adafruit DC & Stepper Motor HAT (#2348) — I2C PCA9685 controller at address `0x60`. Supports 4 DC motors.

```python
from adafruit_motorkit import MotorKit
import board

kit = MotorKit(i2c=board.I2C())

def set_drive(x: float, y: float):
    """Differential drive from normalized x (turn) and y (forward) inputs."""
    left  = max(-1.0, min(1.0, y - x))
    right = max(-1.0, min(1.0, y + x))
    kit.motor1.throttle = right   # -1.0 to 1.0
    kit.motor2.throttle = left

# Install: pip install adafruit-circuitpython-motorkit
```

**Advantage of Motor HAT:** Uses I2C, leaving all GPIO pins free for servos and laser.

---

## 8. Camera Pan/Tilt Servo Control

### Method A: Linux sysfs PWM (panzerbot — RECOMMENDED on RPi)[^9]

Requires enabling hardware PWM in `/boot/firmware/config.txt`:
```
dtoverlay=pwm-2chan,pin=18,func=2,pin2=19,func2=2
```

```go
// Standard hobby servo: 50Hz (20ms period), 500µs–2500µs pulse width
const (
    periodNs   = 20_000_000  // 20ms → 50Hz
    pulseMinNs = 500_000     // 500µs → 0°
    pulseMaxNs = 2_500_000   // 2500µs → 180°
)

// GPIO 18 → /sys/class/pwm/pwmchip0, channel=0  (pan)
// GPIO 19 → /sys/class/pwm/pwmchip2, channel=0  (tilt)

func (s *servo) SetAngle(ctx context.Context, angle float64) error {
    // angle in radians [0, π]
    pulseNs := int(float64(pulseMinNs) + (angle/math.Pi)*float64(pulseMaxNs-pulseMinNs))
    return s.writeFile("duty_cycle", strconv.Itoa(pulseNs))
}
```

### Method B: Python gpiozero (simplest)[^17]

```python
from gpiozero import AngularServo

pan_servo  = AngularServo(18, min_angle=-90, max_angle=90)
tilt_servo = AngularServo(19, min_angle=-45, max_angle=45)

def set_camera(pan_deg: float, tilt_deg: float):
    pan_servo.angle  = max(-90, min(90, pan_deg))
    tilt_servo.angle = max(-45, min(45, tilt_deg))
```

### Method C: Adafruit ServoKit (via PCA9685 — Python)[^17]

If using the Adafruit Motor HAT, its PCA9685 chip has spare channels for servos:
```python
from adafruit_servokit import ServoKit
kit = ServoKit(channels=16)
kit.servo[0].angle = 90   # pan on channel 0
kit.servo[1].angle = 45   # tilt on channel 1
```

**Note:** Mount the laser pointer on the same pan/tilt bracket as the camera — they point in the same direction, no separate servo needed for the laser.

---

## 9. Laser Pointer Control

The laser is a single digital GPIO output pin — simplest possible hardware interface.[^9]

```python
# Python with gpiozero (simplest):
from gpiozero import LED
laser = LED(17)   # GPIO pin 17
laser.on()
laser.off()
```

```go
// Go with go-rpio:
laserPin := rpio.Pin(17)
laserPin.Output()
laserPin.High()  // on
laserPin.Low()   // off
```

**Mount the laser co-axially with the camera** so the pan/tilt servo controls both simultaneously — the pilot sees the laser dot in the video feed aligned with camera center.

---

## 10. Robot Screen & Speaker Output

### Video: Chromium Kiosk Mode (Simplest)[^10]

Run a local HTML page on the robot's HDMI display that shows the pilot's incoming WebRTC video:

```bash
chromium-browser --kiosk --noerrdialogs --disable-infobars \
  http://localhost:8080/robot-display
```

The robot-display page:
```html
<video id="remote" autoplay playsinline style="width:100%;height:100%"></video>
<script>
  // Standard browser WebRTC — receive pilot's video/audio track
  pc.ontrack = (e) => {
    if (e.track.kind === "video")
      document.getElementById("remote").srcObject = e.streams[0];
  };
</script>
```

This runs as a **second WebRTC peer connection** on the robot, separate from the Go/Python robot process, or the robot process can render video via a local WebSocket bridge.

**Alternative — GStreamer pipeline** (headless, lower overhead for video-only display):
```bash
gst-launch-1.0 webrtcbin name=wb ! autovideosink
```

### Audio: Pilot Voice Through Robot Speaker[^10]

**Go + panzerbot pattern** — Opus RTP packets decoded via `gopxl/beep` → ALSA:
```go
pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
    if remoteTrack.Kind() == webrtc.RTPCodecTypeAudio {
        go handleRemoteAudio(remoteTrack)   // decode Opus → ALSA via gopxl/beep
    }
})
```

**Python alternative:** `aiortc` MediaRecorder or custom Opus decoder → PyAudio → ALSA default output device.

---

## 11. TURN/STUN Infrastructure

### Why TURN Is Mandatory for Cross-Internet Connections

WebRTC's ICE protocol tries candidates in priority order:[^18]

```
Direct P2P (host candidates)       ← works on LAN only
       ↓ fails (different networks)
STUN → srflx candidate             ← works ~80-85% of the time
       ↓ fails (symmetric NAT)
TURN/UDP relay                     ← always works if server is reachable
       ↓ fails (UDP blocked)
TURN/TCP relay (port 3478)
       ↓ fails (port 3478 blocked)
TURN/TLS relay (port 443)          ← looks like HTTPS, works everywhere
```

**~15–20% of all real-world internet connections require TURN relay.**[^19] For a robot connecting to pilots in business/corporate environments, the failure rate without TURN is potentially 30–40%. A robot that fails to connect 1-in-4 times is operationally unusable.

**When `iceConnectionState` stalls at `"checking"` indefinitely → symmetric NAT on one or both ends → TURN required.**[^18]

---

### Coturn Deployment

**Self-hosted Coturn on a VPS is the recommended production setup** — fixed cost, full control, no bandwidth metering surprise bills.[^20]

**`turnserver.conf` for telepresence robot:**[^20]
```ini
# Network
listening-port=3478
tls-listening-port=5349      # use 443 for max firewall bypass

# CRITICAL if VPS is behind cloud provider NAT:
external-ip=YOUR_PUBLIC_IP/YOUR_PRIVATE_IP

# Relay port range
min-port=49152
max-port=65535

# WebRTC REQUIRES these two:
lt-cred-mech
fingerprint

# Time-limited credentials (TURN REST API — more secure than static passwords)
use-auth-secret
static-auth-secret=REPLACE_WITH_64_CHAR_RANDOM_SECRET

realm=turn.yourrobot.example.com

# TLS (Let's Encrypt)
cert=/etc/coturn/certs/fullchain.pem
pkey=/etc/coturn/certs/privkey.pem
no-tlsv1
no-tlsv1_1

# Security hardening
no-multicast-peers
no-loopback-peers
no-cli
stale-nonce=600
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
denied-peer-ip=192.168.0.0-192.168.255.255

# Rate limits
user-quota=10
total-quota=100
max-bps=5000000        # 5 Mbps/session (covers 720p + overhead)

log-file=stdout
verbose
```

**Docker Compose:**[^20]
```yaml
services:
  coturn:
    image: coturn/coturn:4.11.0
    network_mode: host     # REQUIRED — Docker performs badly with large UDP port ranges
    restart: unless-stopped
    volumes:
      - ./turnserver.conf:/etc/coturn/turnserver.conf
      - /etc/letsencrypt/live/turn.yourrobot.example.com:/etc/coturn/certs:ro
    environment:
      - DETECT_EXTERNAL_IP=yes
      - DETECT_RELAY_IP=yes
```

**VPS firewall rules:**
```bash
ufw allow 3478/udp && ufw allow 3478/tcp    # TURN/STUN
ufw allow 5349/udp && ufw allow 5349/tcp    # TURNS/DTLS
ufw allow 443/tcp                           # TURNS on HTTPS port
ufw allow 49152:65535/udp                   # relay port range
```

---

### Time-Limited TURN Credentials (REST API)[^20]

Never give permanent TURN credentials to clients. Generate expiring tokens:

```python
import hmac, hashlib, base64, time

def generate_turn_credentials(username: str, shared_secret: str, ttl: int = 3600):
    expiry = int(time.time()) + ttl
    temp_username = f"{expiry}:{username}"
    hashed = hmac.new(
        shared_secret.encode("utf-8"),
        temp_username.encode("utf-8"),
        hashlib.sha1
    )
    return {
        "username":   temp_username,
        "credential": base64.b64encode(hashed.digest()).decode("utf-8"),
        "ttl":        ttl
    }
```

---

### ICE Server Configuration (Robot + Pilot)[^19][^20]

```javascript
const iceConfig = {
  iceServers: [
    // Free public STUN (handles ~80-85% of connections)
    { urls: "stun:stun.l.google.com:19302" },
    { urls: "stun:stun1.l.google.com:19302" },

    // TURN UDP — preferred relay (lowest latency)
    {
      urls: "turn:turn.yourrobot.example.com:3478?transport=udp",
      username: creds.username,
      credential: creds.credential
    },
    // TURN TCP — when UDP is blocked
    {
      urls: "turn:turn.yourrobot.example.com:3478?transport=tcp",
      username: creds.username,
      credential: creds.credential
    },
    // TURNS/TLS on 443 — last resort for deep packet inspection firewalls
    {
      urls: "turns:turn.yourrobot.example.com:443?transport=tcp",
      username: creds.username,
      credential: creds.credential
    }
  ],
  iceTransportPolicy: "all",   // try P2P first, relay as fallback
  bundlePolicy: "max-bundle",
  rtcpMuxPolicy: "require"
};
```

> **Robot on mobile/LTE (symmetric NAT guaranteed):** Set `iceTransportPolicy: "relay"` to skip the multi-second STUN timeout and always use TURN immediately.

---

### TURN Bandwidth & Cost Planning

**720p/30fps H.264 bandwidth:**[^19]

| Stream | Bitrate | Notes |
|--------|---------|-------|
| Robot camera → Pilot | 1.5–2.5 Mbps | H.264 adaptive |
| Pilot camera → Robot | 0.5–0.8 Mbps | Lower quality |
| Audio (bidirectional) | 64–128 Kbps | Opus |
| **Total at robot** | **~2.5–3.5 Mbps** | Per session |

**TURN relay counts both ingress + egress:** ~2.5 GB/hour per active session when using relay.

| TURN Option | Monthly Cost (600 GB) | Latency | Setup |
|-------------|----------------------|---------|-------|
| **Self-hosted Coturn (Hetzner CX21, 20TB included)** | ~€5 flat | Good | Medium |
| **Cloudflare Calls TURN** ($0.05/GB) | ~$30 | Excellent (anycast) | Low |
| Metered.ca Business | $199 + overages | Excellent | Very low |
| Twilio NTS US | ~$240 | Good | Very low |

**Recommendation:** Self-hosted Coturn on Hetzner (~€5/mo with 20TB bandwidth) for a dedicated robot deployment, OR Cloudflare Calls TURN for easy global coverage.

---

## 12. Key Repositories

| Repository | Language | Purpose | Relevance |
|-----------|----------|---------|-----------|
| [`dhonanhibatullah/panzerbot`](https://github.com/dhonanhibatullah/panzerbot) | Go | **Best reference**: RPi 4B + pion/webrtc + GPIO motors + sysfs PWM servos + MMAL H.264 + ALSA audio | ⭐⭐⭐ Closest existing implementation |
| [`pion/webrtc`](https://github.com/pion/webrtc) | Go | Pure Go WebRTC — powers LiveKit; robot auto-accept, OnTrack, OnDataChannel | ⭐⭐⭐ |
| [`pion/mediadevices`](https://github.com/pion/mediadevices) | Go | Camera/mic capture + MMAL H.264 encoding for RPi | ⭐⭐⭐ |
| [`aiortc/aiortc`](https://github.com/aiortc/aiortc) | Python | Python asyncio WebRTC — data channels, webcam, audio; Python robot stack | ⭐⭐⭐ |
| [`livekit/livekit`](https://github.com/livekit/livekit) | Go | Full SFU + signaling server; robot auto-accept via Agents SDK | ⭐⭐⭐ |
| [`livekit/agents`](https://github.com/livekit/agents) | Python | Programmatic LiveKit participant (robot SDK) | ⭐⭐ |
| [`coturn/coturn`](https://github.com/coturn/coturn) | C | STUN/TURN server — mandatory for cross-internet NAT traversal | ⭐⭐⭐ |
| [`rachingenieria/robot-telepresencia-ram-v1`](https://github.com/rachingenieria/robot-telepresencia-ram-v1) | JS/Python | Complete pilot UI: vanilla JS, dual video, touch joystick, data channel drive/servo | ⭐⭐⭐ Closest pilot UI reference |
| [`mihir-chauhan/TeleDrive-WebRTC`](https://github.com/mihir-chauhan/TeleDrive-WebRTC) | JS | Gamepad API integration with WebRTC data channel | ⭐⭐ |
| [`yoannmoinet/nipplejs`](https://github.com/yoannmoinet/nipplejs) | JS | Virtual joystick library for touch/mouse (5K+ stars) | ⭐⭐ |
| [`adafruit/Adafruit_CircuitPython_MotorKit`](https://github.com/adafruit/Adafruit_CircuitPython_MotorKit) | Python | Adafruit Motor HAT Python driver | ⭐⭐ Python path only |

---

## 13. Confidence Assessment

| Finding | Confidence | Basis |
|---------|-----------|-------|
| RPi 4B + Go + pion/webrtc is best for production | **High** | Benchmarked <500ms on RPi 3; panzerbot working reference |
| MMAL H.264 encoding is zero-config on RPi | **High** | Verified in pion/mediadevices README |
| TURN is mandatory for cross-internet | **High** | Twilio/bloggeek stats; ICE failure modes documented |
| ~15–20% of connections need TURN | **Medium** | Twilio says 85% STUN-only success; enterprise may be 30–40% |
| Coturn on Hetzner €5/mo covers 720p robot | **High** | 20TB bandwidth; 720p = ~600GB/month calculated |
| LiveKit SFU adds ~15–30ms latency over P2P | **Medium** | Estimated from SFU architecture; not benchmarked for robot |
| Custom pointer-events joystick better than nipplejs for desktop | **Medium** | Inferred from code analysis; no benchmark found |
| Python aiortc latency "significantly higher" than Go pion | **Medium** | Software vs hardware encoding; specific RPi 4 numbers not benchmarked for aiortc |
| Cloudflare Calls TURN $0.05/GB | **High** | Confirmed from Cloudflare developer docs |

---

## Footnotes

[^1]: [`livekit/livekit`](https://github.com/livekit/livekit) README.md — SFU architecture, latency characteristics
[^2]: [`livekit/agents`](https://github.com/livekit/agents) README.md:60-105 — `@server.rtc_session()` auto-accept robot pattern
[^3]: `dhonanhibatullah/panzerbot:backend/internal/adapters/in/http/handler/rtc_signalling.go:1-50` — custom WebSocket signaling pattern
[^4]: [`pion/webrtc`](https://github.com/pion/webrtc) README.md:1-120 — `OnTrack`, `OnDataChannel`, auto-accept pattern
[^5]: `dhonanhibatullah/panzerbot:backend/internal/adapters/in/http/handler/rtc_signalling.go:48-100` — robot-initiates-offer pattern
[^6]: [`peers/peerjs-server`](https://github.com/peers/peerjs-server) README.md:80-120 — auto-accept call pattern, limitations
[^7]: [`pion/mediadevices`](https://github.com/pion/mediadevices) README.md:157-169 — MMAL zero-config, <500ms latency benchmark
[^8]: `dhonanhibatullah/panzerbot` — working reference implementation, RPi 4B
[^9]: `dhonanhibatullah/panzerbot:backend/go.mod:5-11` — full Go dependency list
[^10]: `dhonanhibatullah/panzerbot:backend/internal/adapters/out/peripheral/rtc_media/impl.go:47-183` — camera capture, audio output patterns
[^11]: [`aiortc/aiortc`](https://github.com/aiortc/aiortc) pyproject.toml:15-27 — PyAV dependency (software encoding only)
[^12]: `rachingenieria/robot-telepresencia-ram-v1:telepresence/web/app.js:79-238` — data channel config, message protocol
[^13]: [`aiortc/aiortc`](https://github.com/aiortc/aiortc) examples/server/client.js:34-84 — RTCPeerConnection setup, ICE gathering, track events
[^14]: `rachingenieria/robot-telepresencia-ram-v1:telepresence/web/app.js:144-363` — custom pointer-events joystick, WASD keyboard
[^15]: [`yoannmoinet/nipplejs`](https://github.com/yoannmoinet/nipplejs) README.md:203-224 — `move` event `vector` data
[^16]: `mihir-chauhan/TeleDrive-WebRTC:client/webrtc.js:82-185` — full Gamepad API with deadzone and delta detection
[^17]: [`adafruit/Adafruit_CircuitPython_MotorKit`](https://github.com/adafruit/Adafruit_CircuitPython_MotorKit) adafruit_motorkit.py — Motor HAT throttle control
[^18]: `zJUNAIDz/vibe-learning-dump:WebRTC/03-ice-stun-turn.md` — ICE candidate types, symmetric NAT failure symptoms; [webrtcforthecurious.com/docs/03-connecting](https://webrtcforthecurious.com/docs/03-connecting/)
[^19]: [twilio.com/en-us/stun-turn](https://www.twilio.com/en-us/stun-turn) — "~85% STUN-only success"; `nirajkvinit/StructWeave-Algorithms` bandwidth estimates; `bloggeek.me/webrtc-turn`
[^20]: [`coturn/coturn`](https://github.com/coturn/coturn) README.turnserver, docker/coturn/README.md — `turnserver.conf` options, Docker deployment; `SqLkk/etdmnet:deploy/coturn/turnserver.conf` production config example; [webrtc.org/getting-started/turn-server](https://webrtc.org/getting-started/turn-server)
