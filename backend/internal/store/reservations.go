// Package store implements database access for the inventory system.
// All concurrency correctness is enforced at the PostgreSQL layer via
// atomic conditional writes — no application-level locks are used.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/jackc/pgx/v5"
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
// The UPDATE predicate `total_stock - reserved >= qty` is the sole correctness
// gate. Postgres evaluates it atomically under row-level locking: two concurrent
// requests for the last unit serialize on the row and exactly one sees the
// predicate satisfied. No read-then-write, no application locks.
//
// Returns domain.ErrInsufficientStock when 0 rows are affected (no stock).
func (s *ReservationStore) Reserve(ctx context.Context, p ReserveParams) (*domain.Reservation, error) {
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

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return r, nil
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
