package api

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // allow all origins for local dev
}

func (h *Handlers) handleWS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.WSHub == nil {
			http.Error(w, `{"error":"websocket hub not initialized"}`, 501)
			return
		}

		chainID := r.URL.Query().Get("chain_id")
		if chainID == "" {
			http.Error(w, `{"error":"chain_id query parameter required"}`, 400)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[WS] upgrade error: %v", err)
			return
		}

		h.WSHub.Join(chainID, conn)
		defer h.WSHub.Leave(chainID, conn)

		// Keep connection alive, read messages (ignore content, just detect close)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}
}
