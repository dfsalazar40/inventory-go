package api

// T029 [US2] — GET /items handler.
//
// Returns all items with live derived available = total_stock - reserved (never negative).
// This endpoint is also the REST snapshot clients fetch on WebSocket connect/reconnect
// to reconcile to backend truth (research §6, FR-011, SC-005).

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// ItemStorer is the dependency interface for item reads.
// Satisfied by *store.ItemStore; also allows test stubs.
type ItemStorer interface {
	ListItems(ctx context.Context) ([]domain.Item, error)
}

// ItemHandler handles HTTP requests for the /items resource.
type ItemHandler struct {
	store ItemStorer
}

// NewItemHandler creates an ItemHandler with the given store.
func NewItemHandler(s ItemStorer) *ItemHandler {
	return &ItemHandler{store: s}
}

// List handles GET /items.
// Returns a JSON array of Item objects (camelCase per the OpenAPI contract).
// Available is always ≥ 0 (enforced in the SQL via GREATEST).
func (h *ItemHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListItems(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error",
			"failed to retrieve items")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(items) //nolint:errcheck
}
