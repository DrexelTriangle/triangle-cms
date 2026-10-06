package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// Newsletter integration tests run against a real MariaDB. Point CMS_TEST_DSN at
// a disposable local database, for example:
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run Newsletter -p 1 -v

// Children first, so foreign keys never block the drop.
var newsletterTablesDropOrder = []string{
	"newsletter_campaign_lists",
	"newsletter_subscriber_lists",
	"newsletter_campaigns",
	"newsletter_subscribers",
	"newsletter_lists",
}

func newsletterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping newsletter database integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	for _, table := range newsletterTablesDropOrder {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := EnsureNewsletterTables(ctx, conn); err != nil {
		t.Fatalf("ensure newsletter tables: %v", err)
	}
	return conn
}

func mustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustCount(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestEnsureNewsletterTables_Idempotent(t *testing.T) {
	conn := newsletterTestDB(t)
	if err := EnsureNewsletterTables(context.Background(), conn); err != nil {
		t.Fatalf("second EnsureNewsletterTables: %v", err)
	}
	for _, table := range newsletterTablesDropOrder {
		n := mustCount(t, conn, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table)
		if n != 1 {
			t.Errorf("table %s: exists %d times, want 1", table, n)
		}
	}
}

func TestNewsletterLists_AutoIncrementStartsAt1000(t *testing.T) {
	conn := newsletterTestDB(t)
	mustExec(t, conn, "INSERT INTO newsletter_lists (name) VALUES ('Hand made')")
	var handMade int64
	if err := conn.QueryRow("SELECT id FROM newsletter_lists WHERE name = 'Hand made'").Scan(&handMade); err != nil {
		t.Fatalf("read hand-made list: %v", err)
	}
	if handMade < 1000 {
		t.Fatalf("hand-made list got id %d; ids below 1000 are reserved for the WordPress port", handMade)
	}

	mustExec(t, conn, "INSERT INTO newsletter_lists (id, name) VALUES (1, 'Drexel Students')")
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_lists WHERE id IN (1, ?)", handMade); n != 2 {
		t.Fatalf("expected both the ported id 1 and the hand-made list, got %d rows", n)
	}
}

func TestNewsletterJoinRows_CascadeOnDelete(t *testing.T) {
	conn := newsletterTestDB(t)
	mustExec(t, conn, "INSERT INTO newsletter_lists (id, name) VALUES (1, 'Students')")
	mustExec(t, conn, `INSERT INTO newsletter_subscribers (id, email, token, source, subscribed_at)
		VALUES (7, 'a@example.com', REPEAT('a', 43), 'admin', UTC_TIMESTAMP())`)
	mustExec(t, conn, "INSERT INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (7, 1)")
	mustExec(t, conn, "INSERT INTO newsletter_campaigns (id, subject, body_blocks) VALUES (9, 'Hello', '{}')")
	mustExec(t, conn, "INSERT INTO newsletter_campaign_lists (campaign_id, list_id) VALUES (9, 1)")

	mustExec(t, conn, "DELETE FROM newsletter_subscribers WHERE id = 7")
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscriber_lists WHERE subscriber_id = 7"); n != 0 {
		t.Errorf("subscriber memberships after delete = %d, want 0", n)
	}

	mustExec(t, conn, "DELETE FROM newsletter_campaigns WHERE id = 9")
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_campaign_lists WHERE campaign_id = 9"); n != 0 {
		t.Errorf("campaign targets after delete = %d, want 0", n)
	}
}

func TestNewsletterJoinRows_RejectUnknownParent(t *testing.T) {
	conn := newsletterTestDB(t)
	if _, err := conn.Exec("INSERT INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (12345, 54321)"); err == nil {
		t.Fatal("membership pointing at missing rows was accepted; foreign keys are not enforced")
	}
}
