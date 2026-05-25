package webrtcpeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

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

	peerConn, err := webrtc.NewPeerConnection(iceConfig)
	if err != nil {
		return fmt.Errorf("display: new peer connection: %w", err)
	}
	defer func() {
		_ = peerConn.Close()
		log.Printf("display: peer connection closed")
	}()

	if _, err := peerConn.AddTrack(videoRelay); err != nil {
		return fmt.Errorf("display: add video track: %w", err)
	}
	if audioRelay != nil {
		if _, err := peerConn.AddTrack(audioRelay); err != nil {
			return fmt.Errorf("display: add audio track: %w", err)
		}
	}

	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("display: connection state → %s", state)
	})

	// Gather all ICE candidates before sending the offer (simpler than trickle on LAN).
	gatherDone := make(chan struct{})
	peerConn.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		if state == webrtc.ICEGatheringStateComplete {
			select {
			case <-gatherDone:
			default:
				close(gatherDone)
			}
		}
	})

	offer, err := peerConn.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("display: create offer: %w", err)
	}
	if err := peerConn.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("display: set local description: %w", err)
	}

	// Wait for ICE gathering to finish (max 2s on LAN).
	select {
	case <-gatherDone:
	case <-time.After(2 * time.Second):
	}

	if err := send(sigMsg{Type: "offer", SDP: peerConn.LocalDescription().SDP}); err != nil {
		return fmt.Errorf("display: send offer: %w", err)
	}

	// Receive answer and any ICE candidates from the display browser.
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
