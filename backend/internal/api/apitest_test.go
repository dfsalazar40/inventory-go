package api

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

const defaultAPITestDSN = "postgres://inventory:inventory@localhost:5432/inventory?sslmode=disable"

func apiTestDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultAPITestDSN
}

// newAPITestPool creates a pgxpool connected to the test DB, applies migrations,
// and closes the pool at the end of the test.
func newAPITestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := apiTestDSN()

	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("run migrations: %v", err)
	}
	m.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.MaxConns = 110

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createAPITestItem inserts a test item and registers cleanup.
func createAPITestItem(t *testing.T, pool *pgxpool.Pool, name string, totalStock int) string {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("api-test-item-%s", name)
	_, err := pool.Exec(ctx,
		`INSERT INTO items (id, name, total_stock, reserved)
		 VALUES ($1, $2, $3, 0)
		 ON CONFLICT (id) DO UPDATE SET total_stock = $3, reserved = 0`,
		id, name, totalStock,
	)
	if err != nil {
		t.Fatalf("createAPITestItem: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM reservations WHERE item_id = $1`, id)   //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM items WHERE id = $1`, id)               //nolint:errcheck
	})
	return id
}
