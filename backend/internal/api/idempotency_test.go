package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dfsalazar40/inventory-go/backend/internal/store"
)

// TestIdempotency_MissingHeader tests that a reserve request with no
// Idempotency-Key header is rejected with 400 idempotency_key_required.
//
// FR-009 / T022 [US6]
func TestIdempotency_MissingHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)
	itemID := createAPITestItem(t, pool, "idem-missing-header", 5)

	rs := store.NewReservationStore(pool)
	h := NewReservationHandler(rs, 0)
	router := NewRouter(h)

	body := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID)
	req := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user-idem")
	// Intentionally no Idempotency-Key header.

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "idempotency_key_required" {
		t.Errorf("want error=idempotency_key_required, got %v", resp["error"])
	}

	// No stock must have changed.
	var reserved int
	pool.QueryRow(t.Context(), `SELECT reserved FROM items WHERE id = $1`, itemID).Scan(&reserved) //nolint:errcheck
	if reserved != 0 {
		t.Errorf("want reserved=0 (no state change), got %d", reserved)
	}
}

// TestIdempotency_ConflictDifferentPayload tests that reusing an idempotency key
// with a different payload is rejected with 409 idempotency_key_conflict.
//
// FR-010 / US6 scenario 2 / T022.
func TestIdempotency_ConflictDifferentPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)
	itemID := createAPITestItem(t, pool, "idem-conflict-payload", 5)
	itemID2 := createAPITestItem(t, pool, "idem-conflict-payload-2", 5)

	rs := store.NewReservationStore(pool)
	h := NewReservationHandler(rs, 0)
	router := NewRouter(h)

	idemKey := "test-conflict-key"

	// First request — succeed.
	body1 := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID)
	req1 := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-User-Id", "user-conflict")
	req1.Header.Set("Idempotency-Key", idemKey)

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d (body: %s)", w1.Code, w1.Body.String())
	}

	// Second request — different payload (different itemId), same key → 409.
	body2 := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID2)
	req2 := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-User-Id", "user-conflict")
	req2.Header.Set("Idempotency-Key", idemKey)

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("want 409, got %d (body: %s)", w2.Code, w2.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "idempotency_key_conflict" {
		t.Errorf("want error=idempotency_key_conflict, got %v", resp["error"])
	}
}

// TestIdempotency_SameKeyAndPayloadSequential tests that sending the same
// reserve request sequentially returns the SAME reservation id and does NOT
// decrement stock a second time.
//
// US6 scenario 1 / T022.
func TestIdempotency_SameKeyAndPayloadSequential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)
	itemID := createAPITestItem(t, pool, "idem-sequential-replay", 5)

	rs := store.NewReservationStore(pool)
	h := NewReservationHandler(rs, 0)
	router := NewRouter(h)

	idemKey := "test-sequential-replay-key"
	body := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID)

	// First request.
	req1 := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-User-Id", "user-replay")
	req1.Header.Set("Idempotency-Key", idemKey)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first request: want 201, got %d (body: %s)", w1.Code, w1.Body.String())
	}
	var r1 map[string]interface{}
	json.NewDecoder(w1.Body).Decode(&r1) //nolint:errcheck
	id1 := r1["id"].(string)

	// Second request — identical key + payload.
	req2 := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-User-Id", "user-replay")
	req2.Header.Set("Idempotency-Key", idemKey)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Errorf("replay request: want 201, got %d (body: %s)", w2.Code, w2.Body.String())
	}
	var r2 map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&r2) //nolint:errcheck
	id2 := r2["id"].(string)

	// Same reservation id must be returned.
	if id1 != id2 {
		t.Errorf("want same reservation id on replay, got %q vs %q", id1, id2)
	}

	// Exactly ONE decrement: reserved == 1.
	var reserved int
	pool.QueryRow(t.Context(), `SELECT reserved FROM items WHERE id = $1`, itemID).Scan(&reserved) //nolint:errcheck
	if reserved != 1 {
		t.Errorf("want reserved=1 (single decrement), got %d", reserved)
	}
}
