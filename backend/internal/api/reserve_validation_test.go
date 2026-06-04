package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dfsalazar40/inventory-go/backend/internal/domain"
)

// stubReservationStore is a minimal in-process stub for validation tests.
// It tracks whether Reserve was called so we can assert no state change.
type stubReservationStore struct {
	called bool
}

func (s *stubReservationStore) Reserve(_ context.Context, _ ReserveParams) (*domain.Reservation, error) {
	s.called = true
	return nil, nil
}

func (s *stubReservationStore) Confirm(_ context.Context, _ string) (*domain.Reservation, error) {
	return nil, nil
}

// newReserveHandler builds the POST /reservations handler under test.
func newReserveHandler(store ReservationStorer) http.HandlerFunc {
	h := &ReservationHandler{store: store}
	return h.Reserve
}

// TestReserve_Validation tests that invalid reserve requests are rejected
// at the handler level with a 400 status and the correct error body, and that
// the store is never called (no state change).
//
// T017 [US1]: validation
func TestReserve_Validation(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "quantity zero",
			body:       `{"itemId":"item-1","quantity":0}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
		{
			name:       "quantity negative",
			body:       `{"itemId":"item-1","quantity":-5}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
		{
			name:       "missing itemId",
			body:       `{"quantity":1}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
		{
			name:       "missing quantity",
			body:       `{"itemId":"item-1"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
		{
			name:       "non-integer quantity (malformed JSON)",
			body:       `{"itemId":"item-1","quantity":"one"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
		{
			name:       "empty body",
			body:       ``,
			wantStatus: http.StatusBadRequest,
			wantError:  "validation_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubReservationStore{}
			handler := newReserveHandler(stub)

			req := httptest.NewRequest(http.MethodPost, "/reservations",
				strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", "test-user")
			req.Header.Set("Idempotency-Key", "test-idempotency-key")

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("want status %d, got %d (body: %s)", tc.wantStatus, w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response body: %v", err)
			}
			if got, ok := resp["error"]; !ok || got != tc.wantError {
				t.Errorf("want error=%q, got %v", tc.wantError, resp)
			}
			if _, ok := resp["message"]; !ok {
				t.Errorf("response missing 'message' field: %v", resp)
			}

			// Store MUST NOT be called on a validation error.
			if stub.called {
				t.Error("store.Reserve was called despite a validation error — state change occurred")
			}
		})
	}
}
