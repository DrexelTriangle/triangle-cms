package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	db "server/internal/database"

	_ "github.com/go-sql-driver/mysql"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run BreakingNewsSettings -v
func breakingNewsSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping breaking-news settings integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the shared table lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	for _, stmt := range []string{
		"DROP TABLE IF EXISTS articles",
		"DROP TABLE IF EXISTS cms_settings",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset schema (%s): %v", stmt, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title LONGTEXT,
			slug VARCHAR(255) NOT NULL UNIQUE,
			breaking_news BOOL,
			pub_date DATETIME NULL,
			archived_at DATETIME NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}
	if err := db.EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	db.ResetSettingsCache()

	return conn
}

func patchBreakingNews(t *testing.T, conn *sql.DB, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/settings/breaking-news", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	PatchBreakingNews(conn).ServeHTTP(rec, req)
	db.ResetSettingsCache()
	return rec
}

func decodeBreakingNews(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return payload
}

// window_hours is optional, and the settings screen is not the only thing that
// can toggle the banner. A plain enable/disable must not quietly reset how long
// flagged articles hold the homepage.
func TestBreakingNewsSettingsHTTP_OmittedWindowIsPreserved(t *testing.T) {
	conn := breakingNewsSettingsTestDB(t)

	if rec := patchBreakingNews(t, conn, `{"enabled":true,"text":"Campus closed","window_hours":6}`); rec.Code != http.StatusOK {
		t.Fatalf("set window: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec := patchBreakingNews(t, conn, `{"enabled":false,"text":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable banner: status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := decodeBreakingNews(t, rec)["window_hours"]; got != float64(6) {
		t.Errorf("window_hours = %v, want 6", got)
	}
}

// 0 is how an admin turns the limit back off, so it has to round-trip rather
// than fall back to some built-in duration.
func TestBreakingNewsSettingsHTTP_ZeroWindowClearsTheLimit(t *testing.T) {
	conn := breakingNewsSettingsTestDB(t)

	if rec := patchBreakingNews(t, conn, `{"enabled":false,"text":"","window_hours":6}`); rec.Code != http.StatusOK {
		t.Fatalf("set window: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec := patchBreakingNews(t, conn, `{"enabled":false,"text":"","window_hours":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear window: status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := decodeBreakingNews(t, rec)["window_hours"]; got != float64(0) {
		t.Errorf("window_hours = %v, want 0", got)
	}
}

func TestBreakingNewsSettingsHTTP_RejectsANegativeWindow(t *testing.T) {
	conn := breakingNewsSettingsTestDB(t)

	rec := patchBreakingNews(t, conn, `{"enabled":false,"text":"","window_hours":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// The admin saves a manual banner; an article is already holding the homepage.
// Echoing the request back would tell them the banner says something it does
// not, so the response is re-resolved.
func TestBreakingNewsSettingsHTTP_ResponseReflectsAnOverridingArticle(t *testing.T) {
	conn := breakingNewsSettingsTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (slug, title, breaking_news, pub_date) VALUES (?, ?, 1, UTC_TIMESTAMP() - INTERVAL 5 MINUTE)",
		"dragonfly", "Dragonfly headliner"); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := patchBreakingNews(t, conn, `{"enabled":true,"text":"Campus closed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	payload := decodeBreakingNews(t, rec)
	if payload["source"] != "article" {
		t.Errorf("source = %v, want %q", payload["source"], "article")
	}
	if payload["text"] != "Dragonfly headliner" {
		t.Errorf("text = %v, want the article headline", payload["text"])
	}
	manual, _ := payload["manual"].(map[string]any)
	if manual["text"] != "Campus closed" {
		t.Errorf("manual.text = %v, want the banner that was just saved", manual["text"])
	}
}

func TestBreakingNewsSettingsHTTP_RejectsAnEnabledBannerWithNoText(t *testing.T) {
	conn := breakingNewsSettingsTestDB(t)

	rec := patchBreakingNews(t, conn, `{"enabled":true,"text":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}
