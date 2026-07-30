package routes

import (
	"net/http"
	"net/http/httptest"
	"server/internal/auth"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestRegister_PublicRoute(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, nil, auth.OIDCConfig{})

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
			name:       "media accepts no PUT",
			method:     http.MethodPut,
			path:       "/v1/media",
			wantStatus: http.StatusMethodNotAllowed,
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

// TestRegister_MediaEndpointsGated proves the media library routes are wired and
// sit behind authentication. A request with no session cookie and no bearer
// token makes RequireAuth answer 401 before the handler (and therefore the DB)
// is ever reached, so a nil connection here is safe: 401 means "registered and
// gated", and 404 would mean the route is missing entirely.
func TestRegister_MediaEndpointsGated(t *testing.T) {
	verifier := oidc.NewVerifier("https://issuer.example", nil, &oidc.Config{
		ClientID:          "test",
		SkipClientIDCheck: true,
	})

	mux := http.NewServeMux()
	Register(mux, nil, verifier, auth.OIDCConfig{})

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/media"},
		{http.MethodGet, "/v1/media/gallery"},
		{http.MethodGet, "/v1/media/1"},
		{http.MethodPost, "/v1/media"},
		{http.MethodPost, "/v1/media/index"},
		{http.MethodPatch, "/v1/media/1"},
		{http.MethodDelete, "/v1/media/1"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
			}
		})
	}
}
