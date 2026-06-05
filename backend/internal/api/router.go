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
//   - reset: handles POST /reset (nil skips the route)
//   - openapiPath: filesystem path to the openapi.yaml file to serve at GET /openapi.yaml
func NewRouter(reservations *ReservationHandler, items *ItemHandler, ws WSHandler, reset *ResetHandler, openapiPath string) *chi.Mux {
	r := chi.NewRouter()

	// Middleware stack: recovery → CORS → structured logger.
	// CORS runs before requireUserID so browser preflight (OPTIONS, which carries
	// no X-User-Id) is answered here instead of being rejected with 400.
	r.Use(middleware.Recoverer)
	r.Use(cors)
	r.Use(structuredLogger)

	// Public routes (no X-User-Id required): OpenAPI contract and the WebSocket.
	r.Get("/openapi.yaml", serveOpenAPI(openapiPath))

	// WebSocket is public on purpose: browsers cannot set custom headers
	// (X-User-Id) on the WS handshake, and the hub broadcasts global stock
	// events that carry no per-user identity. Keeping it behind requireUserID
	// would make every browser connection fail with 400.
	if ws != nil {
		r.Get("/ws", ws.ServeWS)
	}

	// Protected routes: every resource endpoint requires X-User-Id.
	r.Group(func(r chi.Router) {
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

		// Mount the reset endpoint when available.
		if reset != nil {
			r.Post("/reset", reset.Reset)
		}
	})

	return r
}

// cors adds permissive CORS headers so the browser frontend (served from a
// different origin, e.g. http://localhost:5173) can call the API. It reflects
// the request Origin and answers preflight OPTIONS requests directly.
// The custom headers X-User-Id and Idempotency-Key must be explicitly allowed
// or the browser blocks the actual request.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id, Idempotency-Key")
		w.Header().Set("Access-Control-Max-Age", "300")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
