// Package ttl implements the background TTL sweeper that expires pending
// reservations whose holds have elapsed without being confirmed or released.
//
// Design (research §5, FR-005, SC-006):
//   - A Ticker fires approximately every second.
//   - Each tick runs a single atomic CTE (research §3 pattern):
//     pending→expired transition + reserved decrement in one statement.
//   - Only rows WHERE status='pending' AND expires_at <= now() are touched;
//     confirmed reservations are never expired.
//   - The same WHERE status='pending' predicate is the exactly-once mutex shared
//     with the manual Release path: whichever commits first wins, the other
//     matches 0 rows (safe no-op). This resolves the expire-vs-release race
//     (Principle III).
//   - After each tick, a StockEvent of type EventTypeExpired is published to the
//     hub for each distinct item that had at least one reservation expired.
package ttl

import (
	"context"
	"log/slog"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sweeper runs periodic TTL expiration for pending reservations.
type Sweeper struct {
	pool      *pgxpool.Pool
	publisher domain.Publisher
	ttl       time.Duration // kept for reference/logging; sweep uses DB expires_at
}

// NewSweeper creates a Sweeper backed by pool, broadcasting expiration events
// via publisher. ttl is informational (the actual expiry column drives the sweep).
func NewSweeper(pool *pgxpool.Pool, publisher domain.Publisher, ttl time.Duration) *Sweeper {
	return &Sweeper{pool: pool, publisher: publisher, ttl: ttl}
}

// Run starts the sweeper loop. It ticks approximately every second and runs the
// conditional expiration sweep. Call it in a dedicated goroutine.
//
// The provided context controls shutdown: when ctx is cancelled the loop exits
// cleanly (graceful shutdown). Use a cancellable context wired to SIGINT/SIGTERM.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	slog.Info("ttl sweeper started", "tick_interval", "1s")

	for {
		select {
		case <-ctx.Done():
			slog.Info("ttl sweeper stopped")
			return
		case <-ticker.C:
			n := s.RunOnce(ctx)
			if n > 0 {
				slog.Info("ttl sweeper: expired reservations", "count", n)
			}
		}
	}
}

// RunOnce runs a single expiration sweep and returns the number of rows expired.
// It is exported so tests can trigger a sweep deterministically without waiting
// for the ticker.
//
// SQL (research §3 pattern):
//
//	WITH expired AS (
//	  UPDATE reservations
//	     SET status = 'expired'
//	   WHERE status = 'pending' AND expires_at <= now()
//	  RETURNING id, item_id, quantity
//	)
//	UPDATE items i
//	   SET reserved = i.reserved - e.qty_sum
//	  FROM (SELECT item_id, SUM(quantity) AS qty_sum FROM expired GROUP BY item_id) e
//	 WHERE i.id = e.item_id
//	RETURNING i.id, i.reserved, i.total_stock - i.reserved AS available
//
// We batch all expired rows in a single statement so the stock decrement is
// atomic across all rows that expire in the same tick.
func (s *Sweeper) RunOnce(ctx context.Context) int {
	const sweepSQL = `
		WITH expired AS (
			UPDATE reservations
			   SET status = 'expired'
			 WHERE status = 'pending'
			   AND expires_at <= now()
			RETURNING id, item_id, quantity
		),
		item_deltas AS (
			SELECT item_id, SUM(quantity) AS qty_sum
			  FROM expired
			 GROUP BY item_id
		),
		updated_items AS (
			UPDATE items i
			   SET reserved = i.reserved - d.qty_sum
			  FROM item_deltas d
			 WHERE i.id = d.item_id
			RETURNING i.id AS item_id, i.reserved, i.total_stock - i.reserved AS available
		)
		SELECT e.item_id, u.reserved, u.available
		  FROM expired e
		  JOIN updated_items u ON u.item_id = e.item_id`

	rows, err := s.pool.Query(ctx, sweepSQL)
	if err != nil {
		// ctx cancelled on shutdown — log at debug level only.
		if ctx.Err() != nil {
			return 0
		}
		slog.Error("ttl sweeper: sweep query failed", "error", err)
		return 0
	}
	defer rows.Close()

	// Collect per-item events (deduplicated by itemID).
	type itemUpdate struct {
		reserved  int
		available int
	}
	seen := make(map[string]itemUpdate)
	total := 0

	for rows.Next() {
		var itemID string
		var reserved, available int
		if err := rows.Scan(&itemID, &reserved, &available); err != nil {
			slog.Error("ttl sweeper: scan row", "error", err)
			continue
		}
		seen[itemID] = itemUpdate{reserved: reserved, available: available}
		total++
	}
	if err := rows.Err(); err != nil && ctx.Err() == nil {
		slog.Error("ttl sweeper: rows error", "error", err)
	}

	// Publish one StockEvent per affected item (T038).
	if s.publisher != nil {
		for itemID, u := range seen {
			s.publisher.Publish(domain.StockEvent{
				Type:      domain.EventTypeExpired,
				ItemID:    itemID,
				Reserved:  u.reserved,
				Available: u.available,
			})
		}
	}

	return total
}
