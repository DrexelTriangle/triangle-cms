package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// The unique key is the part a fake would get wrong: it has to fit an index
// prefix on a real server, and it compares sources byte-for-byte (ascii_bin),
// so a manual row can never be silently merged into a generated one that only
// differs in case.
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run LegacyRedirects -v
func legacyRedirectsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping legacy redirects integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS legacy_redirects"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := EnsureLegacyRedirectsTable(ctx, conn); err != nil {
		t.Fatalf("EnsureLegacyRedirectsTable: %v", err)
	}
	// Startup runs it on every boot, against a table that already exists.
	if err := EnsureLegacyRedirectsTable(ctx, conn); err != nil {
		t.Fatalf("EnsureLegacyRedirectsTable on an existing table: %v", err)
	}
	return conn
}

func TestLegacyRedirects_ListIsOrderedAndComplete(t *testing.T) {
	conn := legacyRedirectsTestDB(t)
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO legacy_redirects (source_kind, source, target, status, origin) VALUES
		('slug', 'golf%e2%80%99s-season-winds-down', '/article/golf-e2-80-99s-season-winds-down', 301, 'wp_post'),
		('path', '/sports/golf%e2%80%99s-season-winds-down/', '/article/golf-e2-80-99s-season-winds-down', 301, 'wp_post'),
		('post_id', '67825', '/article/golf-e2-80-99s-season-winds-down', 301, 'wp_post'),
		('path', '/xmlrpc.php', '', 410, 'manual')
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := ListLegacyRedirects(ctx, conn)
	if err != nil {
		t.Fatalf("ListLegacyRedirects: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 redirects, got %d: %+v", len(got), got)
	}
	wantOrder := []string{"/sports/golf%e2%80%99s-season-winds-down/", "/xmlrpc.php", "67825", "golf%e2%80%99s-season-winds-down"}
	for i, want := range wantOrder {
		if got[i].Source != want {
			t.Errorf("redirect %d: expected source %q, got %q", i, want, got[i].Source)
		}
	}
	if got[1].Status != 410 || got[1].Target != "" {
		t.Errorf("expected the 410 row to keep an empty target, got %+v", got[1])
	}
}

func TestLegacyRedirects_SourcesAreCaseSensitive(t *testing.T) {
	conn := legacyRedirectsTestDB(t)
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO legacy_redirects (source_kind, source, target, origin) VALUES
		('slug', 'ghost-world', '/article/ghost-world-7983', 'wp_post'),
		('slug', 'Ghost-World', '/article/ghost-world', 'manual')
	`); err != nil {
		t.Fatalf("expected sources differing only in case to coexist: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO legacy_redirects (source_kind, source, target, origin)
		VALUES ('slug', 'ghost-world', '/article/elsewhere', 'wp_post')
	`); err == nil {
		t.Fatal("expected a duplicate (source_kind, source) to be rejected")
	}
}
