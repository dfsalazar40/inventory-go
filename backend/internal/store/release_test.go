// Package store_test contains integration tests for the release path (US4).
//
// Test coverage (T039 [US4]):
//   - release returns stock exactly once (happy path)
//   - double-release is a safe no-op (stock not returned twice)
//   - release-after-expire is a no-op (clock-skew case; never an error, never a
//     second decrement)
//
// Design (research §3): the conditional transition WHERE status='pending' is the
// exactly-once mutex shared by both release and expire. Whichever commits first
// wins; the loser matches 0 rows and is a safe no-op.
package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRelease_HappyPath verifies that releasing a pending reservation returns
// its stock exactly once and transitions the status to released.
//
// T039 [US4] — FR-007, SC-004.
func TestRelease_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "release-happy", 5)

	r, err := s.Reserve(ctx, ReserveParams{
		ItemID:         itemID,
		UserID:         "user-release-happy",
		Quantity:       2,
		TTL:            60 * time.Second,
		IdempotencyKey: "release-happy-key",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if got := itemReserved(t, pool, itemID); got != 2 {
		t.Fatalf("want reserved=2 after reserve, got %d", got)
	}

	// Release.
	if _, err := s.Release(ctx, r.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Stock returned.
	if got := itemReserved(t, pool, itemID); got != 0 {
		t.Errorf("want reserved=0 after release, got %d", got)
	}

	// Status transitioned.
	var status string
	pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id=$1`, r.ID).Scan(&status) //nolint:errcheck
	if status != "released" {
		t.Errorf("want status=released, got %s", status)
	}
}

// TestRelease_DoubleRelease verifies that releasing the same reservation twice
// is a safe no-op: stock returns exactly once and the second call does not error.
//
// T039 [US4] — FR-008, SC-004, Edge Case "double-release".
func TestRelease_DoubleRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "release-double", 5)

	r, err := s.Reserve(ctx, ReserveParams{
		ItemID:         itemID,
		UserID:         "user-double",
		Quantity:       1,
		TTL:            60 * time.Second,
		IdempotencyKey: "double-release-key",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// First release.
	if _, err := s.Release(ctx, r.ID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	reservedAfterFirst := itemReserved(t, pool, itemID)

	// Second release — must be a no-op, no error.
	if _, err := s.Release(ctx, r.ID); err != nil {
		t.Errorf("second release must be a no-op, got error: %v", err)
	}
	reservedAfterSecond := itemReserved(t, pool, itemID)

	if reservedAfterFirst != reservedAfterSecond {
		t.Errorf("double-release must not change reserved: after-first=%d after-second=%d",
			reservedAfterFirst, reservedAfterSecond)
	}
}

// TestRelease_AfterExpire verifies that releasing an already-expired reservation
// is a safe no-op: never an error, never a second stock decrement.
//
// T039 [US4] — FR-007/FR-008, Edge Case "release after expiration" (clock-skew).
func TestRelease_AfterExpire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "release-after-expire", 5)

	r, err := s.Reserve(ctx, ReserveParams{
		ItemID:         itemID,
		UserID:         "user-after-expire",
		Quantity:       1,
		TTL:            60 * time.Second,
		IdempotencyKey: "after-expire-key",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Simulate the sweeper: transition to expired and return stock.
	_, err = pool.Exec(ctx, `
		WITH expired AS (
			UPDATE reservations
			   SET status = 'expired'
			 WHERE id = $1 AND status = 'pending'
			RETURNING item_id, quantity
		)
		UPDATE items i
		   SET reserved = i.reserved - e.quantity
		  FROM expired e
		 WHERE i.id = e.item_id
	`, r.ID)
	if err != nil {
		t.Fatalf("force expire: %v", err)
	}

	reservedAfterExpire := itemReserved(t, pool, itemID)

	// Release on an already-expired reservation: must be a no-op, no error.
	if _, err := s.Release(ctx, r.ID); err != nil {
		t.Errorf("release-after-expire must be a no-op, got error: %v", err)
	}

	// Stock must not change.
	if got := itemReserved(t, pool, itemID); got != reservedAfterExpire {
		t.Errorf("release-after-expire must not change reserved: before=%d after=%d",
			reservedAfterExpire, got)
	}
}

// TestRelease_NotFound verifies that releasing a non-existent reservation id
// returns ErrNotFound (the handler maps this to a 200 no-op per FR-008, since
// releasing an absent id cannot double-decrement stock).
//
// T039 [US4]
func TestRelease_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	// Releasing an absent id returns ErrNotFound; the handler treats this as no-op 200.
	_, err := s.Release(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("want ErrNotFound for non-existent id, got nil")
	}
}

// TestRelease_DoubleReleaseConcurrent fires two concurrent releases of the same
// pending reservation; stock must return exactly once regardless of race winner.
//
// T039 [US4] — constitution NON-NEGOTIABLE (Principle III + IV).
func TestRelease_DoubleReleaseConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	for attempt := 0; attempt < 20; attempt++ {
		itemID := createTestItem(t, pool, fmt.Sprintf("release-concurrent-%d", attempt), 3)

		r, err := s.Reserve(ctx, ReserveParams{
			ItemID:         itemID,
			UserID:         "concurrent-user",
			Quantity:       1,
			TTL:            60 * time.Second,
			IdempotencyKey: fmt.Sprintf("concurrent-release-key-%d", attempt),
		})
		if err != nil {
			t.Fatalf("attempt %d: reserve: %v", attempt, err)
		}

		barrier := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)

		for i := 0; i < 2; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-barrier
				_, errs[i] = s.Release(ctx, r.ID)
			}()
		}

		close(barrier)
		wg.Wait()

		// Both must succeed as no-ops (no error).
		for i, e := range errs {
			if e != nil {
				t.Errorf("attempt %d, goroutine %d: release error: %v", attempt, i, e)
			}
		}

		// Stock returned exactly once.
		if got := itemReserved(t, pool, itemID); got != 0 {
			t.Errorf("attempt %d: want reserved=0 (returned once), got %d", attempt, got)
		}

		// Cleanup.
		pool.Exec(ctx, `DELETE FROM reservations WHERE item_id=$1`, itemID) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM items WHERE id=$1`, itemID)             //nolint:errcheck
	}
}
