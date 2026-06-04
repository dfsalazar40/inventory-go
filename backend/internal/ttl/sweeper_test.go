// Package ttl_test tests the TTL sweeper logic and RESET_TTL_ON_ADD behavior.
//
// Test coverage (T034 [US3]):
//   - pending hold past expires_at → expired, stock returned exactly once
//   - confirmed reservation never expires, even past TTL
//   - expire-vs-release race: concurrent sweep expire and manual release on the
//     same pending row → stock returns exactly once, regardless of winner
//   - RESET_TTL_ON_ADD enabled: new hold resets expires_at for same-user same-item
//     pending holds only; holds on OTHER items are untouched
//   - RESET_TTL_ON_ADD disabled: each hold keeps its original created_at+TTL
package ttl_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/dfsalazar40/inventory-go/backend/internal/store"
	"github.com/dfsalazar40/inventory-go/backend/internal/ttl"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
)

const defaultTestDSN = "postgres://inventory:inventory@localhost:5432/inventory?sslmode=disable"

func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDSN
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := testDSN()

	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("run migrations: %v", err)
	}
	m.Close()

	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.MaxConns = 110

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createTestItem inserts a test item and returns its id.
func createTestItem(t *testing.T, pool *pgxpool.Pool, name string, totalStock int) string {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("ttl-test-%s", name)
	_, err := pool.Exec(ctx,
		`INSERT INTO items (id, name, total_stock, reserved)
		 VALUES ($1, $2, $3, 0)
		 ON CONFLICT (id) DO UPDATE SET total_stock = $3, reserved = 0`,
		id, name, totalStock,
	)
	if err != nil {
		t.Fatalf("createTestItem: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM reservations WHERE item_id = $1`, id)   //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, id)               //nolint:errcheck
	})
	return id
}

// itemReserved reads items.reserved for an item.
func itemReserved(t *testing.T, pool *pgxpool.Pool, itemID string) int {
	t.Helper()
	var reserved int
	err := pool.QueryRow(context.Background(),
		`SELECT reserved FROM items WHERE id = $1`, itemID,
	).Scan(&reserved)
	if err != nil {
		t.Fatalf("itemReserved: %v", err)
	}
	return reserved
}

// reservationStatus reads the status for a reservation by id.
func reservationStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var status string
	err := pool.QueryRow(context.Background(),
		`SELECT status FROM reservations WHERE id = $1`, id,
	).Scan(&status)
	if err != nil {
		t.Fatalf("reservationStatus(%s): %v", id, err)
	}
	return status
}

// mockPublisher records published events for assertions.
type mockPublisher struct {
	mu     sync.Mutex
	events []domain.StockEvent
}

func (m *mockPublisher) Publish(e domain.StockEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *mockPublisher) Events() []domain.StockEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.StockEvent, len(m.events))
	copy(out, m.events)
	return out
}

// TestSweeper_PendingExpired verifies that a pending hold past expires_at is
// transitioned to expired and its stock returned exactly once.
//
// T034 [US3]
func TestSweeper_PendingExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	pub := &mockPublisher{}

	itemID := createTestItem(t, pool, "sweeper-expired", 3)
	s := store.NewReservationStore(pool)

	// Create a hold with a very short TTL.
	r, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemID,
		UserID:         "sweeper-user-1",
		Quantity:       2,
		TTL:            50 * time.Millisecond,
		IdempotencyKey: "sweeper-exp-key-1",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Verify stock is decremented.
	if got := itemReserved(t, pool, itemID); got != 2 {
		t.Fatalf("want reserved=2 after reserve, got %d", got)
	}

	// Wait for TTL to elapse.
	time.Sleep(100 * time.Millisecond)

	// Run one sweeper tick.
	sweeper := ttl.NewSweeper(pool, pub, 60*time.Second)
	swept := sweeper.RunOnce(ctx)
	if swept == 0 {
		t.Fatal("want at least 1 row swept, got 0")
	}

	// Verify status is now expired.
	if status := reservationStatus(t, pool, r.ID); status != "expired" {
		t.Errorf("want status=expired, got %s", status)
	}

	// Verify stock returned.
	if got := itemReserved(t, pool, itemID); got != 0 {
		t.Errorf("want reserved=0 after expire, got %d", got)
	}

	// Verify event published.
	events := pub.Events()
	if len(events) == 0 {
		t.Error("want at least one StockEvent published after expire")
	}
	found := false
	for _, e := range events {
		if e.Type == domain.EventTypeExpired && e.ItemID == itemID {
			found = true
		}
	}
	if !found {
		t.Errorf("want EventTypeExpired for itemID=%s, got %+v", itemID, events)
	}
}

// TestSweeper_ConfirmedNeverExpires verifies that a confirmed reservation is
// not touched by the sweeper even when its original expires_at is in the past.
//
// T034 [US3]
func TestSweeper_ConfirmedNeverExpires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	pub := &mockPublisher{}

	itemID := createTestItem(t, pool, "sweeper-confirmed", 5)
	s := store.NewReservationStore(pool)

	// Reserve and confirm.
	r, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemID,
		UserID:         "sweeper-user-2",
		Quantity:       1,
		TTL:            50 * time.Millisecond,
		IdempotencyKey: "sweeper-conf-key-1",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := s.Confirm(ctx, r.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Wait for original TTL to elapse.
	time.Sleep(100 * time.Millisecond)

	// Run sweeper.
	sweeper := ttl.NewSweeper(pool, pub, 60*time.Second)
	sweeper.RunOnce(ctx)

	// Confirmed reservation must still be confirmed.
	if status := reservationStatus(t, pool, r.ID); status != "confirmed" {
		t.Errorf("want status=confirmed (sweeper must not expire confirmed holds), got %s", status)
	}

	// Stock still held.
	if got := itemReserved(t, pool, itemID); got != 1 {
		t.Errorf("want reserved=1 (units still held), got %d", got)
	}
}

// TestSweeper_ExpireVsReleaseRace fires a sweeper expire and a manual release
// concurrently on the same pending row. Stock must return exactly once.
//
// T034 [US3] — expire-vs-release race (constitution NON-NEGOTIABLE).
func TestSweeper_ExpireVsReleaseRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	pub := &mockPublisher{}
	s := store.NewReservationStore(pool)

	for attempt := 0; attempt < 20; attempt++ {
		itemID := createTestItem(t, pool, fmt.Sprintf("expire-race-%d", attempt), 3)

		r, err := s.Reserve(ctx, store.ReserveParams{
			ItemID:         itemID,
			UserID:         "race-user",
			Quantity:       1,
			TTL:            50 * time.Millisecond,
			IdempotencyKey: fmt.Sprintf("race-key-%d", attempt),
		})
		if err != nil {
			t.Fatalf("attempt %d: reserve: %v", attempt, err)
		}

		// Wait for TTL to elapse so the sweeper CAN expire it.
		time.Sleep(80 * time.Millisecond)

		barrier := make(chan struct{})
		var wg sync.WaitGroup

		var releaseErr error
		sweeper := ttl.NewSweeper(pool, pub, 60*time.Second)

		// Goroutine 1: sweeper expire.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			sweeper.RunOnce(ctx)
		}()

		// Goroutine 2: manual release via store.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			_, releaseErr = s.Release(ctx, r.ID)
		}()

		close(barrier)
		wg.Wait()

		// Release on already-expired is a no-op — not an error.
		if releaseErr != nil {
			t.Errorf("attempt %d: release error: %v", attempt, releaseErr)
		}

		// Regardless of which won, stock must be returned exactly once.
		reserved := itemReserved(t, pool, itemID)
		if reserved != 0 {
			t.Errorf("attempt %d: want reserved=0 (stock returned once), got %d", attempt, reserved)
		}

		// Reservation must be in a terminal state.
		status := reservationStatus(t, pool, r.ID)
		if status != "expired" && status != "released" {
			t.Errorf("attempt %d: want expired or released, got %s", attempt, status)
		}

		// items.reserved must never go negative.
		var total int
		pool.QueryRow(ctx, `SELECT total_stock FROM items WHERE id=$1`, itemID).Scan(&total) //nolint:errcheck
		if reserved < 0 {
			t.Errorf("attempt %d: reserved went negative: %d", attempt, reserved)
		}

		// Cleanup for next attempt.
		pool.Exec(ctx, `DELETE FROM reservations WHERE item_id=$1`, itemID) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM items WHERE id=$1`, itemID)             //nolint:errcheck
	}
}

// TestResetTTLOnAdd_Enabled verifies that when RESET_TTL_ON_ADD is true, adding
// a new pending hold resets expires_at for the same-user, same-item holds only.
// Holds on OTHER items are untouched.
//
// T034 [US3] — per-user, per-item scoping.
func TestResetTTLOnAdd_Enabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	s := store.NewReservationStore(pool)

	// Item A: user will have two pending holds here.
	itemA := createTestItem(t, pool, "reset-ttl-item-a", 10)
	// Item B: user also has a hold here; its expires_at must NOT be reset.
	itemB := createTestItem(t, pool, "reset-ttl-item-b", 10)

	ttlDuration := 200 * time.Millisecond

	// Reserve item A (first hold).
	r1, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "reset-user",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "reset-key-1",
		ResetTTLOnAdd:  true,
	})
	if err != nil {
		t.Fatalf("reserve A first: %v", err)
	}

	// Reserve item B.
	r2, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemB,
		UserID:         "reset-user",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "reset-key-2",
		ResetTTLOnAdd:  true,
	})
	if err != nil {
		t.Fatalf("reserve B: %v", err)
	}

	expiresAtB := r2.ExpiresAt

	// Sleep a bit so we can detect a meaningful reset on item A.
	time.Sleep(80 * time.Millisecond)

	// Reserve item A again (second hold — should reset expires_at for A's pending holds).
	_, err = s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "reset-user",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "reset-key-3",
		ResetTTLOnAdd:  true,
	})
	if err != nil {
		t.Fatalf("reserve A second: %v", err)
	}

	// Read the updated expires_at for r1 (first A hold — should be refreshed).
	var newExpiresAtR1 time.Time
	err = pool.QueryRow(ctx,
		`SELECT expires_at FROM reservations WHERE id=$1`, r1.ID,
	).Scan(&newExpiresAtR1)
	if err != nil {
		t.Fatalf("read expires_at for r1: %v", err)
	}
	// r1.ExpiresAt should have been pushed forward by at least 50ms.
	if !newExpiresAtR1.After(r1.ExpiresAt.Add(50 * time.Millisecond)) {
		t.Errorf("want r1.expires_at reset (pushed forward), got original=%v new=%v",
			r1.ExpiresAt, newExpiresAtR1)
	}

	// Read expires_at for r2 (item B hold — must be UNCHANGED).
	var currentExpiresAtR2 time.Time
	err = pool.QueryRow(ctx,
		`SELECT expires_at FROM reservations WHERE id=$1`, r2.ID,
	).Scan(&currentExpiresAtR2)
	if err != nil {
		t.Fatalf("read expires_at for r2: %v", err)
	}
	// Allow a tiny epsilon but it should essentially be unchanged.
	diff := currentExpiresAtR2.Sub(*expiresAtB)
	if diff < -5*time.Millisecond || diff > 5*time.Millisecond {
		t.Errorf("want item B expires_at unchanged, got diff=%v (original=%v current=%v)",
			diff, expiresAtB, currentExpiresAtR2)
	}
}

// TestResetTTLOnAdd_Disabled verifies that when RESET_TTL_ON_ADD is false, each
// hold keeps its original created_at+TTL with no reset.
//
// T034 [US3]
func TestResetTTLOnAdd_Disabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	s := store.NewReservationStore(pool)

	itemA := createTestItem(t, pool, "no-reset-item-a", 10)

	ttlDuration := 500 * time.Millisecond

	// First reserve on item A.
	r1, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "no-reset-user",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "no-reset-key-1",
		ResetTTLOnAdd:  false,
	})
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	originalExpiresAt := r1.ExpiresAt

	time.Sleep(80 * time.Millisecond)

	// Second reserve on the same item — no reset.
	_, err = s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "no-reset-user",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "no-reset-key-2",
		ResetTTLOnAdd:  false,
	})
	if err != nil {
		t.Fatalf("reserve second: %v", err)
	}

	// r1's expires_at must be unchanged.
	var currentExpiresAt time.Time
	err = pool.QueryRow(ctx,
		`SELECT expires_at FROM reservations WHERE id=$1`, r1.ID,
	).Scan(&currentExpiresAt)
	if err != nil {
		t.Fatalf("read expires_at: %v", err)
	}

	diff := currentExpiresAt.Sub(*originalExpiresAt)
	if diff < -5*time.Millisecond || diff > 5*time.Millisecond {
		t.Errorf("want expires_at unchanged when ResetTTLOnAdd=false, got diff=%v", diff)
	}
}

// TestResetTTLOnAdd_OtherUserNotAffected verifies that resetting TTL for one
// user's pending holds does NOT affect another user's holds on the same item.
//
// T034 [US3]
func TestResetTTLOnAdd_OtherUserNotAffected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := newTestPool(t)
	ctx := context.Background()
	s := store.NewReservationStore(pool)

	itemA := createTestItem(t, pool, "cross-user-item", 10)

	ttlDuration := 500 * time.Millisecond

	// User A reserves item.
	rUserA, err := s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "user-a",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "cross-user-key-1",
		ResetTTLOnAdd:  true,
	})
	if err != nil {
		t.Fatalf("user-a reserve: %v", err)
	}
	originalUserAExpiry := rUserA.ExpiresAt

	time.Sleep(80 * time.Millisecond)

	// User B reserves the same item — should NOT reset user A's expiry.
	_, err = s.Reserve(ctx, store.ReserveParams{
		ItemID:         itemA,
		UserID:         "user-b",
		Quantity:       1,
		TTL:            ttlDuration,
		IdempotencyKey: "cross-user-key-2",
		ResetTTLOnAdd:  true,
	})
	if err != nil {
		t.Fatalf("user-b reserve: %v", err)
	}

	// User A's expiry must be unchanged.
	var currentExpiry time.Time
	err = pool.QueryRow(ctx,
		`SELECT expires_at FROM reservations WHERE id=$1`, rUserA.ID,
	).Scan(&currentExpiry)
	if err != nil {
		t.Fatalf("read user-a expires_at: %v", err)
	}

	diff := currentExpiry.Sub(*originalUserAExpiry)
	if diff < -5*time.Millisecond || diff > 5*time.Millisecond {
		t.Errorf("user-b add must not affect user-a expiry, got diff=%v", diff)
	}
}
