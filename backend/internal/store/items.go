// Package store implements database access for the inventory system.
package store

import (
	"context"
	"fmt"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ItemStore handles read operations for the items table.
type ItemStore struct {
	pool *pgxpool.Pool
}

// NewItemStore creates a new ItemStore backed by the given pool.
func NewItemStore(pool *pgxpool.Pool) *ItemStore {
	return &ItemStore{pool: pool}
}

// ListItems returns all items with derived available = GREATEST(total_stock - reserved, 0).
// The GREATEST cap ensures available is never negative even if a data anomaly occurred.
func (s *ItemStore) ListItems(ctx context.Context) ([]domain.Item, error) {
	const q = `
		SELECT id, name, total_stock, reserved,
		       GREATEST(total_stock - reserved, 0) AS available,
		       created_at
		  FROM items
		 ORDER BY name`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list items query: %w", err)
	}
	defer rows.Close()

	var items []domain.Item
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(
			&it.ID,
			&it.Name,
			&it.TotalStock,
			&it.Reserved,
			&it.Available,
			&it.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	if items == nil {
		items = []domain.Item{}
	}
	return items, nil
}
