package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dfsalazar40/inventory-go/backend/internal/api"
	"github.com/dfsalazar40/inventory-go/backend/internal/config"
	"github.com/dfsalazar40/inventory-go/backend/internal/realtime"
	"github.com/dfsalazar40/inventory-go/backend/internal/store"
	"github.com/dfsalazar40/inventory-go/backend/internal/ttl"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()

	slog.Info("starting inventory server",
		"reservation_ttl", cfg.ReservationTTL,
		"reset_ttl_on_add", cfg.ResetTTLOnAdd,
	)

	// --- Database connection pool ---
	pool, err := connectDB(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// --- Schema migrations ---
	if err := runMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// --- WebSocket hub ---
	hub := realtime.NewHub()
	go hub.Run()

	// --- TTL sweeper: expires pending holds that have elapsed ---
	// The sweeper runs in a dedicated goroutine and stops when the server shuts down.
	sweeperCtx, sweeperCancel := context.WithCancel(context.Background())
	sweeper := ttl.NewSweeper(pool, hub, cfg.ReservationTTL)
	go sweeper.Run(sweeperCtx)

	// --- Store and handler wiring ---
	reservationStore := store.NewReservationStore(pool)
	itemStore := store.NewItemStore(pool)

	// Publisher is the hub: API handlers publish events after successful mutations;
	// the hub broadcasts them to all WebSocket clients. The store remains pure.
	reservationHandler := api.NewReservationHandler(reservationStore, cfg.ReservationTTL, hub).
		WithResetTTLOnAdd(cfg.ResetTTLOnAdd)
	itemHandler := api.NewItemHandler(itemStore)

	// --- Router ---
	// openapi.yaml lives next to the binary; in Docker it is copied from backend/openapi.yaml.
	router := api.NewRouter(reservationHandler, itemHandler, hub, "openapi.yaml")

	// Health check (no X-User-Id required — registered outside the protected group).
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// --- HTTP server ---
	srv := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// --- Graceful shutdown ---
	go func() {
		slog.Info("server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")

	// Stop the TTL sweeper first so no new expiration writes race with shutdown.
	sweeperCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server stopped")
}

// connectDB creates a pgx connection pool and verifies connectivity.
func connectDB(dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	slog.Info("database connected")
	return pool, nil
}

// runMigrations applies pending migrations from the migrations directory.
func runMigrations(dsn string) error {
	m, err := migrate.New("file://migrations", dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	slog.Info("migrations applied")
	return nil
}
