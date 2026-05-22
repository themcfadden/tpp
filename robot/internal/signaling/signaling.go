package signaling

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// Message is the generic signaling envelope.
type Message struct {
	Type         string `json:"type"`                    // "offer" | "answer" | "ice-candidate" | "close"
	SDP          string `json:"sdp,omitempty"`
	Candidate    string `json:"candidate,omitempty"`
	SDPMid       string `json:"sdpMid,omitempty"`
	SDPMLineIndex int   `json:"sdpMLineIndex,omitempty"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // allow all origins (LAN dev)
}

// Handler manages the WebSocket signaling connection.
// onMessage is called for each message received from the pilot.
// Send returns a function the WebRTC layer can call to push messages to the pilot.
type Handler struct {
	onMessage func(msg Message)
}

// New creates a Handler. Call SetOnMessage before registering it.
func New(onMessage func(msg Message)) *Handler {
	return &Handler{onMessage: onMessage}
}

// ServeHTTP upgrades the connection to WebSocket and handles the signaling loop.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("signaling: upgrade: %v", err)
		return
	}
	defer conn.Close()
	log.Printf("signaling: pilot connected from %s", r.RemoteAddr)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("signaling: read: %v", err)
			}
			log.Printf("signaling: pilot disconnected")
			return
		}
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("signaling: bad message: %v", err)
			continue
		}
		h.onMessage(msg)
	}
}

// Sender wraps a WebSocket connection so the WebRTC layer can send signaling messages.
type Sender struct {
	conn *websocket.Conn
}

// NewSender wraps a WebSocket connection for outgoing signaling messages.
func NewSender(conn *websocket.Conn) *Sender {
	return &Sender{conn: conn}
}

// Send marshals and writes a signaling message to the pilot.
func (s *Sender) Send(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, data)
}
