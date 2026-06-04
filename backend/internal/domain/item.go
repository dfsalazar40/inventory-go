package domain

import "time"

// Item represents a product available for reservation.
type Item struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TotalStock int       `json:"totalStock"`
	Reserved   int       `json:"reserved"`
	Available  int       `json:"available"` // derived: TotalStock - Reserved
	CreatedAt  time.Time `json:"createdAt"`
}
