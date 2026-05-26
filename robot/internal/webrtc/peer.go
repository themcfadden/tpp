package webrtcpeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattmc/tppv4/robot/config"
	"github.com/mattmc/tppv4/robot/internal/control"
	"github.com/mattmc/tppv4/robot/internal/media"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// sigMsg is the generic signaling envelope.
type sigMsg struct {
	Type          string `json:"type"`
	SDP           string `json:"sdp,omitempty"`
	Candidate     string `json:"candidate,omitempty"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex int    `json:"sdpMLineIndex,omitempty"`
	Message       string `json:"message,omitempty"` // used by display "log" messages
}

// DisplayHealth captures the robot display page's latest known session state.
type DisplayHealth struct {
	BrowserConnected      bool      `json:"browserConnected"`
	SessionActive         bool      `json:"sessionActive"`
	RelayVideo            bool      `json:"relayVideo"`
	RelayAudio            bool      `json:"relayAudio"`
	LastBrowserConnect    time.Time `json:"lastBrowserConnect,omitempty"`
	LastBrowserDisconnect time.Time `json:"lastBrowserDisconnect,omitempty"`
	LastOfferSent         time.Time `json:"lastOfferSent,omitempty"`
	LastPeerState         string    `json:"lastPeerState,omitempty"`
	LastICEState          string    `json:"lastIceState,omitempty"`
	LastVideoEvent        string    `json:"lastVideoEvent,omitempty"`
	LastVideoPlaying      time.Time `json:"lastVideoPlaying,omitempty"`
	LastLogMessage        string    `json:"lastLogMessage,omitempty"`
	LastError             string    `json:"lastError,omitempty"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Manager creates and manages WebRTC peer connections for incoming pilots.
type Manager struct {
	cfg        config.Config
	dispatcher *control.Dispatcher
	mediaCtrl  *media.Controller
	mu         sync.RWMutex
	displayMu   sync.RWMutex

	// Relay tracks: pilot's incoming video/audio forwarded to the display page.
	videoRelay *webrtc.TrackLocalStaticRTP
	audioRelay *webrtc.TrackLocalStaticRTP
	videoSSRC  uint32 // SSRC of the pilot's video track; used to send PLI

	// pilotPC is the pilot's peer connection, kept so the display can request
	// keyframes (PLI) from the pilot's encoder via RTCP.
	pilotPC   *webrtc.PeerConnection
	pilotMu   sync.Mutex
	pilotGone chan struct{} // closed when the current pilot disconnects
	display   DisplayHealth
}

// New creates a Manager.
func New(cfg config.Config, d *control.Dispatcher, mc *media.Controller) *Manager {
	return &Manager{
		cfg:        cfg,
		dispatcher: d,
		mediaCtrl:  mc,
		pilotGone:  make(chan struct{}),
		display: DisplayHealth{
			LastPeerState:  "idle",
			LastICEState:   "new",
			LastVideoEvent: "idle",
		},
	}
}

// DisplayHealthSnapshot returns the latest server-side view of the display session.
func (m *Manager) DisplayHealthSnapshot() DisplayHealth {
	m.displayMu.RLock()
	snapshot := m.display
	m.displayMu.RUnlock()

	m.mu.RLock()
	snapshot.RelayVideo = m.videoRelay != nil
	snapshot.RelayAudio = m.audioRelay != nil
	m.mu.RUnlock()

	return snapshot
}

func (m *Manager) updateDisplayHealth(update func(*DisplayHealth)) {
	m.displayMu.Lock()
	defer m.displayMu.Unlock()
	update(&m.display)
}

// ServeHTTP handles the WebSocket upgrade and runs the full signaling + peer lifecycle.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("webrtc: ws upgrade: %v", err)
		return
	}
	defer conn.Close()
	log.Printf("webrtc: pilot connected from %s", r.RemoteAddr)

	if err := m.runSession(conn); err != nil {
		log.Printf("webrtc: session ended: %v", err)
	}
}

// ServeDisplay handles the WebSocket upgrade for the robot's local display browser.
// The display page (display.html) connects here to receive the pilot's video/audio.
func (m *Manager) ServeDisplay(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("display: ws upgrade: %v", err)
		return
	}
	defer conn.Close()
	m.updateDisplayHealth(func(h *DisplayHealth) {
		h.BrowserConnected = true
		h.LastBrowserConnect = time.Now()
		h.LastError = ""
	})
	defer m.updateDisplayHealth(func(h *DisplayHealth) {
		h.BrowserConnected = false
		h.SessionActive = false
		h.LastBrowserDisconnect = time.Now()
	})
	log.Printf("display: browser connected from %s", r.RemoteAddr)

	if err := m.runDisplaySession(conn); err != nil {
		m.updateDisplayHealth(func(h *DisplayHealth) {
			h.LastError = err.Error()
		})
		log.Printf("display: session ended: %v", err)
	}
}

func (m *Manager) runSession(conn *websocket.Conn) error {
	// Broadcast to any waiting display sessions that a new pilot is present.
	pilotGone := make(chan struct{})
	m.pilotMu.Lock()
	m.pilotGone = pilotGone
	m.pilotMu.Unlock()

	iceConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConn, err := webrtc.NewPeerConnection(iceConfig)
	if err != nil {
		return fmt.Errorf("new peer connection: %w", err)
	}

	// Store pilot PC so the display can request keyframes via PLI.
	m.pilotMu.Lock()
	m.pilotPC = peerConn
	m.pilotMu.Unlock()

	defer func() {
		_ = peerConn.Close()
		// Only tear down shared pilot state if this session is still the current one.
		m.pilotMu.Lock()
		isCurrentPilot := m.pilotPC == peerConn
		if isCurrentPilot {
			m.pilotPC = nil
		}
		shouldClosePilotGone := m.pilotGone == pilotGone
		m.pilotMu.Unlock()

		if isCurrentPilot {
			// Clear relay tracks so the display doesn't get a stale offer after
			// the current pilot disconnects (forwardRTP goroutines will have stopped).
			m.mu.Lock()
			m.videoRelay = nil
			m.audioRelay = nil
			m.videoSSRC = 0
			m.mu.Unlock()
		}

		// Signal active display sessions to close and reconnect only for the
		// pilot session that still owns the current pilotGone channel.
		if shouldClosePilotGone {
			close(pilotGone)
		}
		log.Printf("webrtc: peer connection closed")
	}()

	send := func(msg sigMsg) {
		data, _ := json.Marshal(msg)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("webrtc: send signaling: %v", err)
		}
	}

	// Trickle ICE: forward local candidates to pilot as they are discovered.
	peerConn.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		ci := c.ToJSON()
		mid := ""
		if ci.SDPMid != nil {
			mid = *ci.SDPMid
		}
		idx := 0
		if ci.SDPMLineIndex != nil {
			idx = int(*ci.SDPMLineIndex)
		}
		send(sigMsg{
			Type:          "ice-candidate",
			Candidate:     ci.Candidate,
			SDPMid:        mid,
			SDPMLineIndex: idx,
		})
	})

	// Receive pilot's video/audio. Create relay tracks so they can be forwarded
	// to the robot's local display browser via ServeDisplay.
	peerConn.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("webrtc: received %s track from pilot (codec=%s ssrc=%d)",
			track.Kind(), track.Codec().MimeType, track.SSRC())

		relay, err := webrtc.NewTrackLocalStaticRTP(
			track.Codec().RTPCodecCapability,
			track.Kind().String(),
			"pilot-relay",
		)
		if err != nil {
			log.Printf("webrtc: create %s relay: %v", track.Kind(), err)
			return
		}

		m.mu.Lock()
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			m.videoRelay = relay
			m.videoSSRC = uint32(track.SSRC())
		} else {
			m.audioRelay = relay
		}
		m.mu.Unlock()

		// Forward RTP packets from pilot → relay track (read by display peer connection).
		go forwardRTP(track, relay)
	})

	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("webrtc: connection state → %s", state)
		if state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateFailed {
			_ = m.dispatcher.EmergencyStop()
		}
	})

	// Wire data channels from pilot.
	peerConn.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("webrtc: data channel opened: %s", dc.Label())
		switch dc.Label() {
		case "drive":
			m.dispatcher.HandleDriveChannel(dc)
		case "servo":
			m.dispatcher.HandleServoChannel(dc)
		case "laser":
			m.dispatcher.HandleLaserChannel(dc)
		case "status":
			m.dispatcher.SetStatusChannel(dc)
		case "logs":
			dc.OnOpen(func() {
				log.Printf("webrtc: logs channel open — mirroring log output to pilot")
				log.SetOutput(io.MultiWriter(os.Stderr, &dcWriter{dc: dc}))
			})
			dc.OnClose(func() {
				log.SetOutput(os.Stderr)
				log.Printf("webrtc: logs channel closed — log output restored to stderr")
			})
		}
	})

	// Add robot camera + mic tracks. If a local media track ends, force a
	// pilot reconnect so the UI does not stay on a frozen last frame.
	var mediaReconnectOnce sync.Once
	if err := m.mediaCtrl.AddTracksTo(peerConn, func() {
		mediaReconnectOnce.Do(func() {
			log.Printf("webrtc: local media track ended; forcing pilot reconnect")
			_ = conn.Close()
			_ = peerConn.Close()
		})
	}); err != nil {
		return fmt.Errorf("add media tracks: %w", err)
	}

	// Signaling loop: wait for pilot's offer, respond with answer.
	// ICE candidates may arrive before the offer (trickle ICE race), so we
	// queue them and drain the queue once the remote description is set.
	var (
		pendingCandidates []webrtc.ICECandidateInit
		remoteDescSet     bool
	)

	addCandidate := func(init webrtc.ICECandidateInit) {
		if err := peerConn.AddICECandidate(init); err != nil {
			log.Printf("webrtc: add ICE candidate: %v", err)
		}
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read signaling: %w", err)
		}
		var msg sigMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("webrtc: bad signaling message: %v", err)
			continue
		}
		switch msg.Type {
		case "offer":
			if err := peerConn.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  msg.SDP,
			}); err != nil {
				return fmt.Errorf("set remote description: %w", err)
			}
			remoteDescSet = true
			for _, c := range pendingCandidates {
				addCandidate(c)
			}
			pendingCandidates = nil

			answer, err := peerConn.CreateAnswer(nil)
			if err != nil {
				return fmt.Errorf("create answer: %w", err)
			}
			if err := peerConn.SetLocalDescription(answer); err != nil {
				return fmt.Errorf("set local description: %w", err)
			}
			send(sigMsg{Type: "answer", SDP: answer.SDP})

		case "ice-candidate":
			if msg.Candidate == "" {
				continue
			}
			sdpMid := msg.SDPMid
			sdpMLineIndex := uint16(msg.SDPMLineIndex)
			init := webrtc.ICECandidateInit{
				Candidate:     msg.Candidate,
				SDPMid:        &sdpMid,
				SDPMLineIndex: &sdpMLineIndex,
			}
			if remoteDescSet {
				addCandidate(init)
			} else {
				pendingCandidates = append(pendingCandidates, init)
			}

		case "close":
			return nil
		}
	}
}

// runDisplaySession handles a WebRTC connection from the robot's local display browser.
// The SERVER acts as offerer: it adds relay tracks and sends the offer to the display.
// If no pilot is connected yet, it sends "no-pilot" and closes so the display retries.
func (m *Manager) runDisplaySession(conn *websocket.Conn) error {
	// gorilla/websocket requires serialized writes. ICE candidate callbacks fire
	// from pion goroutines concurrently with the main signaling loop and the
	// pilotGone watcher, so all writes (including conn.Close) share one mutex.
	var writeMu sync.Mutex
	send := func(msg sigMsg) error {
		data, _ := json.Marshal(msg)
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, data)
	}
	closeConn := func() {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.Close()
	}

	// Check whether relay tracks are available (requires a pilot to be connected).
	m.mu.RLock()
	videoRelay := m.videoRelay
	audioRelay := m.audioRelay
	m.mu.RUnlock()

	log.Printf("display: relay state — video=%v audio=%v", videoRelay != nil, audioRelay != nil)
	m.updateDisplayHealth(func(h *DisplayHealth) {
		h.SessionActive = videoRelay != nil
		if videoRelay == nil {
			h.LastPeerState = "waiting-for-pilot"
		}
	})

	if videoRelay == nil {
		log.Printf("display: no pilot connected yet, telling display to retry")
		_ = send(sigMsg{Type: "no-pilot"})
		return nil
	}

	// Snapshot the pilot-gone channel now that we know a pilot is connected.
	// We'll watch it below so the display reconnects when the pilot leaves.
	m.pilotMu.Lock()
	pilotGone := m.pilotGone
	m.pilotMu.Unlock()

	iceConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	// Register default codecs on a fresh MediaEngine so the offer contains
	// the full codec list (VP8, VP9, H264, Opus, etc.) that Chromium expects.
	// Without this, webrtc.NewAPI produces an empty offer and the browser rejects it.
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return fmt.Errorf("display: register codecs: %w", err)
	}

	// Include loopback ICE candidates so the display (Chromium kiosk on the
	// same Pi) can always connect via 127.0.0.1, regardless of firewall rules
	// on the LAN interface.
	se := webrtc.SettingEngine{}
	se.SetIPFilter(func(ip net.IP) bool { return true })

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se))

	peerConn, err := api.NewPeerConnection(iceConfig)
	if err != nil {
		return fmt.Errorf("display: new peer connection: %w", err)
	}
	m.updateDisplayHealth(func(h *DisplayHealth) {
		h.SessionActive = true
		h.LastPeerState = "new"
		h.LastICEState = "new"
	})
	defer func() {
		_ = peerConn.Close()
		m.updateDisplayHealth(func(h *DisplayHealth) {
			h.SessionActive = false
			h.LastPeerState = "closed"
		})
		log.Printf("display: peer connection closed")
	}()

	log.Printf("display: adding relay tracks to peer connection")
	if _, err := peerConn.AddTrack(videoRelay); err != nil {
		return fmt.Errorf("display: add video track: %w", err)
	}
	if audioRelay != nil {
		if _, err := peerConn.AddTrack(audioRelay); err != nil {
			return fmt.Errorf("display: add audio track: %w", err)
		}
	} else {
		log.Printf("display: no audio relay track available")
	}

	// videoPlaying is closed when the display browser sends a "video-playing"
	// message, signaling that Chromium is rendering frames and PLIs can stop.
	videoPlaying := make(chan struct{})

	// sessionDone is closed when runDisplaySession returns. Used to stop the
	// PLI goroutine and the pilotGone watcher so they don't outlive the session.
	sessionDone := make(chan struct{})
	defer close(sessionDone)

	// ICE candidates from pion fire concurrently (from pion goroutines) and can
	// arrive at the browser BEFORE the offer if we send them immediately.
	// Buffer them here and flush only after the offer has been sent, guaranteeing
	// the browser always receives the offer first.
	var (
		iceBufMu   sync.Mutex
		iceBuf     []sigMsg
		offerSent  bool
	)
	sendAfterOffer := func(msg sigMsg) {
		iceBufMu.Lock()
		if !offerSent {
			iceBuf = append(iceBuf, msg)
			iceBufMu.Unlock()
			return
		}
		iceBufMu.Unlock()
		if err := send(msg); err != nil {
			log.Printf("display: send ICE candidate: %v", err)
		}
	}

	peerConn.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			log.Printf("display: ICE gathering complete")
			return
		}
		log.Printf("display: ICE candidate → %s", c.String())
		ci := c.ToJSON()
		mid := ""
		if ci.SDPMid != nil {
			mid = *ci.SDPMid
		}
		idx := 0
		if ci.SDPMLineIndex != nil {
			idx = int(*ci.SDPMLineIndex)
		}
		sendAfterOffer(sigMsg{
			Type:          "ice-candidate",
			Candidate:     ci.Candidate,
			SDPMid:        mid,
			SDPMLineIndex: idx,
		})
	})

	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		m.updateDisplayHealth(func(h *DisplayHealth) {
			h.LastICEState = state.String()
		})
		log.Printf("display: ICE connection state → %s", state)
	})

	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		m.updateDisplayHealth(func(h *DisplayHealth) {
			h.LastPeerState = state.String()
		})
		log.Printf("display: connection state → %s", state)
		if state == webrtc.PeerConnectionStateConnected {
			// The relay forwards VP8 mid-stream; Chromium can't render until it
			// receives a keyframe. Send PLIs repeatedly every 1.5 s until the
			// display browser confirms the video element is playing. We stop
			// after 20 attempts (~30 s) — periodic keyframes will arrive naturally.
			// sessionDone is closed when runDisplaySession returns, ensuring this
			// goroutine never outlives its session (fixes goroutine leak on reconnect).
			go func() {
				ticker := time.NewTicker(1500 * time.Millisecond)
				defer ticker.Stop()
				for attempt := 1; attempt <= 20; attempt++ {
					m.sendPLI()
					log.Printf("display: PLI #%d sent", attempt)
					select {
					case <-videoPlaying:
						log.Printf("display: video confirmed playing after %d PLI(s)", attempt)
						return
					case <-sessionDone:
						log.Printf("display: session ended — PLI goroutine stopping at attempt %d", attempt)
						return
					case <-ticker.C:
					}
				}
				log.Printf("display: PLI limit reached, stopping")
			}()
		}
	})

	log.Printf("display: creating offer")
	offer, err := peerConn.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("display: create offer: %w", err)
	}
	if err := peerConn.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("display: set local description: %w", err)
	}

	log.Printf("display: sending offer to browser (SDP length: %d)", len(offer.SDP))
	m.updateDisplayHealth(func(h *DisplayHealth) {
		h.LastOfferSent = time.Now()
	})
	if err := send(sigMsg{Type: "offer", SDP: offer.SDP}); err != nil {
		return fmt.Errorf("display: send offer: %w", err)
	}

	// Offer is now sent — flush any ICE candidates that arrived before we
	// could send the offer, then allow future candidates through immediately.
	iceBufMu.Lock()
	offerSent = true
	toFlush := iceBuf
	iceBuf = nil
	iceBufMu.Unlock()
	for _, msg := range toFlush {
		log.Printf("display: flushing buffered ICE candidate")
		if err := send(msg); err != nil {
			log.Printf("display: send buffered ICE candidate: %v", err)
		}
	}

	// Receive answer and any ICE candidates from the display browser.
	// ICE is trickled: candidates may arrive before or after the answer.

	// When the pilot disconnects, close the WebSocket so the display browser
	// retries and picks up the next pilot's relay tracks.
	go func() {
		select {
		case <-pilotGone:
			log.Printf("display: pilot gone — closing display session for reconnect")
			closeConn()
		case <-sessionDone:
		}
	}()

	var (
		pendingCandidates []webrtc.ICECandidateInit
		remoteDescSet     bool
	)

	addCandidate := func(init webrtc.ICECandidateInit) {
		if err := peerConn.AddICECandidate(init); err != nil {
			log.Printf("display: add ICE candidate: %v", err)
		}
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("display: read signaling: %w", err)
		}
		var msg sigMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("display: bad signaling message: %v", err)
			continue
		}
		log.Printf("display: ← %s", msg.Type)
		switch msg.Type {
		case "answer":
			if err := peerConn.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  msg.SDP,
			}); err != nil {
				return fmt.Errorf("display: set remote description: %w", err)
			}
			remoteDescSet = true
			for _, c := range pendingCandidates {
				addCandidate(c)
			}
			pendingCandidates = nil

		case "ice-candidate":
			if msg.Candidate == "" {
				continue
			}
			sdpMid := msg.SDPMid
			sdpMLineIndex := uint16(msg.SDPMLineIndex)
			init := webrtc.ICECandidateInit{
				Candidate:     msg.Candidate,
				SDPMid:        &sdpMid,
				SDPMLineIndex: &sdpMLineIndex,
			}
			if remoteDescSet {
				addCandidate(init)
			} else {
				pendingCandidates = append(pendingCandidates, init)
			}

		case "log":
			// Log messages sent from the display browser, forwarded here
			// so they appear in the robot log (and thus in the pilot's logs channel).
			m.updateDisplayHealth(func(h *DisplayHealth) {
				h.LastLogMessage = msg.Message
				if idx := strings.Index(msg.Message, "video event: "); idx >= 0 {
					h.LastVideoEvent = msg.Message[idx+len("video event: "):]
				}
			})
			log.Printf("display[browser]: %s", msg.Message)

		case "video-playing":
			// Display browser confirmed the video element fired the "playing" event.
			// Close the channel once (select guards against double-close).
			m.updateDisplayHealth(func(h *DisplayHealth) {
				h.LastVideoEvent = "playing"
				h.LastVideoPlaying = time.Now()
			})
			select {
			case <-videoPlaying:
			default:
				close(videoPlaying)
			}
			log.Printf("display: received video-playing confirmation")

		case "close":
			return nil
		}
	}
}

// dcWriter is an io.Writer that sends each log line as text over a WebRTC data channel.
type dcWriter struct {
	dc *webrtc.DataChannel
}

func (w *dcWriter) Write(p []byte) (int, error) {
	// SendText is non-blocking; ignore errors so a closed channel doesn't break logging.
	_ = w.dc.SendText(string(p))
	return len(p), nil
}

// sendPLI sends a Picture Loss Indication to the pilot, requesting an
// immediate keyframe. Called when the display peer connection is established
// so the display doesn't have to wait for the next periodic keyframe.
func (m *Manager) sendPLI() {
	m.pilotMu.Lock()
	pc := m.pilotPC
	m.pilotMu.Unlock()

	m.mu.RLock()
	ssrc := m.videoSSRC
	m.mu.RUnlock()

	if pc == nil || ssrc == 0 {
		log.Printf("display: PLI skipped (pilotPC=%v ssrc=%d)", pc != nil, ssrc)
		return
	}
	if err := pc.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: ssrc, SenderSSRC: ssrc},
	}); err != nil {
		log.Printf("display: send PLI: %v", err)
		return
	}
	log.Printf("display: sent PLI to pilot — requesting keyframe")
}

// forwardRTP copies RTP packets from a remote track to a local relay track
// until the source closes. Runs in its own goroutine.
func forwardRTP(src *webrtc.TrackRemote, dst *webrtc.TrackLocalStaticRTP) {
	for {
		pkt, _, err := src.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("webrtc: relay %s read: %v", src.Kind(), err)
			}
			return
		}
		if err := dst.WriteRTP(pkt); err != nil {
			// Ignore write errors — display may not be connected yet.
			_ = err
		}
	}
}

// Ensure Manager implements http.Handler for /ws.
var _ http.Handler = (*Manager)(nil)

// Keep context import used.
var _ = context.Background
