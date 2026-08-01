package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// Every table the CMS creates itself must have a canonical definition on disk,
// because scripts/generate_wordpress_sql.py reads these same files to build the
// local seed. A rename here silently breaks the seed generator, which has no
// tests of its own, so this is the guard for both consumers.
func TestTableSchema_CoversEveryCMSOwnedTable(t *testing.T) {
	for _, table := range []string{"classifieds", "comments", "cms_poll_counts", "site_taxonomy"} {
		t.Run(table, func(t *testing.T) {
			got := TableSchema(table)

			if !strings.Contains(got, "CREATE TABLE IF NOT EXISTS "+table) {
				t.Fatalf("schema/%s.sql must CREATE TABLE IF NOT EXISTS %s, got:\n%s", table, table, got)
			}
			// Startup runs these against databases that already have the table,
			// so a plain CREATE TABLE would fail the boot.
			if strings.HasSuffix(strings.TrimSpace(got), ";") {
				t.Fatalf("schema/%s.sql must not end in a semicolon; it is executed as a single statement", table)
			}
		})
	}
}

func TestTableSchema_PollTableNameMatchesItsFile(t *testing.T) {
	// EnsurePollsTable looks the file up by PollTableName, so the constant and
	// the filename have to agree or startup panics.
	if got := TableSchema(PollTableName); !strings.Contains(got, PollTableName) {
		t.Fatalf("schema for %q does not define that table:\n%s", PollTableName, got)
	}
}

func TestTableSchema_PanicsOnUnknownTable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a table with no canonical schema")
		}
	}()

	TableSchema("no_such_table")
}

// TestArticleIDZeroSurvivesSeed proves the thing the NO_AUTO_VALUE_ON_ZERO
// preamble exists for: `articles` has a real row with id = 0, and without that
// mode MariaDB reads 0 in an AUTO_INCREMENT column as "assign the next value",
// renumbering it and orphaning every articles_authors and seo row that
// references it. Needs a real server — the behaviour is entirely server-side.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/seed_test?multiStatements=true' go test ./internal/database/ -run IDZero -v
func TestArticleIDZeroSurvivesSeed(t *testing.T) {
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping id=0 seed integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx := context.Background()

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS id_zero_probe"); err != nil {
		t.Fatalf("drop probe table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE id_zero_probe (id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, title VARCHAR(64))"); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS id_zero_probe")
	})

	// Exactly what scripts/generate_wordpress_sql.py prepends to each seed file.
	if _, err := conn.ExecContext(ctx, "SET sql_mode = CONCAT(@@sql_mode, ',NO_AUTO_VALUE_ON_ZERO')"); err != nil {
		t.Fatalf("set sql_mode: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO id_zero_probe (id, title) VALUES (0, 'legacy')"); err != nil {
		t.Fatalf("insert id=0: %v", err)
	}

	var minID int64
	if err := conn.QueryRowContext(ctx, "SELECT MIN(id) FROM id_zero_probe").Scan(&minID); err != nil {
		t.Fatalf("read back min id: %v", err)
	}
	if minID != 0 {
		t.Fatalf("id=0 row was renumbered to %d; the seed preamble is not taking effect", minID)
	}
}
