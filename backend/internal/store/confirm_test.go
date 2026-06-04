package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// TestConfirm_PendingToConfirmed verifies the happy-path transition:
//   - status becomes confirmed
//   - expires_at becomes NULL
//   - confirmed_at is set
//   - stock (reserved) does NOT change
//
// T025 [US8]
func TestConfirm_PendingToConfirmed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "confirm-happy", 3)

	// Create a pending reservation.
	r, err := s.Reserve(ctx, ReserveParams{ItemID: itemID, UserID: "user-confirm", Quantity: 1, TTL: 60 * time.Second, IdempotencyKey: "confirm-happy-key"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	reservedBefore := itemReserved(t, pool, itemID)

	// Confirm it.
	confirmed, err := s.Confirm(ctx, r.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if confirmed.Status != domain.StatusConfirmed {
		t.Errorf("want status=confirmed, got %s", confirmed.Status)
	}
	if confirmed.ExpiresAt != nil {
		t.Errorf("want expires_at=NULL after confirm, got %v", confirmed.ExpiresAt)
	}
	if confirmed.ConfirmedAt == nil {
		t.Error("want confirmed_at to be set, got nil")
	}

	// No stock change on confirm.
	reservedAfter := itemReserved(t, pool, itemID)
	if reservedBefore != reservedAfter {
		t.Errorf("confirm must not change reserved: before=%d after=%d", reservedBefore, reservedAfter)
	}
}

// TestConfirm_DoesNotExpireAfterTTL verifies that a confirmed reservation
// still exists after its original TTL window has elapsed.
//
// T025 [US8]
func TestConfirm_DoesNotExpireAfterTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	// Use a very short TTL so we can test expiry behaviour quickly.
	itemID := createTestItem(t, pool, "confirm-no-expire", 2)

	r, err := s.Reserve(ctx, ReserveParams{ItemID: itemID, UserID: "user-no-expire", Quantity: 1, TTL: 100 * time.Millisecond, IdempotencyKey: "no-expire-key"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Confirm before TTL elapses.
	confirmed, err := s.Confirm(ctx, r.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != domain.StatusConfirmed {
		t.Fatalf("want confirmed, got %s", confirmed.Status)
	}

	// Wait well past the original TTL.
	time.Sleep(300 * time.Millisecond)

	// Directly query the row — it must still exist and be confirmed.
	var status string
	err = pool.QueryRow(ctx,
		`SELECT status FROM reservations WHERE id = $1`, r.ID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("read reservation after TTL: %v", err)
	}
	if status != "confirmed" {
		t.Errorf("want status=confirmed after TTL, got %s", status)
	}

	// Stock must still be held.
	reserved := itemReserved(t, pool, itemID)
	if reserved != 1 {
		t.Errorf("want reserved=1 (units still held), got %d", reserved)
	}
}

// TestConfirm_Idempotent_AlreadyConfirmed re-confirms an already-confirmed
// reservation; it must return the same reservation as a 200 no-op with no
// extra stock effect.
//
// FR-016 / US8 scenario 4 / T025.
func TestConfirm_Idempotent_AlreadyConfirmed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "confirm-idempotent", 3)

	r, err := s.Reserve(ctx, ReserveParams{ItemID: itemID, UserID: "user-idem-confirm", Quantity: 1, IdempotencyKey: "idem-confirm-key"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// First confirm.
	c1, err := s.Confirm(ctx, r.ID)
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}

	reservedAfterFirst := itemReserved(t, pool, itemID)

	// Re-confirm — must return the same reservation with no error.
	c2, err := s.Confirm(ctx, r.ID)
	if err != nil {
		t.Errorf("re-confirm must be a no-op, got error: %v", err)
	}
	if c2 == nil {
		t.Fatal("re-confirm returned nil reservation")
	}
	if c1.ID != c2.ID {
		t.Errorf("want same id on re-confirm: %s vs %s", c1.ID, c2.ID)
	}
	if c2.Status != domain.StatusConfirmed {
		t.Errorf("want status=confirmed on re-confirm, got %s", c2.Status)
	}

	// No extra stock effect.
	reservedAfterSecond := itemReserved(t, pool, itemID)
	if reservedAfterFirst != reservedAfterSecond {
		t.Errorf("re-confirm must not change reserved: before=%d after=%d", reservedAfterFirst, reservedAfterSecond)
	}
}

// TestConfirm_ReleasedReservation verifies that confirming a released
// reservation returns ErrNotFound or ErrNotPending (terminal state).
//
// T025 [US8]
func TestConfirm_ReleasedReservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "confirm-released", 3)

	r, err := s.Reserve(ctx, ReserveParams{ItemID: itemID, UserID: "user-released", Quantity: 1, IdempotencyKey: "released-key"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Manually transition to released.
	_, err = pool.Exec(ctx,
		`UPDATE reservations SET status='released', released_at=now() WHERE id=$1`, r.ID)
	if err != nil {
		t.Fatalf("force release: %v", err)
	}
	// Return stock too (to simulate a proper release).
	pool.Exec(ctx, `UPDATE items SET reserved = reserved - $1 WHERE id = $2`, r.Quantity, itemID) //nolint:errcheck

	_, err = s.Confirm(ctx, r.ID)
	if err == nil {
		t.Error("want error when confirming a released reservation, got nil")
		return
	}
	if !errors.Is(err, domain.ErrNotPending) && !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotPending or ErrNotFound, got %v", err)
	}
}

// TestConfirm_ExpiredReservation verifies that confirming an expired
// reservation returns ErrNotFound or ErrNotPending.
//
// T025 [US8]
func TestConfirm_ExpiredReservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	itemID := createTestItem(t, pool, "confirm-expired", 3)

	r, err := s.Reserve(ctx, ReserveParams{ItemID: itemID, UserID: "user-expired", Quantity: 1, IdempotencyKey: "expired-key"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Force to expired.
	pool.Exec(ctx, `UPDATE reservations SET status='expired' WHERE id=$1`, r.ID)   //nolint:errcheck
	pool.Exec(ctx, `UPDATE items SET reserved = reserved - $1 WHERE id = $2`, r.Quantity, itemID) //nolint:errcheck

	_, err = s.Confirm(ctx, r.ID)
	if err == nil {
		t.Error("want error when confirming an expired reservation, got nil")
		return
	}
	if !errors.Is(err, domain.ErrNotPending) && !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotPending or ErrNotFound, got %v", err)
	}
}

// TestConfirm_AbsentReservation verifies that confirming a non-existent id
// returns ErrNotFound.
//
// T025 [US8]
func TestConfirm_AbsentReservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	_, err := s.Confirm(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("want ErrNotFound for absent reservation, got %v", err)
	}
}

// TestConfirm_VsExpireRace fires a confirm and a manual pending→expired
// transition concurrently on the same pending row. Exactly one outcome must
// win. If confirm wins: status=confirmed, units held. If expire wins:
// status=expired, units returned once.
//
// T025 [US8] confirm-vs-expire race.
func TestConfirm_VsExpireRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	s := NewReservationStore(pool)
	ctx := context.Background()

	for attempt := 0; attempt < 20; attempt++ {
		itemID := createTestItem(t, pool, fmt.Sprintf("confirm-race-%d", attempt), 2)

		r, err := s.Reserve(ctx, ReserveParams{
			ItemID:         itemID,
			UserID:         "user-race",
			Quantity:       1,
			TTL:            60 * time.Second,
			IdempotencyKey: fmt.Sprintf("race-key-%d", attempt),
		})
		if err != nil {
			t.Fatalf("reserve attempt %d: %v", attempt, err)
		}

		var (
			wg      sync.WaitGroup
			barrier = make(chan struct{})
		)

		var confirmErr, expireErr error
		var confirmedRes *domain.Reservation

		// Goroutine 1: confirm.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			confirmedRes, confirmErr = s.Confirm(ctx, r.ID)
		}()

		// Goroutine 2: force expire (the TTL sweeper pattern).
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			// Simulate the conditional pending→expired sweep transition,
			// returning stock in the same CTE.
			_, expireErr = pool.Exec(ctx, `
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
		}()

		close(barrier)
		wg.Wait()

		if expireErr != nil {
			t.Fatalf("attempt %d: expire exec error: %v", attempt, expireErr)
		}

		// Read the final state.
		var finalStatus string
		pool.QueryRow(ctx, `SELECT status FROM reservations WHERE id = $1`, r.ID).Scan(&finalStatus) //nolint:errcheck

		reserved := itemReserved(t, pool, itemID)
		total := itemTotalStock(t, pool, itemID)

		switch finalStatus {
		case "confirmed":
			// Confirm won. Units still held. No stock returned.
			if confirmErr != nil {
				t.Errorf("attempt %d: confirm winner but confirmErr=%v", attempt, confirmErr)
			}
			if confirmedRes == nil || confirmedRes.Status != domain.StatusConfirmed {
				t.Errorf("attempt %d: confirmed result expected, got %v", attempt, confirmedRes)
			}
			if reserved != 1 {
				t.Errorf("attempt %d: confirmed win — want reserved=1, got %d", attempt, reserved)
			}
		case "expired":
			// Expire won. Stock returned exactly once. Confirm must report ErrNotPending/ErrNotFound.
			if confirmErr == nil {
				t.Errorf("attempt %d: expire won but confirm returned no error", attempt)
			}
			if !errors.Is(confirmErr, domain.ErrNotPending) && !errors.Is(confirmErr, domain.ErrNotFound) {
				t.Errorf("attempt %d: expire won — want ErrNotPending or ErrNotFound, got %v", attempt, confirmErr)
			}
			if reserved != 0 {
				t.Errorf("attempt %d: expired win — want reserved=0 (stock returned), got %d", attempt, reserved)
			}
		default:
			t.Errorf("attempt %d: unexpected final status: %q", attempt, finalStatus)
		}

		// Invariant: reserved never exceeds total.
		if reserved > total {
			t.Errorf("attempt %d: invariant violated: reserved (%d) > total (%d)", attempt, reserved, total)
		}

		// Clean up for next attempt.
		pool.Exec(ctx, `DELETE FROM reservations WHERE item_id = $1`, itemID) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, itemID)             //nolint:errcheck
	}
}
