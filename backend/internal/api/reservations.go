package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

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
	Confirm(ctx context.Context, reservationID string) (*domain.Reservation, error)
	Release(ctx context.Context, reservationID string) (*domain.Reservation, error)
	// ListByUser returns the PENDING + CONFIRMED reservations for a user (FR-012).
	// Lazy expiration is applied: expired-but-unswept rows are excluded.
	ListByUser(ctx context.Context, userID string) ([]domain.Reservation, error)
}

// ReservationHandler handles HTTP requests for the /reservations resource.
type ReservationHandler struct {
	store        ReservationStorer
	ttl          time.Duration // default TTL for new reservations; 0 means "use store default"
	publisher    domain.Publisher
	resetTTLOnAdd bool // RESET_TTL_ON_ADD config (FR-017)
}

// NewReservationHandler creates a ReservationHandler with the given store, TTL, and publisher.
// publisher may be nil — in that case no events are broadcast (safe for tests that don't need it).
func NewReservationHandler(s ReservationStorer, ttl time.Duration, publisher domain.Publisher) *ReservationHandler {
	return &ReservationHandler{store: s, ttl: ttl, publisher: publisher}
}

// WithResetTTLOnAdd configures the RESET_TTL_ON_ADD behaviour for the handler.
// Call this after NewReservationHandler when the config flag is known.
func (h *ReservationHandler) WithResetTTLOnAdd(v bool) *ReservationHandler {
	h.resetTTLOnAdd = v
	return h
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
//   - Idempotency-Key header must be present and non-empty (FR-009).
//   - itemId must be present and non-empty.
//   - quantity must be an integer >= 1 (non-integer body → 400 at JSON decode).
//
// On success: 201 with the Reservation JSON.
// On replay (same key + same payload): 201 with the original Reservation JSON.
// On idempotency key conflict (same key + different payload): 409.
// On missing Idempotency-Key header: 400.
// On insufficient stock / conflict: 409.
// On validation error: 400.
func (h *ReservationHandler) Reserve(w http.ResponseWriter, r *http.Request) {
	// FR-009: Idempotency-Key is required.
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required",
			"Idempotency-Key header is required")
		return
	}

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

	// Call the store with the idempotency key.
	params := ReserveParams{
		ItemID:         req.ItemID,
		UserID:         userID,
		Quantity:       *req.Quantity,
		TTL:            h.ttl,
		IdempotencyKey: idemKey,
		ResetTTLOnAdd:  h.resetTTLOnAdd,
	}

	reservation, err := h.store.Reserve(r.Context(), params)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrIdempotencyKeyRequired):
			writeError(w, http.StatusBadRequest, "idempotency_key_required",
				"Idempotency-Key header is required")
		case errors.Is(err, domain.ErrIdempotencyKeyConflict):
			writeError(w, http.StatusConflict, "idempotency_key_conflict",
				"Idempotency-Key was already used with a different request payload")
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

	// Publish a broadcast event so WebSocket clients update their view.
	// The event is fire-and-forget; it does not affect the HTTP response.
	// We do not know the post-mutation available count here without an extra
	// query, so we signal the event type + itemId; clients reconcile via
	// snapshot (GET /items) on reconnect or use the reserved delta.
	if h.publisher != nil {
		h.publisher.Publish(domain.StockEvent{
			Type:   domain.EventTypeReserved,
			ItemID: reservation.ItemID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(reservation) //nolint:errcheck
}

// ConfirmReservation handles POST /reservations/{id}/confirm.
//
// Idempotency is BY RESERVATION STATE (FR-016, data-model.md):
//   - Pending → confirmed: 200 with the confirmed reservation.
//   - Already confirmed: 200 no-op (same reservation returned).
//   - Released or expired: 409 not_pending.
//   - Not found: 404 not_found.
//
// No Idempotency-Key header is required or accepted on this endpoint.
// No stock change occurs on confirm.
func (h *ReservationHandler) ConfirmReservation(w http.ResponseWriter, r *http.Request) {
	reservationID := chi.URLParam(r, "id")
	if reservationID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "reservation id is required")
		return
	}

	reservation, err := h.store.Confirm(r.Context(), reservationID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found",
				"reservation not found")
		case errors.Is(err, domain.ErrNotPending):
			writeError(w, http.StatusConflict, "not_pending",
				"reservation is no longer pending and cannot be confirmed")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error",
				"an unexpected error occurred")
		}
		return
	}

	// Publish a confirmed event so WebSocket clients can update reservation status.
	if h.publisher != nil {
		h.publisher.Publish(domain.StockEvent{
			Type:   domain.EventTypeConfirmed,
			ItemID: reservation.ItemID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(reservation) //nolint:errcheck
}

// ReleaseReservation handles DELETE /reservations/{id}.
//
// Design (FR-007, FR-008, research §3):
//   - Calls store.Release which uses a conditional WHERE status='pending'
//     transition that returns stock exactly once.
//   - Releasing a terminal reservation (released/expired/confirmed/absent) is a
//     safe no-op → returns 200 (idempotent per FR-008).
//   - On success, publishes a StockEvent of type EventTypeReleased to the hub.
//
// Returns 200 with the latest reservation state (or an empty body on no-op).
// This endpoint carries no Idempotency-Key — release is idempotent by design
// (the conditional transition is the idempotency mechanism).
func (h *ReservationHandler) ReleaseReservation(w http.ResponseWriter, r *http.Request) {
	reservationID := chi.URLParam(r, "id")
	if reservationID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "reservation id is required")
		return
	}

	reservation, err := h.store.Release(r.Context(), reservationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// A non-existent id cannot double-decrement stock; treat as 200 no-op
			// per FR-008. The caller's UI timer may have already cleaned up the row.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"not_found"}`)) //nolint:errcheck
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error",
			"an unexpected error occurred")
		return
	}

	// Publish a release event so WebSocket clients update their inventory view.
	if h.publisher != nil {
		h.publisher.Publish(domain.StockEvent{
			Type:   domain.EventTypeReleased,
			ItemID: reservation.ItemID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(reservation) //nolint:errcheck
}

// ListReservations handles GET /reservations.
//
// Returns the calling user's active reservations (PENDING + CONFIRMED).
// The user is identified by the X-User-Id header (enforced by middleware).
// An optional `userId` query parameter overrides the scope (per OpenAPI contract)
// but defaults to the header value.
//
// Lazy expiration is applied in the store layer so expired-but-unswept rows
// are transparently excluded from the response (research §5, FR-012).
//
// Returns 200 with a (possibly empty) JSON array — never null.
func (h *ReservationHandler) ListReservations(w http.ResponseWriter, r *http.Request) {
	// Default scope: the authenticated user from the required header.
	userID := r.Header.Get("X-User-Id")

	// Optional query-param override (openapi.yaml: ?userId=<uuid>).
	if qp := r.URL.Query().Get("userId"); qp != "" {
		userID = qp
	}

	reservations, err := h.store.ListByUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error",
			"an unexpected error occurred")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(reservations) //nolint:errcheck
}
