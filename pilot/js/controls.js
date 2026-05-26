// controls.js — Joystick, keyboard, and gamepad input handling.
// All input is routed through protocol.js — never touches the DOM directly
// except to move knobs.

import { sendDrive, sendStop, sendServo, sendLaser, sendLaserAim } from './protocol.js';

// ── State ────────────────────────────────────────────────────────────────────
let laserOn = false;
let camPan = 0;
let camTilt = 0;
let laserPan = 0;
let laserTilt = 45; // down position
const TILT_DOWN_DEG = 45;

const cameraKnob = document.getElementById('cameraKnob');
const laserKnob = document.getElementById('laserKnob');
const laserPad = document.getElementById('laserPad');

function setCameraKnob(nx, ny) {
  cameraKnob.style.left = `${50 + nx * 35}%`;
  cameraKnob.style.top  = `${50 + ny * 35}%`;
}

function setLaserKnob(nx, ny) {
  laserKnob.style.left = `${50 + nx * 35}%`;
  laserKnob.style.top  = `${50 + ny * 35}%`;
}

function updateCameraPose(pan, tilt) {
  camPan = Math.max(-90, Math.min(90, pan));
  camTilt = Math.max(-45, Math.min(45, tilt));
  sendServo(camPan, camTilt);
}

function pointLaserDown() {
  updateLaserPose(0, TILT_DOWN_DEG);
  setLaserKnob(0, 1);
}

function updateLaserPose(pan, tilt) {
  laserPan = Math.max(-90, Math.min(90, pan));
  laserTilt = Math.max(-45, Math.min(45, tilt));
  sendLaserAim(laserPan, laserTilt);
}

function setLaserState(on) {
  if (laserOn === on) return;
  laserOn = on;
  sendLaser(laserOn);
  updateLaserUI();
}

// ── Joystick (custom pointer-events, zero dependencies) ──────────────────────

function attachPad(padId, knobId, onMove, onRelease, opts = {}) {
  const pad  = document.getElementById(padId);
  const knob = document.getElementById(knobId);
  let active = false;
  const resetX = opts.resetX ?? 0;
  const resetY = opts.resetY ?? 0;
  const resetOnRelease = opts.resetOnRelease ?? true;
  const onPress = opts.onPress ?? (() => {});

  function setKnobPosition(nx, ny) {
    knob.style.left = `${50 + nx * 35}%`;
    knob.style.top  = `${50 + ny * 35}%`;
  }

  function handleMove(clientX, clientY) {
    const rect   = pad.getBoundingClientRect();
    const cx     = rect.left + rect.width  / 2;
    const cy     = rect.top  + rect.height / 2;
    const maxR   = rect.width * 0.35;
    let dx = clientX - cx;
    let dy = clientY - cy;
    const dist = Math.hypot(dx, dy);
    if (dist > maxR) { dx *= maxR / dist; dy *= maxR / dist; }
    const nx = dx / maxR;   // -1..1
    const ny = dy / maxR;   // -1..1
    setKnobPosition(nx, ny);
    onMove(nx, ny);
  }

  pad.addEventListener('pointerdown', e => {
    active = true;
    pad.setPointerCapture(e.pointerId); // keep tracking if finger slides off
    onPress(e);
    handleMove(e.clientX, e.clientY);
  });
  pad.addEventListener('pointermove', e => { if (active) handleMove(e.clientX, e.clientY); });

  const release = () => {
    if (!active) return;
    active = false;
    if (resetOnRelease) {
      setKnobPosition(resetX, resetY);
    }
    onRelease();
  };
  pad.addEventListener('pointerup',     release);
  pad.addEventListener('pointercancel', release);
}

// Drive pad: push joystick → send drive command; release → stop.
attachPad('drivePad', 'driveKnob',
  (x, y) => sendDrive(x, -y),   // invert Y: up = forward
  ()     => sendStop()
);

// Camera pad: push joystick → send servo absolute position; hold last on release.
attachPad('cameraPad', 'cameraKnob',
  (x, y) => {
    updateCameraPose(
      Math.round(-x * 90),
      Math.round(y * 45),
    );
  },
  () => {},  // hold last position on release
  {
    resetOnRelease: false,
  }
);

// Laser pad: move to aim; hold pad press to fire; release to stop and point down.
attachPad('laserPad', 'laserKnob',
  (x, y) => {
    updateLaserPose(
      Math.round(-x * 90),
      Math.round(y * 45),
    );
  },
  () => {
    setLaserState(false);
    pointLaserDown();
  },
  {
    onPress: () => setLaserState(true),
    resetX: 0,
    resetY: 1,
  }
);

function updateLaserUI() {
  laserPad.classList.toggle('active', laserOn);
  const el = document.getElementById('laserStatus');
  el.textContent = laserOn ? '🔴 Laser ON' : '🔴 Laser OFF';
  el.className   = laserOn ? 'laser-on'   : 'laser-off';
}

// ── Keyboard (WASD / Arrow keys) ─────────────────────────────────────────────

const heldKeys = new Set();
document.addEventListener('keydown', e => {
  const nav = ['KeyW','KeyA','KeyS','KeyD','ArrowUp','ArrowDown','ArrowLeft','ArrowRight'];
  if (nav.includes(e.code)) { e.preventDefault(); heldKeys.add(e.code); }
  if (e.code === 'KeyL')   { e.preventDefault(); setLaserState(true); }
  if (e.code === 'Space')  { e.preventDefault(); sendStop(); }
});
document.addEventListener('keyup', e => {
  heldKeys.delete(e.code);
  if (e.code === 'KeyL') {
    setLaserState(false);
    pointLaserDown();
  }
});

let lastKX = 0, lastKY = 0;
function keyboardLoop() {
  let x = 0, y = 0;
  if (heldKeys.has('KeyW') || heldKeys.has('ArrowUp'))    y += 1;
  if (heldKeys.has('KeyS') || heldKeys.has('ArrowDown'))  y -= 1;
  if (heldKeys.has('KeyA') || heldKeys.has('ArrowLeft'))  x -= 1;
  if (heldKeys.has('KeyD') || heldKeys.has('ArrowRight')) x += 1;
  if (x !== lastKX || y !== lastKY) {
    if (x === 0 && y === 0) sendStop();
    else sendDrive(x, y);
    lastKX = x; lastKY = y;
  }
  requestAnimationFrame(keyboardLoop);
}
requestAnimationFrame(keyboardLoop);

// ── Gamepad API ───────────────────────────────────────────────────────────────
// Left stick (axes 0/1): drive. Right stick (axes 2/3): camera pan/tilt.
// Left stick button (btn 10): laser on while held.
// D-pad (buttons 12/13/14/15): laser pan/tilt aiming.

const DEADZONE = 0.08;
let gpLX = 0, gpLY = 0, gpRX = 0, gpRY = 0;
let gpR3 = false;

function pollGamepad() {
  const gamepads = navigator.getGamepads ? navigator.getGamepads() : [];
  for (const gp of gamepads) {
    if (!gp) continue;

    const lx = applyDeadzone(gp.axes[0]);
    const ly = applyDeadzone(gp.axes[1]);
    const rx = applyDeadzone(gp.axes[2]);
    const ry = applyDeadzone(gp.axes[3]);

    if (lx !== gpLX || ly !== gpLY) {
      gpLX = lx; gpLY = ly;
      if (lx === 0 && ly === 0) sendStop();
      else sendDrive(lx, -ly); // invert Y
    }
    if (rx !== gpRX || ry !== gpRY) {
      gpRX = rx; gpRY = ry;
      updateCameraPose(
        Math.round(-rx * 90),
        Math.round(ry * 45),
      );
      setCameraKnob(rx, ry);
    }

    const dpadX = (gp.buttons[15]?.pressed ? 1 : 0) - (gp.buttons[14]?.pressed ? 1 : 0);
    const dpadY = (gp.buttons[13]?.pressed ? 1 : 0) - (gp.buttons[12]?.pressed ? 1 : 0);
    if (dpadX !== 0 || dpadY !== 0) {
      updateLaserPose(
        Math.round(laserPan + (-dpadX * 3)),
        Math.round(laserTilt + (dpadY * 2)),
      );
      setLaserKnob(laserPan / 90, laserTilt / 45);
    }

    const r3 = gp.buttons[10]?.pressed ?? false;
    if (r3 !== gpR3) {
      if (r3) {
        setLaserState(true);
      } else {
        setLaserState(false);
        pointLaserDown();
      }
      gpR3 = r3;
    }
  }
  requestAnimationFrame(pollGamepad);
}

window.addEventListener('gamepadconnected', e => {
  console.log(`Gamepad connected: ${e.gamepad.id}`);
  requestAnimationFrame(pollGamepad);
});

function applyDeadzone(v) {
  return Math.abs(v) > DEADZONE ? v : 0;
}

pointLaserDown();
