// Package store provides the data access layer for the inventory system.
// This file contains test helpers shared across store-level tests.
package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTestDSN = "postgres://inventory:inventory@localhost:5432/inventory?sslmode=disable"

// testDSN returns the DSN to use for integration tests, falling back to the default.
func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDSN
}

// migrationsPath returns the absolute path to the migrations directory.
// It walks up from the current working directory to find the backend root.
func migrationsPath() string {
	// Tests run from the package directory. The migrations dir is at
	// backend/migrations relative to the module root.
	// We use a relative path based on the test package location.
	return "file://../../migrations"
}

// newTestPool creates a pgx pool connected to the test database and applies
// all migrations. The pool is closed automatically when the test finishes.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := testDSN()

	// Apply migrations so the schema is always up-to-date.
	m, err := migrate.New(migrationsPath(), dsn)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("run migrations: %v", err)
	}
	m.Close()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping database: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

// createTestItem inserts an item with the given totalStock and returns its id.
// Use a unique name to avoid collisions between tests.
func createTestItem(t *testing.T, pool *pgxpool.Pool, name string, totalStock int) string {
	t.Helper()
	ctx := context.Background()

	id := fmt.Sprintf("test-item-%s", name)
	_, err := pool.Exec(ctx,
		`INSERT INTO items (id, name, total_stock, reserved)
		 VALUES ($1, $2, $3, 0)
		 ON CONFLICT (id) DO UPDATE SET total_stock = $3, reserved = 0`,
		id, name, totalStock,
	)
	if err != nil {
		t.Fatalf("createTestItem: %v", err)
	}

	t.Cleanup(func() {
		cleanupItem(pool, id)
	})

	return id
}

// cleanupItem deletes a test item and its reservations.
func cleanupItem(pool *pgxpool.Pool, itemID string) {
	ctx := context.Background()
	// Remove reservations first (FK), then idempotency_keys (cascades from reservations), then item.
	pool.Exec(ctx, `DELETE FROM reservations WHERE item_id = $1`, itemID) //nolint:errcheck
	pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, itemID)             //nolint:errcheck
}

// itemReserved reads the current `reserved` column for an item.
func itemReserved(t *testing.T, pool *pgxpool.Pool, itemID string) int {
	t.Helper()
	var reserved int
	err := pool.QueryRow(context.Background(),
		`SELECT reserved FROM items WHERE id = $1`, itemID,
	).Scan(&reserved)
	if err != nil {
		t.Fatalf("itemReserved: %v", err)
	}
	return reserved
}

// itemTotalStock reads the current `total_stock` column for an item.
func itemTotalStock(t *testing.T, pool *pgxpool.Pool, itemID string) int {
	t.Helper()
	var total int
	err := pool.QueryRow(context.Background(),
		`SELECT total_stock FROM items WHERE id = $1`, itemID,
	).Scan(&total)
	if err != nil {
		t.Fatalf("itemTotalStock: %v", err)
	}
	return total
}
