package store

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// hashPayload builds a deterministic sha256 hex string from the normalized
// reserve payload: itemId + ":" + quantity. This is the request_hash stored in
// idempotency_keys so replays can be distinguished from key-with-different-payload.
func hashPayload(itemID string, quantity int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", itemID, quantity)))
	return fmt.Sprintf("%x", h)
}

// idempotencyResult is what checkOrRegisterIdempotencyKey returns.
type idempotencyResult struct {
	// existing is non-nil when the key already existed with the SAME hash
	// (safe replay). The caller must return this reservation without creating
	// a new one.
	existing *domain.Reservation
	// conflict is true when the key existed with a DIFFERENT hash.
	conflict bool
}

// checkOrRegisterIdempotencyKey attempts to INSERT the idempotency key inside
// tx (which is the ongoing reserve transaction). The entire flow:
//
//  1. INSERT INTO idempotency_keys (key, request_hash, reservation_id)
//     VALUES ($key, $hash, $reservationID)
//     ON CONFLICT (key) DO NOTHING
//
//  2. If 0 rows inserted (conflict):
//     - load the existing row.
//     - same hash → safe replay: return the stored reservation.
//     - different hash → 409 conflict.
//
//  3. If 1 row inserted → first call; proceed normally (result is empty).
//
// reservationID must be the UUID already inserted in this transaction.
func checkOrRegisterIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	key string,
	requestHash string,
	reservationID string,
) (*idempotencyResult, error) {
	const insertSQL = `
		INSERT INTO idempotency_keys (key, request_hash, reservation_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING`

	tag, err := tx.Exec(ctx, insertSQL, key, requestHash, reservationID)
	if err != nil {
		return nil, fmt.Errorf("insert idempotency key: %w", err)
	}

	if tag.RowsAffected() == 1 {
		// First call — nothing more to do.
		return &idempotencyResult{}, nil
	}

	// 0 rows inserted → key already exists. Load it to decide replay vs conflict.
	const selectSQL = `
		SELECT ik.request_hash,
		       r.id, r.item_id, r.user_id, r.quantity, r.status,
		       r.created_at, r.expires_at, r.confirmed_at, r.released_at
		  FROM idempotency_keys ik
		  JOIN reservations r ON r.id = ik.reservation_id
		 WHERE ik.key = $1`

	row := tx.QueryRow(ctx, selectSQL, key)

	var storedHash string
	var existing domain.Reservation
	var status string

	err = row.Scan(
		&storedHash,
		&existing.ID,
		&existing.ItemID,
		&existing.UserID,
		&existing.Quantity,
		&status,
		&existing.CreatedAt,
		&existing.ExpiresAt,
		&existing.ConfirmedAt,
		&existing.ReleasedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load existing idempotency key: %w", err)
	}
	existing.Status = domain.ReservationStatus(status)

	if storedHash != requestHash {
		return &idempotencyResult{conflict: true}, domain.ErrIdempotencyKeyConflict
	}

	// Same hash → safe replay.
	return &idempotencyResult{existing: &existing}, nil
}
