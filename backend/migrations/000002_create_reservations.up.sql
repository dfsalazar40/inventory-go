-- Reservations: holds placed by users on item stock.
-- Status lifecycle: pending -> confirmed | released | expired
-- Only pending rows have an expires_at; confirmed rows have expires_at = NULL.
-- reserved on items changes on: reserve (+qty), release/expire (-qty). Confirm does NOT change reserved.

CREATE TABLE IF NOT EXISTS reservations (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id      TEXT        NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
    user_id      TEXT        NOT NULL,
    quantity     INTEGER     NOT NULL CHECK (quantity > 0),
    status       TEXT        NOT NULL CHECK (status IN ('pending', 'confirmed', 'released', 'expired')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,          -- NULL once confirmed
    confirmed_at TIMESTAMPTZ,          -- NULL until confirmed
    released_at  TIMESTAMPTZ           -- NULL until released
);

-- "My reservations" view: filter by user.
CREATE INDEX IF NOT EXISTS idx_reservations_user_id ON reservations (user_id);

-- Sweeper scan: find pending rows past expiry.
CREATE INDEX IF NOT EXISTS idx_reservations_status_expires_at ON reservations (status, expires_at);

-- Per-item aggregation / event fan-out.
CREATE INDEX IF NOT EXISTS idx_reservations_item_id ON reservations (item_id);
