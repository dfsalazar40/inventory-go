package api

// T048 [Polish] — GET /openapi.yaml handler.
//
// Serves the OpenAPI contract file so tooling (Swagger UI, Postman, etc.) can
// fetch it directly from the running backend. The file is embedded at build
// time via the path passed to NewRouter, keeping it in sync with the handlers.

import (
	"net/http"
	"os"
)

// serveOpenAPI returns an http.HandlerFunc that serves the YAML file at path
// with Content-Type: application/yaml. If the file cannot be read at startup
// the handler returns 503 so the failure is observable.
func serveOpenAPI(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "openapi.yaml unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}
}
