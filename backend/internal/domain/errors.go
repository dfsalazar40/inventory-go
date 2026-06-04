package domain

import "errors"

// Typed domain errors. Handlers map these to HTTP status codes and error codes.
var (
	// ErrInsufficientStock is returned when the atomic UPDATE finds no available stock.
	ErrInsufficientStock = errors.New("insufficient_stock")

	// ErrConflict is returned on a general concurrency conflict (race lost).
	ErrConflict = errors.New("conflict")

	// ErrIdempotencyKeyConflict is returned when a key is reused with a different payload.
	ErrIdempotencyKeyConflict = errors.New("idempotency_key_conflict")

	// ErrIdempotencyKeyRequired is returned when the Idempotency-Key header is absent.
	ErrIdempotencyKeyRequired = errors.New("idempotency_key_required")

	// ErrNotFound is returned when a reservation does not exist.
	ErrNotFound = errors.New("not_found")

	// ErrNotPending is returned when a confirm/release is attempted on a non-pending reservation.
	ErrNotPending = errors.New("not_pending")

	// ErrValidation is returned for invalid request payloads.
	ErrValidation = errors.New("validation_error")
)
