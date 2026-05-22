// controls.js — Joystick, keyboard, and gamepad input handling.
// All input is routed through protocol.js — never touches the DOM directly
// except to move knobs.

import { sendDrive, sendStop, sendServo, sendServoDelta, sendLaser } from './protocol.js';

// ── State ────────────────────────────────────────────────────────────────────
let laserOn = false;

// ── Joystick (custom pointer-events, zero dependencies) ──────────────────────

function attachPad(padId, knobId, onMove, onRelease) {
  const pad  = document.getElementById(padId);
  const knob = document.getElementById(knobId);
  let active = false;

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
    knob.style.left = `${50 + nx * 35}%`;
    knob.style.top  = `${50 + ny * 35}%`;
    onMove(nx, ny);
  }

  pad.addEventListener('pointerdown', e => {
    active = true;
    pad.setPointerCapture(e.pointerId); // keep tracking if finger slides off
    handleMove(e.clientX, e.clientY);
  });
  pad.addEventListener('pointermove', e => { if (active) handleMove(e.clientX, e.clientY); });

  const release = () => {
    if (!active) return;
    active = false;
    knob.style.left = knob.style.top = '50%';
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
let camPan = 0, camTilt = 0;
attachPad('cameraPad', 'cameraKnob',
  (x, y) => {
    camPan  = Math.round(-x * 90);   // -90..90 deg
    camTilt = Math.round( y * 45);   // -45..45 deg
    sendServo(camPan, camTilt);
  },
  () => {}  // hold last camera position on release
);

// ── Laser button ──────────────────────────────────────────────────────────────

const laserBtn = document.getElementById('laserBtn');
laserBtn.addEventListener('click', () => {
  laserOn = !laserOn;
  sendLaser(laserOn);
  updateLaserUI();
});

function updateLaserUI() {
  laserBtn.classList.toggle('active', laserOn);
  const el = document.getElementById('laserStatus');
  el.textContent = laserOn ? '🔴 Laser ON' : '🔴 Laser OFF';
  el.className   = laserOn ? 'laser-on'   : 'laser-off';
}

// ── Keyboard (WASD / Arrow keys) ─────────────────────────────────────────────

const heldKeys = new Set();
document.addEventListener('keydown', e => {
  const nav = ['KeyW','KeyA','KeyS','KeyD','ArrowUp','ArrowDown','ArrowLeft','ArrowRight'];
  if (nav.includes(e.code)) { e.preventDefault(); heldKeys.add(e.code); }
  if (e.code === 'KeyL')   { laserOn = !laserOn; sendLaser(laserOn); updateLaserUI(); }
  if (e.code === 'Space')  { e.preventDefault(); sendStop(); }
});
document.addEventListener('keyup', e => heldKeys.delete(e.code));

let lastKX = null, lastKY = null;
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
// Left stick (axes 0/1): drive. Right stick (axes 2/3): camera delta.
// Left bumper (btn 4): laser toggle.

const DEADZONE = 0.08;
let gpLX=0, gpLY=0, gpRX=0, gpRY=0;
let gpLB = false;

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
      sendServoDelta(rx * 5, ry * 5); // incremental camera pan/tilt
    }

    // Left bumper (button 4) — laser toggle on press
    const lb = gp.buttons[4]?.pressed ?? false;
    if (lb && !gpLB) { laserOn = !laserOn; sendLaser(laserOn); updateLaserUI(); }
    gpLB = lb;
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
