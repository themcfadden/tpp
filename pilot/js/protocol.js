// protocol.js — Data channel message constructors and the send helper.
// All other modules import from here so the protocol stays in one place.

export const channels = {}; // populated by webrtc.js: { drive, servo, laser, status }

/** Send a drive command. x=turn (-1..1), y=forward (-1..1). */
export function sendDrive(x, y) {
  sendOn(channels.drive, { type: 'drive', x, y });
}

/** Send an explicit stop. */
export function sendStop() {
  sendOn(channels.drive, { type: 'stop' });
}

/** Send an absolute camera servo position (degrees). */
export function sendServo(pan, tilt) {
  sendOn(channels.servo, { type: 'servo', pan, tilt });
}

/** Send a camera servo delta (right-stick incremental). */
export function sendServoDelta(pan, tilt) {
  sendOn(channels.servo, { type: 'servo_delta', pan, tilt });
}

/** Send a laser toggle command (reliable channel). */
export function sendLaser(on) {
  sendOn(channels.laser, { type: 'laser', on });
}

/** Send an absolute laser gimbal position (degrees). */
export function sendLaserAim(pan, tilt) {
  sendOn(channels.laser, { type: 'laser_aim', pan, tilt });
}

function sendOn(dc, payload) {
  if (!dc || dc.readyState !== 'open') {
    console.warn('[protocol] channel not open, dropping:', payload);
    return;
  }
  // console.log('[protocol] →', JSON.stringify(payload));
  dc.send(JSON.stringify(payload));
}
