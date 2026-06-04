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

	// Step 2: insert the reservation row in the same transaction.
	// The expiration time is set from TTL at hold creation.
	expiresAt := time.Now().UTC().Add(p.ttl())

	const insertSQL = `
		INSERT INTO reservations (item_id, user_id, quantity, status, expires_at)
		VALUES ($1, $2, $3, 'pending', $4)
		RETURNING id, item_id, user_id, quantity, status, created_at, expires_at,
		          confirmed_at, released_at`

	row := tx.QueryRow(ctx, insertSQL, p.ItemID, p.UserID, p.Quantity, expiresAt)

	r, err := scanReservation(row)
	if err != nil {
		return nil, fmt.Errorf("insert reservation: %w", err)
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

// scanReservation scans a single reservation row from a pgx.Row.
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
	return &r, nil
}
