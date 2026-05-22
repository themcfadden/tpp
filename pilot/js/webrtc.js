// webrtc.js — RTCPeerConnection setup, signaling, and media management.

import { channels }        from './protocol.js';
import { setStatus, handleRobotStatus } from './ui.js';

const SIGNAL_URL = `ws://${location.host}/ws`;

const iceConfig = {
  iceServers: [
    { urls: 'stun:stun.l.google.com:19302' },
    // TODO: add TURN server credentials here when deploying cross-internet
  ],
  bundlePolicy:   'max-bundle',
  rtcpMuxPolicy:  'require',
};

async function start() {
  setStatus('connecting');

  // ── WebSocket signaling connection ────────────────────────────────────────
  const ws = new WebSocket(SIGNAL_URL);
  await new Promise((resolve, reject) => {
    ws.onopen  = resolve;
    ws.onerror = reject;
  });

  // ── RTCPeerConnection ─────────────────────────────────────────────────────
  const pc = new RTCPeerConnection(iceConfig);

  // ── Data channels (created before offer so they appear in the SDP) ────────
  channels.drive  = pc.createDataChannel('drive',  { ordered: false, maxRetransmits: 0 });
  channels.servo  = pc.createDataChannel('servo',  { ordered: false, maxRetransmits: 0 });
  channels.laser  = pc.createDataChannel('laser',  { ordered: true  });
  channels.status = pc.createDataChannel('status', { ordered: true  });

  channels.drive.onopen  = () => setStatus('connected');
  channels.drive.onclose = () => setStatus('disconnected');
  channels.status.onmessage = e => handleRobotStatus(e.data);

  // ── Receive robot video + audio ───────────────────────────────────────────
  const robotVideo = document.getElementById('robotVideo');
  const robotAudio = document.getElementById('robotAudio');
  let   videoTrackCount = 0;

  pc.ontrack = (event) => {
    if (event.track.kind === 'video') {
      // First video track is the main camera; additional tracks could be added later.
      if (videoTrackCount === 0) {
        robotVideo.srcObject = new MediaStream([event.track]);
      }
      videoTrackCount++;
    } else if (event.track.kind === 'audio') {
      robotAudio.srcObject = new MediaStream([event.track]);
      robotAudio.muted = false; // must NOT be muted — this is the robot's microphone
      robotAudio.play().catch(e => console.warn('audio autoplay blocked:', e));
    }
  };

  // Declare that we want to receive robot video + audio.
  pc.addTransceiver('video', { direction: 'recvonly' });
  pc.addTransceiver('audio', { direction: 'recvonly' });

  // ── Pilot's own camera + mic → robot screen ───────────────────────────────
  let localStream;
  try {
    localStream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: 'user', width: { ideal: 640 }, height: { ideal: 480 } },
      audio: true,
    });
    document.getElementById('localVideo').srcObject = localStream;
    localStream.getTracks().forEach(track => pc.addTrack(track, localStream));
  } catch (err) {
    console.warn('getUserMedia failed (control-only mode):', err);
  }

  // ── ICE candidates: trickle from browser → robot ─────────────────────────
  pc.onicecandidate = (e) => {
    if (!e.candidate) return;
    ws.send(JSON.stringify({
      type:          'ice-candidate',
      candidate:     e.candidate.candidate,
      sdpMid:        e.candidate.sdpMid,
      sdpMLineIndex: e.candidate.sdpMLineIndex,
    }));
  };

  pc.onconnectionstatechange = () => {
    const state = pc.connectionState;
    console.log('WebRTC connection state:', state);
    if (state === 'connected')    setStatus('connected');
    if (state === 'disconnected' || state === 'failed') setStatus(state);
  };

  // ── Create offer and send to robot ───────────────────────────────────────
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  // Wait for all ICE candidates to be gathered (avoids multiple round-trips on LAN).
  await waitForIceGathering(pc);

  ws.send(JSON.stringify({ type: 'offer', sdp: pc.localDescription.sdp }));

  // ── Signaling loop: process messages from robot ───────────────────────────
  ws.onmessage = async (event) => {
    const msg = JSON.parse(event.data);
    switch (msg.type) {
      case 'answer':
        await pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        break;
      case 'ice-candidate':
        if (msg.candidate) {
          await pc.addIceCandidate({
            candidate:     msg.candidate,
            sdpMid:        msg.sdpMid,
            sdpMLineIndex: msg.sdpMLineIndex,
          });
        }
        break;
    }
  };

  ws.onclose = () => {
    setStatus('disconnected');
    pc.close();
    // Auto-reconnect after 3 seconds.
    setTimeout(start, 3000);
  };
}

function waitForIceGathering(pc) {
  return new Promise(resolve => {
    if (pc.iceGatheringState === 'complete') return resolve();
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        resolve();
      }
    };
    pc.addEventListener('icegatheringstatechange', check);
    // Safety timeout: don't wait more than 2 seconds on LAN.
    setTimeout(resolve, 2000);
  });
}

// Auto-start when the page loads.
start().catch(err => {
  console.error('WebRTC start failed:', err);
  setStatus('failed');
});
