package api

// POST /reset — restore the demo to its initial seeded state.
//
// Clears all reservations and idempotency keys and resets the catalog to the
// seeded baseline, then broadcasts a reset event per item so every connected
// client reconciles to the initial state. Returns the fresh item list.

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// Reseeder is the dependency interface for the reset endpoint.
// Satisfied by *store.ReservationStore; also allows test stubs.
type Reseeder interface {
	ResetToSeed(ctx context.Context) ([]domain.Item, error)
}

// ResetHandler handles POST /reset.
type ResetHandler struct {
	store     Reseeder
	publisher domain.Publisher
}

// NewResetHandler creates a ResetHandler. publisher may be nil (no broadcast).
func NewResetHandler(s Reseeder, publisher domain.Publisher) *ResetHandler {
	return &ResetHandler{store: s, publisher: publisher}
}

// Reset handles POST /reset.
func (h *ResetHandler) Reset(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ResetToSeed(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error",
			"failed to reset inventory")
		return
	}

	// Broadcast the restored state so other connected clients reconcile.
	if h.publisher != nil {
		for _, it := range items {
			h.publisher.Publish(domain.StockEvent{
				Type:      domain.EventTypeReset,
				ItemID:    it.ID,
				Reserved:  it.Reserved,
				Available: it.Available,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(items) //nolint:errcheck
}
