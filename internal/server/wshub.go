package server

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"minimax-monitor/internal/storage"
)

// wsWriteTimeout is the per-write deadline applied to every websocket frame.
const wsWriteTimeout = 2 * time.Second

// WSHub broadcasts Snapshot frames to all connected websocket clients.
type WSHub struct {
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
	snap  []storage.Snapshot
}

// NewWSHub constructs an empty hub.
func NewWSHub() *WSHub {
	return &WSHub{
		conns: make(map[*websocket.Conn]struct{}),
	}
}

// Broadcast stores snap and pushes it to every connected client.
func (h *WSHub) Broadcast(snap []storage.Snapshot) {
	h.mu.Lock()
	h.snap = snap
	clients := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	msg := map[string]any{
		"type": "snapshot",
		"data": map[string]any{
			"fetched_at": time.Now().UnixMilli(),
			"models":     snap,
		},
	}
	for _, c := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
		err := wsjson.Write(ctx, c, msg)
		cancel()
		if err != nil {
			log.Printf("ws: write error: %v", err)
			h.Unregister(c)
		}
	}
}

// Register adds c to the hub and, if a snapshot is already buffered,
// immediately replays it to the new client.
func (h *WSHub) Register(c *websocket.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	snap := h.snap
	h.mu.Unlock()

	if snap != nil {
		ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
		msg := map[string]any{
			"type": "snapshot",
			"data": map[string]any{
				"fetched_at": time.Now().UnixMilli(),
				"models":     snap,
			},
		}
		_ = wsjson.Write(ctx, c, msg)
		cancel()
	}
}

// Unregister removes c from the hub.
func (h *WSHub) Unregister(c *websocket.Conn) {
	h.mu.Lock()
	if _, ok := h.conns[c]; ok {
		delete(h.conns, c)
	}
	h.mu.Unlock()
}

// ServeWS accepts a websocket upgrade, registers the conn, and blocks
// on reads until the client disconnects.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("ws: accept error: %v", err)
		return
	}
	h.Register(c)
	defer func() {
		h.Unregister(c)
		_ = c.Close(websocket.StatusNormalClosure, "")
	}()
	// Block until the client disconnects (or the request context is done).
	for {
		if _, _, err := c.Read(r.Context()); err != nil {
			return
		}
	}
}