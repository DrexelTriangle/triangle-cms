package routes

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"server/internal/auth"
	db "server/internal/database"

	"github.com/coreos/go-oidc/v3/oidc"
)

// The editorial routes editors may now call, and the author routes that stay
// admin-only. Each request carries a body that is not JSON and the author
// routes a slug that is not canonical, so a handler the gate lets through
// answers 400 before it reads or writes the database.
var (
	editorRoutes = []struct{ method, path string }{
		{http.MethodPost, "/v1/authors"},
		{http.MethodPost, "/v1/developing-stories"},
		{http.MethodPut, "/v1/developing-stories"},
		{http.MethodDelete, "/v1/developing-stories"},
	}
	adminOnlyAuthorRoutes = []struct{ method, path string }{
		{http.MethodPut, "/v1/authors/Not_Canonical"},
		{http.MethodPatch, "/v1/authors/Not_Canonical"},
		{http.MethodPatch, "/v1/authors/Not_Canonical/restore"},
		{http.MethodDelete, "/v1/authors/Not_Canonical"},
	}
)

func testVerifier() *oidc.IDTokenVerifier {
	return oidc.NewVerifier("https://issuer.example", nil, &oidc.Config{
		ClientID:          "test",
		SkipClientIDCheck: true,
	})
}

func serveAs(mux *http.ServeMux, method, path, sid string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// Runs in CI: without a session every one of these routes is registered and
// answers 401 before its handler, so a nil conn is safe.
func TestRegister_AuthorAndDevelopingStoryWritesRequireAuth(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, nil, testVerifier(), auth.OIDCConfig{}, nil, nil, nil, nil, "", nil)

	for _, tt := range append(editorRoutes, adminOnlyAuthorRoutes...) {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, ""); code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", code)
			}
		})
	}
}

// Role gates need a real session, which resolves through cms_sessions and
// cms_users. Skips unless CMS_TEST_DSN is set.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/routes/ -run RoleGates -v
func TestRegister_AuthorAndDevelopingStoryRoleGates(t *testing.T) {
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping route role-gate integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	if err := db.EnsureUsersTable(ctx, conn); err != nil {
		t.Fatalf("ensure cms_users: %v", err)
	}
	if err := db.EnsureSessionsTable(ctx, conn); err != nil {
		t.Fatalf("ensure cms_sessions: %v", err)
	}

	// A role outside the ENUM can't be stored under strict mode, so the
	// unknown role is written on one connection with sql_mode cleared. It
	// reads back as '', which stands in for any role that is neither editor
	// nor admin.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	newSession := func(role string) string {
		t.Helper()
		c, err := conn.Conn(ctx)
		if err != nil {
			t.Fatalf("conn: %v", err)
		}
		defer c.Close()
		if _, err := c.ExecContext(ctx, "SET SESSION sql_mode = ''"); err != nil {
			t.Fatalf("clear sql_mode: %v", err)
		}
		sub := "route-roles-" + role + "-" + suffix
		res, err := c.ExecContext(ctx,
			"INSERT INTO cms_users (sub, email, name, role) VALUES (?, ?, ?, ?)",
			sub, sub+"@example.test", sub, role)
		if err != nil {
			t.Fatalf("insert %q user: %v", role, err)
		}
		userID, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("user id: %v", err)
		}
		sid, err := db.GenerateSessionID()
		if err != nil {
			t.Fatalf("session id: %v", err)
		}
		if err := db.CreateSession(ctx, conn, db.Session{
			ID: sid, UserID: userID, AccessToken: "test", ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("create session: %v", err)
		}
		t.Cleanup(func() {
			conn.ExecContext(ctx, "DELETE FROM cms_sessions WHERE id = ?", sid)
			conn.ExecContext(ctx, "DELETE FROM cms_users WHERE id = ?", userID)
		})
		return sid
	}
	editor := newSession("editor")
	admin := newSession("admin")
	unknown := newSession("viewer")

	mux := http.NewServeMux()
	Register(mux, conn, testVerifier(), auth.OIDCConfig{}, nil, nil, nil, nil, "", nil)

	for _, tt := range editorRoutes {
		name := tt.method + " " + tt.path
		t.Run("editor "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, editor); code != http.StatusBadRequest {
				t.Fatalf("expected the gate to pass (handler 400), got %d", code)
			}
		})
		t.Run("admin "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, admin); code != http.StatusBadRequest {
				t.Fatalf("expected the gate to pass (handler 400), got %d", code)
			}
		})
		t.Run("unknown role "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, unknown); code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", code)
			}
		})
	}

	for _, tt := range adminOnlyAuthorRoutes {
		name := tt.method + " " + tt.path
		t.Run("editor "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, editor); code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", code)
			}
		})
		t.Run("unknown role "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, unknown); code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", code)
			}
		})
		t.Run("admin "+name, func(t *testing.T) {
			if code := serveAs(mux, tt.method, tt.path, admin); code != http.StatusBadRequest {
				t.Fatalf("expected the gate to pass (handler 400), got %d", code)
			}
		})
	}
}
