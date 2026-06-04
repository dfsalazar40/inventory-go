package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// DatabaseURL is the full DSN for the PostgreSQL connection pool.
	DatabaseURL string

	// ReservationTTL is how long a PENDING hold lives before expiring.
	// Controlled by RESERVATION_TTL env var (seconds, default 60).
	ReservationTTL time.Duration

	// ResetTTLOnAdd controls whether adding a new hold resets expires_at for the
	// user's existing pending holds on the same item.
	// Controlled by RESET_TTL_ON_ADD env var (bool, default true).
	ResetTTLOnAdd bool
}

// Load reads configuration from environment variables, applying defaults where needed.
func Load() Config {
	ttlSeconds := 60
	if v := os.Getenv("RESERVATION_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttlSeconds = n
		}
	}

	resetTTL := true
	if v := os.Getenv("RESET_TTL_ON_ADD"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			resetTTL = b
		}
	}

	return Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		ReservationTTL: time.Duration(ttlSeconds) * time.Second,
		ResetTTLOnAdd:  resetTTL,
	}
}
