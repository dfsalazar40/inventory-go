-- Aggregate a user's repeated holds on the SAME item into a single PENDING row.
-- A partial unique index on (user_id, item_id) limited to pending rows lets the
-- reserve path use INSERT ... ON CONFLICT DO UPDATE to add quantity instead of
-- creating a new reservation each time. Confirmed/released/expired rows are not
-- covered by the index, so a user can hold a fresh pending batch after confirming.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pending_user_item
    ON reservations (user_id, item_id)
    WHERE status = 'pending';
