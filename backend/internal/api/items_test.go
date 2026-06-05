package api

// T028 [US2] — Test-first for GET /items and broadcast on mutation.
//
// Covers:
//   - GET /items returns each item with derived available = total_stock - reserved (never negative).
//   - A successful reserve publishes a broadcast event to a registered mock subscriber.
//
// FR-001, FR-011, SC-005.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/dfsalazar40/inventory-go/backend/internal/store"
)

// --- helpers ---

// mockPublisher records every published event so tests can assert on it.
type mockPublisher struct {
	events chan domain.StockEvent
}

func newMockPublisher(buf int) *mockPublisher {
	return &mockPublisher{events: make(chan domain.StockEvent, buf)}
}

func (m *mockPublisher) Publish(e domain.StockEvent) {
	m.events <- e
}

// --- T028a: GET /items returns derived available ---

func TestItems_GetAll_DerivedAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)

	// Create two items: one with partial reservation, one fully available.
	itemA := createAPITestItem(t, pool, "items-get-a", 10)
	itemB := createAPITestItem(t, pool, "items-get-b", 5)

	// Manually set reserved on itemA to simulate an existing hold.
	_, err := pool.Exec(t.Context(), `UPDATE items SET reserved = 3 WHERE id = $1`, itemA)
	if err != nil {
		t.Fatalf("set reserved: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(t.Context(), `UPDATE items SET reserved = 0 WHERE id = $1`, itemA) //nolint:errcheck
	})

	itemStore := store.NewItemStore(pool)
	itemHandler := NewItemHandler(itemStore)
	router := NewRouter(nil, itemHandler, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set("X-User-Id", "user-list-items")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var items []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Build a map by item id for assertions.
	byID := make(map[string]map[string]interface{})
	for _, it := range items {
		id, _ := it["id"].(string)
		byID[id] = it
	}

	// itemA: totalStock=10, reserved=3 → available=7
	a, ok := byID[itemA]
	if !ok {
		t.Fatalf("itemA %q not in response", itemA)
	}
	assertItemFields(t, a, 10, 3, 7)

	// itemB: totalStock=5, reserved=0 → available=5
	b, ok := byID[itemB]
	if !ok {
		t.Fatalf("itemB %q not in response", itemB)
	}
	assertItemFields(t, b, 5, 0, 5)
}

// TestItems_GetAll_AvailableNeverNegative checks that when an item is fully reserved
// (available = 0), the response shows available = 0 (never negative).
// The DB enforces reserved <= total_stock via CHECK constraint, so available is always >= 0.
func TestItems_GetAll_AvailableNeverNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)

	// Create item with 2 total stock, then reserve all 2 units directly.
	itemC := createAPITestItem(t, pool, "items-get-cap", 2)
	_, err := pool.Exec(t.Context(), `UPDATE items SET reserved = 2 WHERE id = $1`, itemC)
	if err != nil {
		t.Fatalf("set reserved to total_stock: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(t.Context(), `UPDATE items SET reserved = 0 WHERE id = $1`, itemC) //nolint:errcheck
	})

	itemStore := store.NewItemStore(pool)
	itemHandler := NewItemHandler(itemStore)
	router := NewRouter(nil, itemHandler, nil, nil, "")

	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set("X-User-Id", "user-cap-test")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var items []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, it := range items {
		id, _ := it["id"].(string)
		if id != itemC {
			continue
		}
		available, _ := it["available"].(float64)
		if available != 0 {
			t.Errorf("fully reserved item: available want 0, got %v", available)
		}
		if available < 0 {
			t.Errorf("available must never be negative, got %v", available)
		}
		return
	}
	t.Fatalf("itemC %q not found in response", itemC)
}

// --- T028b: reserve publishes a broadcast event to the hub ---

func TestItems_Reserve_PublishesBroadcastEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)
	itemID := createAPITestItem(t, pool, "items-broadcast", 5)

	pub := newMockPublisher(4)

	rs := store.NewReservationStore(pool)
	itemStore := store.NewItemStore(pool)
	itemHandler := NewItemHandler(itemStore)
	h := NewReservationHandler(rs, 0, pub)
	router := NewRouter(h, itemHandler, nil, nil, "")

	body := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID)
	req := httptest.NewRequest(http.MethodPost, "/reservations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user-broadcast")
	req.Header.Set("Idempotency-Key", "key-broadcast-001")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Assert the broadcast event was delivered within a short timeout.
	select {
	case evt := <-pub.events:
		if evt.ItemID != itemID {
			t.Errorf("event.ItemID want %q, got %q", itemID, evt.ItemID)
		}
		if evt.Type != domain.EventTypeReserved {
			t.Errorf("event.Type want %q, got %q", domain.EventTypeReserved, evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast event after reserve")
	}
}

// --- helpers ---

func assertItemFields(t *testing.T, it map[string]interface{}, wantTotal, wantReserved, wantAvailable int) {
	t.Helper()
	totalStock, _ := it["totalStock"].(float64)
	reserved, _ := it["reserved"].(float64)
	available, _ := it["available"].(float64)

	if int(totalStock) != wantTotal {
		t.Errorf("totalStock: want %d, got %d", wantTotal, int(totalStock))
	}
	if int(reserved) != wantReserved {
		t.Errorf("reserved: want %d, got %d", wantReserved, int(reserved))
	}
	if int(available) != wantAvailable {
		t.Errorf("available: want %d, got %d", wantAvailable, int(available))
	}
	// Verify required fields exist.
	if _, ok := it["id"]; !ok {
		t.Error("missing field: id")
	}
	if _, ok := it["name"]; !ok {
		t.Error("missing field: name")
	}
}
