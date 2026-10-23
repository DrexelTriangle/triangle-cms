package routes

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"server/internal/auth"
	"strings"
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
	Register(mux, conn, verifier, auth.OIDCConfig{}, nil, nil, nil, nil)

	public := []string{
		"/v1/articles",
		"/v1/articles/some-slug",
		"/v1/search",
		"/v1/authors",
		"/v1/authors/some-author",
		"/v1/settings/homepage-carousel",
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

func TestRegister_SettingsWriteEndpointsGated(t *testing.T) {
	verifier := oidc.NewVerifier("https://issuer.example", nil, &oidc.Config{
		ClientID:          "test",
		SkipClientIDCheck: true,
	})

	mux := http.NewServeMux()
	Register(mux, nil, verifier, auth.OIDCConfig{}, nil, nil, nil, nil)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPatch, "/v1/settings/site"},
		{http.MethodPatch, "/v1/settings/seo"},
		{http.MethodPatch, "/v1/settings/breaking-news"},
		{http.MethodPatch, "/v1/settings/homepage-carousel"},
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

func TestRegister_PublicRoute(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, nil, auth.OIDCConfig{}, nil, nil, nil, nil)

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
	Register(mux, nil, verifier, auth.OIDCConfig{}, nil, nil, nil, nil)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/media"},
		{http.MethodGet, "/v1/media/gallery"},
		{http.MethodGet, "/v1/media/1"},
		{http.MethodPost, "/v1/media"},
		{http.MethodPost, "/v1/media/fetch"},
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

// The quiz answer route has its own 60/min limiter (spec 5.1): a 30-question
// play must fit, and spending the quiz budget must not touch the poll's.
func TestRegister_QuizAnswerLimiterIsItsOwn(t *testing.T) {
	conn, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	mux := http.NewServeMux()
	Register(mux, conn, nil, auth.OIDCConfig{}, nil, nil, nil, nil)

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"definition_version":1,"question_index":0,"answer_index":0}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.7:1"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 1; i <= 60; i++ {
		if code := post("/v1/quiz/1/answers"); code == http.StatusTooManyRequests {
			t.Fatalf("post %d was rate limited; the quiz limit is 60 a minute", i)
		}
	}
	if code := post("/v1/quiz/1/answers"); code != http.StatusTooManyRequests {
		t.Fatalf("post 61: want 429, got %d", code)
	}
	if code := post("/v1/poll"); code == http.StatusTooManyRequests {
		t.Fatal("the poll limiter must not share the quiz bucket")
	}
}
