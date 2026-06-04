package domain

import "time"

// Item represents a product available for reservation.
type Item struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TotalStock int       `json:"totalStock"`
	Reserved   int       `json:"reserved"`
	Available  int       `json:"available"` // derived: TotalStock - Reserved, never negative
	CreatedAt  time.Time `json:"createdAt"`
}

// EventType identifies the kind of stock/reservation mutation that triggered an event.
type EventType string

const (
	EventTypeReserved  EventType = "reserved"
	EventTypeConfirmed EventType = "confirmed"
	EventTypeReleased  EventType = "released"
	EventTypeExpired   EventType = "expired"
)

// StockEvent is broadcast to all WebSocket clients after every committed mutation.
// It carries enough information for clients to update their local view or trigger
// a reconciling snapshot fetch from GET /items.
type StockEvent struct {
	Type      EventType `json:"type"`
	ItemID    string    `json:"itemId"`
	Reserved  int       `json:"reserved"`
	Available int       `json:"available"`
}

// Publisher is the interface the API layer uses to broadcast stock events.
// Implemented by *realtime.Hub in production; by a mock in tests.
// Keeping it here (in domain) avoids an import cycle: api → domain ← realtime.
type Publisher interface {
	Publish(e StockEvent)
}
