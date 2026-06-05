-- Drop the display-order column. Quantities are left as-is (down does not restore
-- the prior catalog values; re-running 000004/000005 up re-seeds them).
ALTER TABLE items DROP COLUMN IF EXISTS sort_order;
