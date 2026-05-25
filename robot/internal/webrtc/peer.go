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
	"sync"

	"github.com/gorilla/websocket"
	"github.com/mattmc/tppv4/robot/config"
	"github.com/mattmc/tppv4/robot/internal/control"
	"github.com/mattmc/tppv4/robot/internal/media"
	"github.com/pion/webrtc/v4"
)

// sigMsg is the generic signaling envelope.
type sigMsg struct {
	Type          string `json:"type"`
	SDP           string `json:"sdp,omitempty"`
	Candidate     string `json:"candidate,omitempty"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex int    `json:"sdpMLineIndex,omitempty"`
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

	// Relay tracks: pilot's incoming video/audio forwarded to the display page.
	videoRelay *webrtc.TrackLocalStaticRTP
	audioRelay *webrtc.TrackLocalStaticRTP
}

// New creates a Manager.
func New(cfg config.Config, d *control.Dispatcher, mc *media.Controller) *Manager {
	return &Manager{cfg: cfg, dispatcher: d, mediaCtrl: mc}
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
	log.Printf("display: browser connected from %s", r.RemoteAddr)

	if err := m.runDisplaySession(conn); err != nil {
		log.Printf("display: session ended: %v", err)
	}
}

func (m *Manager) runSession(conn *websocket.Conn) error {
	iceConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConn, err := webrtc.NewPeerConnection(iceConfig)
	if err != nil {
		return fmt.Errorf("new peer connection: %w", err)
	}
	defer func() {
		_ = peerConn.Close()
		// Clear relay tracks so the display doesn't get a stale offer after
		// the pilot disconnects (forwardRTP goroutines will have stopped).
		m.mu.Lock()
		m.videoRelay = nil
		m.audioRelay = nil
		m.mu.Unlock()
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
		log.Printf("webrtc: received %s track from pilot", track.Kind())

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

	// Add robot camera + mic tracks.
	if err := m.mediaCtrl.AddTracksTo(peerConn); err != nil {
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
	send := func(msg sigMsg) error {
		data, _ := json.Marshal(msg)
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	// Check whether relay tracks are available (requires a pilot to be connected).
	m.mu.RLock()
	videoRelay := m.videoRelay
	audioRelay := m.audioRelay
	m.mu.RUnlock()

	log.Printf("display: relay state — video=%v audio=%v", videoRelay != nil, audioRelay != nil)

	if videoRelay == nil {
		log.Printf("display: no pilot connected yet, telling display to retry")
		_ = send(sigMsg{Type: "no-pilot"})
		return nil
	}

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
	defer func() {
		_ = peerConn.Close()
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
		if err := send(sigMsg{
			Type:          "ice-candidate",
			Candidate:     ci.Candidate,
			SDPMid:        mid,
			SDPMLineIndex: idx,
		}); err != nil {
			log.Printf("display: send ICE candidate: %v", err)
		}
	})

	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("display: ICE connection state → %s", state)
	})

	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("display: connection state → %s", state)
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
	if err := send(sigMsg{Type: "offer", SDP: offer.SDP}); err != nil {
		return fmt.Errorf("display: send offer: %w", err)
	}

	// Receive answer and any ICE candidates from the display browser.
	// ICE is trickled: candidates may arrive before or after the answer.
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
