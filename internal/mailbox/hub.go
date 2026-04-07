package mailbox

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// connState holds a WebSocket connection and serialises all writes to it.
type connState struct {
	conn *websocket.Conn
	wmu  sync.Mutex // guards all WriteMessage calls
}

func (cs *connState) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cs.wmu.Lock()
	defer cs.wmu.Unlock()
	return cs.conn.WriteMessage(websocket.TextMessage, data)
}

func (cs *connState) ping() error {
	cs.wmu.Lock()
	defer cs.wmu.Unlock()
	return cs.conn.WriteMessage(websocket.PingMessage, nil)
}

// Hub maps mailbox ID → WebSocket connection state.
type Hub struct {
	mu    sync.Mutex
	conns map[string]*connState
}

var hub = &Hub{conns: make(map[string]*connState)}

func GetHub() *Hub { return hub }

// Register associates id with conn, replacing any existing registration.
func (h *Hub) Register(id string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[id] = &connState{conn: conn}
}

// Unregister removes id from the hub.
func (h *Hub) Unregister(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, id)
}

func (h *Hub) getState(id string) *connState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[id]
}

// Push sends a JSON-encoded message to the mailbox owner if connected.
// Safe for concurrent callers (e.g. SMTP goroutine and WebSocket read loop).
func (h *Hub) Push(id string, msg any) {
	cs := h.getState(id)
	if cs == nil {
		return
	}
	if err := cs.writeJSON(msg); err != nil {
		log.Printf("mailbox push write: %v", err)
		h.Unregister(id)
	}
}

// Send writes a JSON message to the registered connection for id.
// Returns nil if id is not registered.
func (h *Hub) Send(id string, msg any) error {
	cs := h.getState(id)
	if cs == nil {
		return nil
	}
	return cs.writeJSON(msg)
}

// Ping sends a WebSocket ping frame to the registered connection for id.
// Returns nil if id is not registered.
func (h *Hub) Ping(id string) error {
	cs := h.getState(id)
	if cs == nil {
		return nil
	}
	return cs.ping()
}
