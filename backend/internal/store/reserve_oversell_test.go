package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// TestReserve_Oversell tests that under 100 concurrent reserve requests
// for an item with only 10 units, exactly 10 succeed and 90 are rejected.
// The invariant reserved <= total_stock must hold at all times.
//
// T016 [US1]: oversell prevention
func TestReserve_Oversell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	store := NewReservationStore(pool)

	const goroutines = 100
	const totalStock = 10
	const qty = 1

	itemID := createTestItem(t, pool, "oversell-prevention", totalStock)

	var (
		wg      sync.WaitGroup
		barrier = make(chan struct{})
	)

	type result struct {
		err error
	}
	results := make([]result, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier // maximize contention

			ctx := context.Background()
			_, err := store.Reserve(ctx, ReserveParams{
				ItemID:         itemID,
				UserID:         "user-oversell",
				Quantity:       qty,
				IdempotencyKey: fmt.Sprintf("oversell-key-%d", idx),
			})
			results[idx] = result{err: err}
		}(i)
	}

	close(barrier)
	wg.Wait()

	var successes, failures int
	for _, r := range results {
		if r.err == nil {
			successes++
		} else {
			failures++
			// Every failure MUST be a typed domain error.
			if !errors.Is(r.err, domain.ErrInsufficientStock) && !errors.Is(r.err, domain.ErrConflict) {
				t.Errorf("unexpected error type: %v", r.err)
			}
		}
	}

	// Exactly totalStock goroutines must succeed.
	if successes != totalStock {
		t.Errorf("want exactly %d success(es), got %d", totalStock, successes)
	}
	if failures != goroutines-totalStock {
		t.Errorf("want exactly %d failure(s), got %d", goroutines-totalStock, failures)
	}

	// INV-1: reserved must equal total (all stock claimed) and never exceed it.
	reserved := itemReserved(t, pool, itemID)
	total := itemTotalStock(t, pool, itemID)

	if reserved != totalStock {
		t.Errorf("want reserved=%d, got %d", totalStock, reserved)
	}
	if reserved > total {
		t.Errorf("INV-1 violated: reserved (%d) > total_stock (%d)", reserved, total)
	}
	// INV-4: available never negative.
	available := total - reserved
	if available < 0 {
		t.Errorf("INV-4 violated: available (%d) is negative", available)
	}
}
