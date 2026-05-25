package ws

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// LogEntry represents a single progress log message.
type LogEntry struct {
	Step   string `json:"step"`
	Server string `json:"server,omitempty"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Hub manages WebSocket connections grouped by chain ID.
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*websocket.Conn]bool // chainID -> connections
}

// NewHub creates a new WebSocket hub.
func NewHub() *Hub {
	return &Hub{rooms: make(map[string]map[*websocket.Conn]bool)}
}

// Join adds a connection to a chain's room.
func (h *Hub) Join(chainID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[chainID] == nil {
		h.rooms[chainID] = make(map[*websocket.Conn]bool)
	}
	h.rooms[chainID][conn] = true
	log.Printf("[WS] client joined chain=%s (total=%d)", chainID, len(h.rooms[chainID]))
}

// Leave removes a connection from a chain's room.
func (h *Hub) Leave(chainID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[chainID] != nil {
		delete(h.rooms[chainID], conn)
		if len(h.rooms[chainID]) == 0 {
			delete(h.rooms, chainID)
		}
	}
	log.Printf("[WS] client left chain=%s", chainID)
}

// Broadcast sends a log entry to all connections in a chain's room.
func (h *Hub) Broadcast(chainID string, entry LogEntry) {
	h.mu.RLock()
	conns := h.rooms[chainID]
	h.mu.RUnlock()

	data, _ := json.Marshal(entry)
	for conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[WS] write error: %v", err)
			conn.Close()
			h.Leave(chainID, conn)
		}
	}
}

// ProgressWriter implements io.Writer to bridge log output to WebSocket.
type ProgressWriter struct {
	hub     *Hub
	chainID string
}

// NewProgressWriter creates a writer that broadcasts each line to the WS room.
func NewProgressWriter(hub *Hub, chainID string) *ProgressWriter {
	return &ProgressWriter{hub: hub, chainID: chainID}
}

func (w *ProgressWriter) Write(p []byte) (int, error) {
	msg := string(p)
	if msg != "" && msg != "\n" {
		w.hub.Broadcast(w.chainID, LogEntry{Detail: msg})
	}
	return len(p), nil
}
