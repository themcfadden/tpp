// ui.js — Connection status display and robot state updates.

const connStatus = document.getElementById('connStatus');

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
