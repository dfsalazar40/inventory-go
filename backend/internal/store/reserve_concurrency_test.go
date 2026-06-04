package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// TestReserve_LastUnitContention tests that under heavy concurrency for a
// single unit of stock, exactly 1 goroutine succeeds and the rest get a typed
// insufficient-stock or conflict error. The invariant reserved <= total_stock
// must hold at all times and available must never go negative.
//
// T015 [US1]: last-unit contention
func TestReserve_LastUnitContention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	store := NewReservationStore(pool)

	const goroutines = 50
	const totalStock = 1
	const qty = 1

	itemID := createTestItem(t, pool, "last-unit-contention", totalStock)

	var (
		wg      sync.WaitGroup
		barrier = make(chan struct{})
		mu      sync.Mutex
		successes int
		failures  int
	)

	type result struct {
		err error
	}
	results := make([]result, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier // wait for all goroutines to be ready

			ctx := context.Background()
			_, err := store.Reserve(ctx, ReserveParams{
				ItemID:         itemID,
				UserID:         "user-contention",
				Quantity:       qty,
				IdempotencyKey: fmt.Sprintf("contention-key-%d", idx),
			})
			results[idx] = result{err: err}
		}(i)
	}

	// Release all goroutines simultaneously to maximize contention.
	close(barrier)
	wg.Wait()

	for _, r := range results {
		mu.Lock()
		if r.err == nil {
			successes++
		} else {
			failures++
			// Every failure MUST be a typed domain error (insufficient stock or conflict).
			if !errors.Is(r.err, domain.ErrInsufficientStock) && !errors.Is(r.err, domain.ErrConflict) {
				t.Errorf("unexpected error type: %v", r.err)
			}
		}
		mu.Unlock()
	}

	// Exactly 1 must succeed.
	if successes != totalStock {
		t.Errorf("want exactly %d success(es), got %d (failures: %d)", totalStock, successes, failures)
	}
	if failures != goroutines-totalStock {
		t.Errorf("want exactly %d failure(s), got %d", goroutines-totalStock, failures)
	}

	// Invariant: reserved == total (all stock consumed, never exceeded).
	reserved := itemReserved(t, pool, itemID)
	total := itemTotalStock(t, pool, itemID)
	if reserved != totalStock {
		t.Errorf("want reserved=%d, got %d", totalStock, reserved)
	}
	if reserved > total {
		t.Errorf("invariant violated: reserved (%d) > total_stock (%d)", reserved, total)
	}
}
