package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegister_PublicRoute(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, nil)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "get health route",
			method:     http.MethodGet,
			path:       "/v1/health",
			wantStatus: http.StatusOK,
		},
		{
			name:       "post health not allowed",
			method:     http.MethodPost,
			path:       "/v1/health",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "unknown route not found",
			method:     http.MethodGet,
			path:       "/unknown",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "media placeholder is explicit not implemented",
			method:     http.MethodGet,
			path:       "/v1/media",
			wantStatus: http.StatusNotImplemented,
		},
		{
			name:       "protected write route method not allowed without verifier",
			method:     http.MethodPost,
			path:       "/v1/articles",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, rec.Code)
			}
		})
	}
}
