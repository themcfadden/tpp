// webrtc.js — RTCPeerConnection setup, signaling, and media management.

import { channels }        from './protocol.js';
import { setStatus, handleRobotStatus, startDisplayHealthPolling } from './ui.js';

function websocketUrl(path) {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}${path}`;
}

const SIGNAL_URL = websocketUrl('/ws');

const iceConfig = {
  iceServers: [
    { urls: 'stun:stun.l.google.com:19302' },
    // TODO: add TURN server credentials here when deploying cross-internet
  ],
  bundlePolicy:   'max-bundle',
  rtcpMuxPolicy:  'require',
};

async function start() {
  startDisplayHealthPolling();
  setStatus('connecting');
  console.log('[webrtc] signaling url:', SIGNAL_URL);

  // ── WebSocket signaling connection ────────────────────────────────────────
  const ws = new WebSocket(SIGNAL_URL);
  await new Promise((resolve, reject) => {
    ws.onopen  = resolve;
    ws.onerror = (event) => {
      console.error('[webrtc] signaling socket error:', event);
      reject(event);
    };
  });

  // ── RTCPeerConnection ─────────────────────────────────────────────────────
  const pc = new RTCPeerConnection(iceConfig);

  // ── Data channels (created before offer so they appear in the SDP) ────────
  channels.drive  = pc.createDataChannel('drive',  { ordered: false, maxRetransmits: 0 });
  channels.servo  = pc.createDataChannel('servo',  { ordered: false, maxRetransmits: 0 });
  channels.laser  = pc.createDataChannel('laser',  { ordered: true  });
  channels.status = pc.createDataChannel('status', { ordered: true  });
  channels.logs   = pc.createDataChannel('logs',   { ordered: true  });

  channels.drive.onopen  = () => { console.log('[webrtc] drive channel open'); setStatus('connected'); };
  channels.drive.onclose = () => { console.log('[webrtc] drive channel closed'); setStatus('disconnected'); };
  channels.servo.onopen  = () => console.log('[webrtc] servo channel open');
  channels.laser.onopen  = () => console.log('[webrtc] laser channel open');
  channels.status.onopen = () => console.log('[webrtc] status channel open');
  channels.status.onmessage = e => { console.log('[webrtc] ← status:', e.data); handleRobotStatus(e.data); };
  channels.logs.onopen    = () => console.log('[webrtc] logs channel open — robot log output will appear here');
  channels.logs.onmessage = e => console.log('[robot]', e.data.trimEnd());

  // ── Receive robot video + audio ───────────────────────────────────────────
  const robotVideo = document.getElementById('robotVideo');
  const robotAudio = document.getElementById('robotAudio');
  let   videoTrackCount = 0;

  pc.ontrack = (event) => {
    console.log('[webrtc] ontrack:', event.track.kind, 'id=' + event.track.id, 'readyState=' + event.track.readyState);
    event.track.onended = () => console.warn('[webrtc] robot', event.track.kind, 'track ended id=' + event.track.id);
    if (event.track.kind === 'video') {
      // First video track is the main camera; additional tracks could be added later.
      if (videoTrackCount === 0) {
        robotVideo.srcObject = new MediaStream([event.track]);
        robotVideo.play().catch(e => console.warn('[webrtc] robotVideo autoplay blocked:', e));
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
  // ICE candidates from the robot may arrive before the answer is processed
  // (trickle ICE race), so queue them and drain once remote desc is set.
  let remoteDescSet = false;
  const pendingCandidates = [];

  ws.onmessage = async (event) => {
    const msg = JSON.parse(event.data);
    switch (msg.type) {
      case 'answer':
        await pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        remoteDescSet = true;
        for (const c of pendingCandidates) {
          await pc.addIceCandidate(c).catch(e => console.warn('[webrtc] addIceCandidate (queued):', e));
        }
        pendingCandidates.length = 0;
        break;
      case 'ice-candidate':
        if (msg.candidate) {
          const init = { candidate: msg.candidate, sdpMid: msg.sdpMid, sdpMLineIndex: msg.sdpMLineIndex };
          if (remoteDescSet) {
            await pc.addIceCandidate(init).catch(e => console.warn('[webrtc] addIceCandidate:', e));
          } else {
            pendingCandidates.push(init);
          }
        }
        break;
    }
  };

  ws.onclose = () => {
    console.warn('[webrtc] signaling socket closed');
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
