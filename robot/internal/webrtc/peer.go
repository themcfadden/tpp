package webrtcpeer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	mu         sync.Mutex
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

	// Receive pilot's video/audio (displayed on robot screen via Chromium kiosk).
	peerConn.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Printf("webrtc: received %s track from pilot", track.Kind())
		if track.Kind() == webrtc.RTPCodecTypeAudio {
			go m.mediaCtrl.PlayPilotAudio(context.Background(), track)
		}
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
			if err := peerConn.AddICECandidate(webrtc.ICECandidateInit{
				Candidate:     msg.Candidate,
				SDPMid:        &sdpMid,
				SDPMLineIndex: &sdpMLineIndex,
			}); err != nil {
				log.Printf("webrtc: add ICE candidate: %v", err)
			}

		case "close":
			return nil
		}
	}
}
