package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"health", http.MethodGet, "/healthz", http.StatusOK},
		{"unknown path", http.MethodGet, "/missing", http.StatusNotFound},
		{"wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(nil, "").ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d", response.Code, tt.status)
			}
			if tt.status == http.StatusOK && response.Body.String() != "ok\n" {
				t.Errorf("body = %q, want %q", response.Body.String(), "ok\n")
			}
		})
	}
}
