package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// WSHandler is the minimal interface the router needs to mount the /ws endpoint.
// Satisfied by *realtime.Hub. Declared here to avoid an import cycle.
type WSHandler interface {
	ServeWS(w http.ResponseWriter, r *http.Request)
}

// NewRouter builds the chi router with standard middleware.
// Pass handlers to mount; nil handlers are skipped (useful for incremental batch delivery).
//
//   - reservations: handles POST /reservations and POST /reservations/{id}/confirm
//   - items: handles GET /items (nil skips the route)
//   - ws: handles GET /ws WebSocket upgrade (nil skips the route)
func NewRouter(reservations *ReservationHandler, items *ItemHandler, ws WSHandler) *chi.Mux {
	r := chi.NewRouter()

	// Middleware stack: recovery → structured logger → user-id enforcement.
	r.Use(middleware.Recoverer)
	r.Use(structuredLogger)
	r.Use(requireUserID)

	// Mount reservation routes when available.
	if reservations != nil {
		r.Post("/reservations", reservations.Reserve)
		r.Get("/reservations", reservations.ListReservations)
		r.Post("/reservations/{id}/confirm", reservations.ConfirmReservation)
		r.Delete("/reservations/{id}", reservations.ReleaseReservation)
	}

	// Mount item routes when available.
	if items != nil {
		r.Get("/items", items.List)
	}

	// Mount WebSocket endpoint when available.
	if ws != nil {
		r.Get("/ws", ws.ServeWS)
	}

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
