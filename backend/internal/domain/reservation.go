package domain

import "time"

// ReservationStatus represents the lifecycle state of a reservation.
type ReservationStatus string

const (
	StatusPending   ReservationStatus = "pending"
	StatusConfirmed ReservationStatus = "confirmed"
	StatusReleased  ReservationStatus = "released"
	StatusExpired   ReservationStatus = "expired"
)

// Reservation is a hold by one user on N units of one item.
type Reservation struct {
	ID          string            `json:"id"`
	ItemID      string            `json:"itemId"`
	UserID      string            `json:"userId"`
	Quantity    int               `json:"quantity"`
	Status      ReservationStatus `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
	ExpiresAt   *time.Time        `json:"expiresAt"`   // nil once confirmed
	ConfirmedAt *time.Time        `json:"confirmedAt"` // nil until confirmed
	ReleasedAt  *time.Time        `json:"releasedAt"`  // nil until released
}

// IsTerminal returns true when the reservation is in a terminal state
// (confirmed, released, or expired) and cannot transition further.
func (r *Reservation) IsTerminal() bool {
	return r.Status == StatusConfirmed ||
		r.Status == StatusReleased ||
		r.Status == StatusExpired
}
