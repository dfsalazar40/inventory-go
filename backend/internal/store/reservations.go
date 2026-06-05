// Package store implements database access for the inventory system.
// All concurrency correctness is enforced at the PostgreSQL layer via
// atomic conditional writes — no application-level locks are used.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReserveParams holds the inputs for a reserve operation.
type ReserveParams struct {
	ItemID   string
	UserID   string
	Quantity int
	// TTL is the duration a PENDING hold lives before expiring.
	// Defaults to 60 seconds if zero.
	TTL time.Duration
	// IdempotencyKey is the frontend-generated key (Idempotency-Key header).
	// Required: Reserve returns ErrIdempotencyKeyRequired when empty.
	IdempotencyKey string
	// ResetTTLOnAdd controls whether adding this hold resets expires_at for
	// the user's existing pending holds on the same item (per-user, per-item).
	// When true, a fresh TTL window is shared across all the user's pending
	// holds of that item. Holds on other items are untouched.
	ResetTTLOnAdd bool
}

// ttl returns the effective TTL for a reserve operation.
func (p ReserveParams) ttl() time.Duration {
	if p.TTL > 0 {
		return p.TTL
	}
	return 60 * time.Second
}

// ReservationStorer is the interface satisfied by ReservationStore.
// Declare it here so both the handler and test stubs can reference it.
type ReservationStorer interface {
	Reserve(ctx context.Context, p ReserveParams) (*domain.Reservation, error)
	Confirm(ctx context.Context, reservationID string) (*domain.Reservation, error)
	// Release transitions the reservation from PENDING to RELEASED and returns
	// the current reservation state. If the reservation is already in a terminal
	// state (released/expired/confirmed), returns the current row as a no-op.
	// Returns ErrNotFound if the id does not exist.
	Release(ctx context.Context, reservationID string) (*domain.Reservation, error)
	// ListByUser returns all PENDING and CONFIRMED reservations for a given user,
	// applying lazy expiration so expired-but-unswept rows are excluded (FR-012).
	ListByUser(ctx context.Context, userID string) ([]domain.Reservation, error)
}

// ReservationStore is the concrete Postgres-backed implementation.
type ReservationStore struct {
	pool *pgxpool.Pool
}

// NewReservationStore creates a new ReservationStore backed by the given pool.
func NewReservationStore(pool *pgxpool.Pool) *ReservationStore {
	return &ReservationStore{pool: pool}
}

// Reserve creates a PENDING hold on the given item for the given user and
// quantity. It is the heart of Principle II: the entire operation is a single
// transaction containing one atomic conditional UPDATE on items followed by one
// INSERT into reservations.
//
// Idempotency (FR-009/FR-010, research §4): p.IdempotencyKey is required.
// Inside the transaction, after inserting the reservation, we attempt
// INSERT INTO idempotency_keys ON CONFLICT DO NOTHING. If the key already
// existed:
//   - same request_hash → safe replay: return the existing reservation, no
//     stock change, no new insert (rollback this tx).
//   - different hash → 409: roll back and return ErrIdempotencyKeyConflict.
//
// The UPDATE predicate `total_stock - reserved >= qty` is the sole correctness
// gate. Postgres evaluates it atomically under row-level locking: two concurrent
// requests for the last unit serialize on the row and exactly one sees the
// predicate satisfied. No read-then-write, no application locks.
//
// Returns domain.ErrInsufficientStock when 0 rows are affected (no stock).
// Returns domain.ErrIdempotencyKeyRequired when IdempotencyKey is empty.
func (s *ReservationStore) Reserve(ctx context.Context, p ReserveParams) (*domain.Reservation, error) {
	// Enforce required idempotency key before opening a transaction.
	if p.IdempotencyKey == "" {
		return nil, domain.ErrIdempotencyKeyRequired
	}

	requestHash := hashPayload(p.ItemID, p.Quantity)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Step 1: atomic conditional UPDATE on items.
	// This single statement checks availability and claims the stock atomically.
	// If 0 rows → insufficient stock (predicate not met).
	// If 1 row → stock secured; proceed to insert the reservation in the same tx.
	const updateSQL = `
		UPDATE items
		   SET reserved = reserved + $1
		 WHERE id = $2
		   AND total_stock - reserved >= $1
		RETURNING id`

	var returnedID string
	err = tx.QueryRow(ctx, updateSQL, p.Quantity, p.ItemID).Scan(&returnedID)
	if err != nil {
		if err == pgx.ErrNoRows {
			// 0 rows affected: predicate not satisfied — insufficient stock.
			return nil, domain.ErrInsufficientStock
		}
		// Defense-in-depth: if the DB CHECK constraint (reserved <= total_stock,
		// SQLSTATE 23514) fires due to a logic bug, classify it as insufficient
		// stock rather than leaking a 500. This should never trigger in normal
		// operation — the UPDATE predicate is the sole correctness gate — but
		// ensures a safe, typed error if it ever does.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return nil, domain.ErrInsufficientStock
		}
		return nil, fmt.Errorf("atomic reserve UPDATE: %w", err)
	}

	// Step 2: upsert the reservation row in the same transaction.
	//
	// Aggregation (FR: repeated holds on the same item collapse into one row):
	// a partial unique index on (user_id, item_id) WHERE status='pending' lets us
	// ON CONFLICT DO UPDATE to ADD quantity to the user's existing pending hold of
	// this item instead of creating a second row. items.reserved was already
	// incremented atomically in Step 1, so the row quantity and items.reserved
	// stay in lock-step.
	//
	// RESET_TTL_ON_ADD (T037, research §5, FR-017) is folded into the conflict
	// path: when enabled, the existing pending hold's expires_at is refreshed to
	// the new window; when disabled, it is preserved. Scoped to (user_id, item_id)
	// by the conflict target — other items and other users are never touched.
	expiresAt := time.Now().UTC().Add(p.ttl())

	const upsertSQL = `
		INSERT INTO reservations (item_id, user_id, quantity, status, expires_at)
		VALUES ($1, $2, $3, 'pending', $4)
		ON CONFLICT (user_id, item_id) WHERE status = 'pending'
		DO UPDATE SET
			quantity   = reservations.quantity + EXCLUDED.quantity,
			expires_at = CASE WHEN $5 THEN EXCLUDED.expires_at ELSE reservations.expires_at END
		RETURNING id, item_id, user_id, quantity, status, created_at, expires_at,
		          confirmed_at, released_at`

	row := tx.QueryRow(ctx, upsertSQL, p.ItemID, p.UserID, p.Quantity, expiresAt, p.ResetTTLOnAdd)

	r, err := scanReservation(row)
	if err != nil {
		return nil, fmt.Errorf("upsert reservation: %w", err)
	}

	// Step 3: idempotency key registration — inside the same transaction.
	// ON CONFLICT (key) DO NOTHING: if this key is new, 1 row inserted (first
	// call). If already present, 0 rows (duplicate). The result determines
	// whether we commit a new reservation or return the existing one.
	idem, err := checkOrRegisterIdempotencyKey(ctx, tx, p.IdempotencyKey, requestHash, r.ID)
	if err != nil {
		// err is ErrIdempotencyKeyConflict (different payload) — roll back.
		return nil, err
	}

	if idem.existing != nil {
		// Safe replay: key already existed with the same payload hash.
		// Roll back this transaction (the stock UPDATE and reservation INSERT
		// are undone) and return the previously created reservation.
		tx.Rollback(ctx) //nolint:errcheck
		return idem.existing, nil
	}

	// idem.existing == nil → first call; commit everything.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return r, nil
}

// Confirm transitions a reservation from PENDING to CONFIRMED.
//
// Design (data-model.md, research §2):
//   - Conditional UPDATE WHERE status='pending' → exactly-once guarantee.
//   - 0 rows affected → re-read the row:
//     * already confirmed → safe no-op (return the row, no error).
//     * released/expired  → ErrNotPending.
//     * absent            → ErrNotFound.
//   - No stock change: confirmed units stay in items.reserved.
//   - expires_at is set to NULL; confirmed_at is set to now().
func (s *ReservationStore) Confirm(ctx context.Context, reservationID string) (*domain.Reservation, error) {
	const updateSQL = `
		UPDATE reservations
		   SET status = 'confirmed',
		       confirmed_at = now(),
		       expires_at = NULL
		 WHERE id = $1
		   AND status = 'pending'
		RETURNING id, item_id, user_id, quantity, status, created_at, expires_at,
		          confirmed_at, released_at`

	row := s.pool.QueryRow(ctx, updateSQL, reservationID)
	r, err := scanReservation(row)
	if err == nil {
		// Transition succeeded — return the confirmed reservation.
		return r, nil
	}
	if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("confirm UPDATE: %w", err)
	}

	// 0 rows — re-read to distinguish no-op (already confirmed) from errors.
	const selectSQL = `
		SELECT id, item_id, user_id, quantity, status, created_at, expires_at,
		       confirmed_at, released_at
		  FROM reservations
		 WHERE id = $1`

	row2 := s.pool.QueryRow(ctx, selectSQL, reservationID)
	existing, readErr := scanReservation(row2)
	if readErr != nil {
		if readErr == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("confirm re-read: %w", readErr)
	}

	switch existing.Status {
	case domain.StatusConfirmed:
		// Already confirmed — idempotent no-op.
		return existing, nil
	default:
		// released or expired — cannot confirm.
		return nil, domain.ErrNotPending
	}
}

// Release transitions a reservation from PENDING to RELEASED and returns its
// units to items.reserved exactly once (research §3).
//
// The conditional WHERE status='pending' means:
//   - First call on a pending reservation → transition fires, stock returned.
//   - Any subsequent call (already released / expired / confirmed / absent)
//     → 0 rows affected → safe no-op, no error (FR-007, FR-008).
//
// This is the exactly-once mutex shared with the TTL sweeper: whichever
// transition (release or expire) commits first wins; the other matches 0 rows.
//
// Returns the current reservation state regardless of whether the transition
// fired (allows the handler to return the reservation per OpenAPI contract).
// Returns ErrNotFound if the id does not exist at all.
func (s *ReservationStore) Release(ctx context.Context, reservationID string) (*domain.Reservation, error) {
	// Step 1: conditional transition + stock return atomically (research §3).
	// This uses the WHERE status='pending' predicate so the decrement fires
	// at most once across concurrent callers.
	const releaseSQL = `
		WITH released AS (
			UPDATE reservations
			   SET status = 'released', released_at = now()
			 WHERE id = $1 AND status = 'pending'
			RETURNING item_id, quantity
		)
		UPDATE items i
		   SET reserved = i.reserved - r.quantity
		  FROM released r
		 WHERE i.id = r.item_id`

	if _, err := s.pool.Exec(ctx, releaseSQL, reservationID); err != nil {
		return nil, fmt.Errorf("release reservation: %w", err)
	}

	// Step 2: read the current reservation state to return per OpenAPI contract.
	const selectSQL = `
		SELECT id, item_id, user_id, quantity, status,
		       created_at, expires_at, confirmed_at, released_at
		  FROM reservations
		 WHERE id = $1`

	row := s.pool.QueryRow(ctx, selectSQL, reservationID)
	reservation, err := scanReservation(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Reservation does not exist — safe to return ErrNotFound.
			// Note: a prior release on an absent id is still a no-op (no
			// stock was changed). The handler maps this to 200 no-op per FR-008.
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("release: read reservation: %w", err)
	}

	return reservation, nil
}

// seedCatalogSQL is the canonical "initial state" catalog — the single source of
// truth for ResetToSeed. It mirrors migration 000005 so a reset restores exactly
// the seeded inventory (quantities + display order).
const seedCatalogSQL = `
	INSERT INTO items (id, name, total_stock, reserved, sort_order) VALUES
	    ('item-vintage-camera',   'Vintage Camera',   20,  6, 1),
	    ('item-mechanical-watch', 'Mechanical Watch', 10,  6, 2),
	    ('item-acoustic-guitar',  'Acoustic Guitar',  16,  8, 3),
	    ('item-smart-flask',      'Smart Flask',      20, 19, 4),
	    ('item-running-shoes',    'Running Shoes',    12, 12, 5),
	    ('item-gaming-mouse',     'Gaming Mouse',     15, 14, 6)
	ON CONFLICT (id) DO UPDATE
	   SET name        = EXCLUDED.name,
	       total_stock = EXCLUDED.total_stock,
	       reserved    = EXCLUDED.reserved,
	       sort_order  = EXCLUDED.sort_order;`

// ResetToSeed restores the demo to its initial state: it clears every reservation
// and idempotency key and resets the catalog to the seeded baseline, then returns
// the fresh item list (with derived available) so the caller can return it and
// broadcast the new state. Backs POST /reset.
func (s *ReservationStore) ResetToSeed(ctx context.Context) ([]domain.Item, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Clear all holds. idempotency_keys.reservation_id is ON DELETE CASCADE; we
	// truncate both together so the FK is satisfied in a single statement.
	if _, err := tx.Exec(ctx, `TRUNCATE reservations, idempotency_keys`); err != nil {
		return nil, fmt.Errorf("reset: truncate: %w", err)
	}
	// Restore catalog baseline.
	if _, err := tx.Exec(ctx, seedCatalogSQL); err != nil {
		return nil, fmt.Errorf("reset: reseed catalog: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("reset: commit: %w", err)
	}

	// Read the restored catalog (post-commit) to return and broadcast.
	const listSQL = `
		SELECT id, name, total_stock, reserved,
		       GREATEST(total_stock - reserved, 0) AS available, created_at
		  FROM items
		 ORDER BY sort_order, name`
	rows, err := s.pool.Query(ctx, listSQL)
	if err != nil {
		return nil, fmt.Errorf("reset: list items: %w", err)
	}
	defer rows.Close()

	items := []domain.Item{}
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.ID, &it.Name, &it.TotalStock, &it.Reserved, &it.Available, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("reset: scan item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reset: rows: %w", err)
	}
	return items, nil
}

// ListByUser returns the PENDING and CONFIRMED reservations for a user.
//
// Design (FR-012, research §5 lazy expiration):
//   - Fetches rows where status IN ('pending','confirmed') AND user_id = $1.
//   - For pending rows, lazy expiration is applied (scanReservation). Rows that
//     are lazily-expired are excluded from the result so the caller never sees
//     stale pending holds when the sweeper is slightly behind.
//   - Confirmed rows have no TTL and are always included.
//
// Returns an empty (non-nil) slice when the user has no active reservations.
func (s *ReservationStore) ListByUser(ctx context.Context, userID string) ([]domain.Reservation, error) {
	const q = `
		SELECT id, item_id, user_id, quantity, status, created_at, expires_at,
		       confirmed_at, released_at
		  FROM reservations
		 WHERE user_id = $1
		   AND status IN ('pending', 'confirmed')
		 ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list reservations by user: %w", err)
	}
	defer rows.Close()

	var out []domain.Reservation
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reservation: %w", err)
		}
		// Lazy expiration: skip rows that appear pending but are past their TTL.
		// The sweeper will tombstone them shortly; we don't surface them to users.
		if r.Status == domain.StatusExpired {
			continue
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reservations: rows error: %w", err)
	}

	if out == nil {
		out = []domain.Reservation{}
	}
	return out, nil
}

// scanReservation scans a single reservation row from a pgx.Row.
//
// T036 [US3] Lazy expiration on read: if the scanned row has status='pending'
// and its expires_at is in the past, we treat it as expired so a momentary
// sweeper lag never returns stale holds to callers.
// Note: we do NOT mutate the DB here (that is the sweeper's job); we only
// adjust the returned domain object's Status field.
func scanReservation(row pgx.Row) (*domain.Reservation, error) {
	var r domain.Reservation
	var status string

	err := row.Scan(
		&r.ID,
		&r.ItemID,
		&r.UserID,
		&r.Quantity,
		&status,
		&r.CreatedAt,
		&r.ExpiresAt,
		&r.ConfirmedAt,
		&r.ReleasedAt,
	)
	if err != nil {
		return nil, err
	}

	r.Status = domain.ReservationStatus(status)

	// Lazy expiration: treat a pending row past its expires_at as expired in
	// memory so callers (handlers, tests) never observe a stale pending status
	// while the sweeper hasn't run yet. The DB row is NOT updated here.
	if r.Status == domain.StatusPending && r.ExpiresAt != nil && time.Now().After(*r.ExpiresAt) {
		r.Status = domain.StatusExpired
	}

	return &r, nil
}
