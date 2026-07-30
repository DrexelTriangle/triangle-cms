package routes

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"server/internal/auth"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	_ "github.com/go-sql-driver/mysql"
)

// TestRegister_ReadEndpointsPublicWithVerifier proves that content read
// endpoints are reachable without authentication even when an OIDC verifier is
// configured (verifier != nil), while privileged endpoints stay gated. A
// missing token makes RequireAuth return 401 before it ever touches the DB or
// network, so a 401 means "gated" and any other status means the request
// reached the handler. The DB points at a dead address so public handlers fail
// fast (non-401) rather than requiring a live database.
func TestRegister_ReadEndpointsPublicWithVerifier(t *testing.T) {
	verifier := oidc.NewVerifier("https://issuer.example", nil, &oidc.Config{
		ClientID:          "test",
		SkipClientIDCheck: true,
	})
	conn, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	mux := http.NewServeMux()
	Register(mux, conn, verifier, auth.OIDCConfig{})

	public := []string{
		"/v1/articles",
		"/v1/articles/some-slug",
		"/v1/search",
		"/v1/authors",
		"/v1/authors/some-author",
		"/v1/taxonomy",
	}
	for _, path := range public {
		t.Run("public "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("expected %s to be public, got 401", path)
			}
		})
	}

	gated := []string{
		"/v1/activity",
		"/v1/users",
		"/v1/users/me",
		"/v1/seo/audit",
		"/v1/media",
	}
	for _, path := range gated {
		t.Run("gated "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected %s to require auth (401), got %d", path, rec.Code)
			}
		})
	}
}

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
