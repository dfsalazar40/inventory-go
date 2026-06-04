-- Idempotency keys: links a frontend-generated key to the reservation it produced.
-- Same key + matching request_hash -> replay (return existing reservation).
-- Same key + different request_hash -> 409 conflict.

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key            TEXT        PRIMARY KEY,
    request_hash   TEXT        NOT NULL,
    reservation_id UUID        NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
