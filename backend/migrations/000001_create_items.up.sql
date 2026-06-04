-- Items: products available for reservation.
-- total_stock is fixed capacity; reserved tracks active (pending + confirmed) holds.
-- available is derived as total_stock - reserved and never stored.

CREATE TABLE IF NOT EXISTS items (
    id          TEXT        PRIMARY KEY,
    name        TEXT        NOT NULL,
    total_stock INTEGER     NOT NULL CHECK (total_stock >= 0),
    reserved    INTEGER     NOT NULL DEFAULT 0
                            CHECK (reserved >= 0)
                            CHECK (reserved <= total_stock),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for per-item aggregation and event fan-out.
CREATE INDEX IF NOT EXISTS idx_items_id ON items (id);
