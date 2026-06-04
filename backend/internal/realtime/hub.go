// Package realtime implements the gorilla/websocket hub that broadcasts
// stock and reservation events to all connected clients.
//
// Design (research §6, FR-011):
//   - Snapshot-on-connect: the client fetches GET /items immediately after
//     upgrading, so it always starts from backend truth.
//   - Live deltas: every committed mutation (reserve, confirm, release, expire)
//     publishes a StockEvent to the hub via Hub.Publish; the hub broadcasts
//     it to every registered client connection.
//   - Reconnect-with-backoff on the frontend: if the socket drops the client
//     reconnects, fetches a fresh snapshot, and then resumes delta processing.
//
// The hub is goroutine-safe: all client map mutations happen on a single
// goroutine (the Run loop) driven by internal channels.
package realtime

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

var upgrader = websocket.Upgrader{
	// Allow all origins for the local dev/challenge environment.
	CheckOrigin: func(r *http.Request) bool { return true },

	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// client represents a single WebSocket connection managed by the hub.
type client struct {
	send chan []byte
}

// Hub maintains the set of active WebSocket clients and broadcasts
// StockEvents to all of them.
//
// Publish is safe to call from any goroutine (it sends to a buffered channel).
// Register/Unregister are driven internally by the ServeWS handler.
type Hub struct {
	// broadcast receives events from API handlers (via Publish).
	broadcast chan domain.StockEvent

	// register / unregister are driven by ServeWS goroutines.
	register   chan *client
	unregister chan *client

	// mu protects the clients map; only the Run goroutine touches it, but
	// we keep mu for the unlikely case a future refactor accesses it directly.
	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewHub creates a Hub ready to Run.
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan domain.StockEvent, 256),
		register:   make(chan *client, 64),
		unregister: make(chan *client, 64),
		clients:    make(map[*client]struct{}),
	}
}

// Publish sends a StockEvent to the hub's broadcast channel.
// Safe to call from any goroutine. Does not block unless the channel is full
// (buffer = 256; in practice at the challenge scale this is never full).
func (h *Hub) Publish(e domain.StockEvent) {
	h.broadcast <- e
}

// Run starts the hub event loop. Call it in a dedicated goroutine.
// It processes register, unregister, and broadcast events sequentially,
// so the clients map never needs external locking from within the loop.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case evt := <-h.broadcast:
			payload, err := json.Marshal(evt)
			if err != nil {
				slog.Error("hub: marshal event", "error", err)
				continue
			}
			h.mu.Lock()
			for c := range h.clients {
				select {
				case c.send <- payload:
				default:
					// Slow client — drop it to prevent head-of-line blocking.
					delete(h.clients, c)
					close(c.send)
				}
			}
			h.mu.Unlock()
		}
	}
}

// ServeWS upgrades an HTTP connection to a WebSocket and registers the
// resulting client with the hub. Each client gets two goroutines:
//   - writePump: drains the client's send channel and writes to the socket.
//   - readPump: reads (and discards) incoming messages; detects disconnect.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("hub: websocket upgrade failed", "error", err)
		return
	}

	c := &client{send: make(chan []byte, 256)}
	h.register <- c

	go c.writePump(conn, h)
	c.readPump(conn, h) // blocks until disconnect
}

// readPump reads from the WebSocket connection. Its only job is to detect
// disconnection and unregister the client. Messages from the client are
// discarded (the hub is server→client only in this design).
func (c *client) readPump(conn *websocket.Conn, h *Hub) {
	defer func() {
		h.unregister <- c
		conn.Close()
	}()

	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
		return nil
	})

	for {
		// ReadMessage blocks until a message arrives or the deadline fires.
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// writePump drains the client's send channel and writes messages to the
// WebSocket. It also sends periodic pings so the read deadline doesn't fire
// on idle connections.
func (c *client) writePump(conn *websocket.Conn, h *Hub) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if !ok {
				// Hub closed the channel.
				conn.WriteMessage(websocket.CloseMessage, []byte{}) //nolint:errcheck
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
