package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dfsalazar40/inventory-go/backend/internal/store"
)

// newIntegrationRouter builds a real router backed by a real Postgres pool,
// wiring store → handler → router exactly as production does.
// It uses the shared storetest pool helper through the store package.
func newIntegrationRouter(t *testing.T) (http.Handler, *store.ReservationStore) {
	t.Helper()
	pool := newAPITestPool(t)
	rs := store.NewReservationStore(pool)
	h := NewReservationHandler(rs, 0, nil)
	return NewRouter(h, nil, nil, ""), rs
}

// TestIdempotency_ConcurrentSameKeyAndPayload fires two reserve requests
// with the SAME Idempotency-Key and the SAME payload concurrently. Exactly
// ONE reservation must be created and exactly ONE stock decrement must happen.
//
// SC-003 / US6 scenario 3 / T021.
func TestIdempotency_ConcurrentSameKeyAndPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newAPITestPool(t)
	itemID := createAPITestItem(t, pool, "idempotency-concurrent", 5)

	rs := store.NewReservationStore(pool)
	h := NewReservationHandler(rs, 0, nil)
	router := NewRouter(h, nil, nil, "")

	const goroutines = 2
	idemKey := "test-concurrent-idempotency-key"
	body := fmt.Sprintf(`{"itemId":%q,"quantity":1}`, itemID)

	var (
		wg      sync.WaitGroup
		barrier = make(chan struct{})
		mu      sync.Mutex

		successCount int
		successID    string
		codes        []int
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier

			req := httptest.NewRequest(http.MethodPost, "/reservations",
				bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", "user-idem")
			req.Header.Set("Idempotency-Key", idemKey)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			mu.Lock()
			defer mu.Unlock()
			codes = append(codes, w.Code)

			if w.Code == http.StatusCreated {
				successCount++
				var r map[string]interface{}
				if err := json.NewDecoder(w.Body).Decode(&r); err == nil {
					if id, ok := r["id"].(string); ok {
						if successID == "" {
							successID = id
						} else if successID != id {
							t.Errorf("two different reservation IDs returned: %s vs %s", successID, id)
						}
					}
				}
			}
		}()
	}

	close(barrier)
	wg.Wait()

	// Both goroutines must return a success — one creates, the other replays.
	// The reservation ID must be the same for both.
	for _, code := range codes {
		if code != http.StatusCreated {
			t.Errorf("want 201 for both goroutines, got %d", code)
		}
	}
	if successCount != goroutines {
		t.Errorf("want both goroutines to return 201, got %d successes out of %d", successCount, goroutines)
	}

	// Exactly ONE stock decrement: reserved == 1.
	var reserved int
	err := pool.QueryRow(t.Context(), `SELECT reserved FROM items WHERE id = $1`, itemID).Scan(&reserved)
	if err != nil {
		t.Fatalf("read reserved: %v", err)
	}
	if reserved != 1 {
		t.Errorf("want reserved=1 (single decrement), got %d", reserved)
	}
}
