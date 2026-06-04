package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the chi router with standard middleware.
// Handlers for specific routes are registered separately in each handler file.
func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	// Middleware stack: recovery → structured logger → user-id enforcement.
	r.Use(middleware.Recoverer)
	r.Use(structuredLogger)
	r.Use(requireUserID)

	return r
}

// structuredLogger is a middleware that logs every request using slog.
func structuredLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			slog.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		}()

		next.ServeHTTP(ww, r)
	})
}

// requireUserID enforces that every request carries the X-User-Id header.
// Returns 400 if the header is absent or empty.
func requireUserID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")
		if userID == "" {
			http.Error(w, `{"error":"validation_error","message":"X-User-Id header is required"}`, http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
