package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/dfsalazar40/inventory-go/backend/internal/store"
)

// ReserveParams is re-exported from the store package so the test stub
// in the api package can implement the interface without an import cycle.
// We alias it here to keep the api package clean.
type ReserveParams = store.ReserveParams

// ReservationStorer is the dependency interface expected by ReservationHandler.
// Satisfied by *store.ReservationStore (and by in-test stubs).
type ReservationStorer interface {
	Reserve(ctx context.Context, p ReserveParams) (*domain.Reservation, error)
}

// ReservationHandler handles HTTP requests for the /reservations resource.
type ReservationHandler struct {
	store ReservationStorer
	ttl   time.Duration // default TTL for new reservations; 0 means "use store default"
}

// NewReservationHandler creates a ReservationHandler with the given store and TTL.
func NewReservationHandler(s ReservationStorer, ttl time.Duration) *ReservationHandler {
	return &ReservationHandler{store: s, ttl: ttl}
}

// createReservationRequest is the decoded request body for POST /reservations.
// quantity must be an integer >= 1 per the OpenAPI contract.
type createReservationRequest struct {
	ItemID   string `json:"itemId"`
	Quantity *int   `json:"quantity"` // pointer so we can distinguish 0 from absent
}

// errorResponse is the JSON body for all error responses (per Error schema in openapi.yaml).
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeError writes a JSON error response with the given HTTP status code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: code, Message: message}) //nolint:errcheck
}

// Reserve handles POST /reservations.
//
// Validation:
//   - itemId must be present and non-empty.
//   - quantity must be an integer >= 1 (non-integer body → 400 at JSON decode).
//
// On success: 201 with the Reservation JSON.
// On insufficient stock / conflict: 409.
// On validation error: 400.
//
// Idempotency-Key enforcement is deferred to a later batch (US6). The header
// may be present but is not required here.
func (h *ReservationHandler) Reserve(w http.ResponseWriter, r *http.Request) {
	// Decode body. A non-integer quantity field fails here → 400.
	var req createReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error",
			"request body must be valid JSON with itemId (string) and quantity (integer >= 1)")
		return
	}

	// Validate itemId.
	if req.ItemID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "itemId is required")
		return
	}

	// Validate quantity: must be present and >= 1.
	if req.Quantity == nil || *req.Quantity < 1 {
		writeError(w, http.StatusBadRequest, "validation_error", "quantity must be an integer >= 1")
		return
	}

	// Extract userId from the enforced middleware header.
	userID := r.Header.Get("X-User-Id")

	// Call the store.
	params := ReserveParams{
		ItemID:   req.ItemID,
		UserID:   userID,
		Quantity: *req.Quantity,
		TTL:      h.ttl,
	}

	reservation, err := h.store.Reserve(r.Context(), params)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInsufficientStock):
			writeError(w, http.StatusConflict, "insufficient_stock",
				"not enough stock available for the requested quantity")
		case errors.Is(err, domain.ErrConflict):
			writeError(w, http.StatusConflict, "conflict",
				"reservation could not be completed due to a concurrency conflict")
		case errors.Is(err, domain.ErrValidation):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error",
				"an unexpected error occurred")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(reservation) //nolint:errcheck
}
