// ui.js — Connection status display and robot state updates.

const connStatus = document.getElementById('connStatus');
const displayStatus = document.getElementById('displayStatus');
let displayPollStarted = false;

export function setStatus(state) {
  const labels = {
    connecting:   ['connecting',   '⬤ Connecting…'],
    connected:    ['connected',    '⬤ Connected'],
    disconnected: ['disconnected', '⬤ Disconnected'],
    failed:       ['disconnected', '⬤ Failed'],
  };
  const [cls, text] = labels[state] ?? labels.disconnected;
  connStatus.className = `status ${cls}`;
  connStatus.textContent = text;
}

// Called when the robot sends a status message back over the status data channel.
export function handleRobotStatus(msg) {
  try {
    const data = JSON.parse(msg);
    if (data.type === 'status') {
      const laserEl = document.getElementById('laserStatus');
      laserEl.textContent = data.laser ? '🔴 Laser ON' : '🔴 Laser OFF';
      laserEl.className   = data.laser ? 'laser-on'   : 'laser-off';
    }
  } catch { /* ignore malformed messages */ }
}

export function setDisplayHealth(state) {
  if (!displayStatus) return;

  if (!state) {
    displayStatus.className = 'display-status unknown';
    displayStatus.textContent = 'Display: unknown';
    return;
  }

  const connected = state.browserConnected && state.sessionActive && state.lastPeerState === 'connected';
  const waiting = state.browserConnected && state.lastPeerState !== 'connected';

  if (connected) {
    displayStatus.className = 'display-status ok';
    displayStatus.textContent = 'Display: live';
    return;
  }
  if (waiting) {
    displayStatus.className = 'display-status waiting';
    displayStatus.textContent = 'Display: connecting';
    return;
  }

  displayStatus.className = 'display-status down';
  displayStatus.textContent = 'Display: down';
}

export function startDisplayHealthPolling() {
  if (displayPollStarted) return;
  displayPollStarted = true;

  const poll = async () => {
    try {
      const res = await fetch('/display-health', { cache: 'no-store' });
      if (!res.ok) throw new Error('status ' + res.status);
      const data = await res.json();
      setDisplayHealth(data);
    } catch {
      setDisplayHealth(null);
    }
  };

  poll();
  setInterval(poll, 2000);
}
