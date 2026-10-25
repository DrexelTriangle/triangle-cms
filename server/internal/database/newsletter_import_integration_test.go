package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// Skips unless CMS_TEST_DSN is set.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run LegacySubscriber -v
func legacyImportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping newsletter import integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx := context.Background()
	for _, table := range []string{"newsletter_deliveries", "newsletter_links", "newsletter_subscriber_lists", "newsletter_subscribers", "newsletter_lists", "newsletters"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := EnsureNewsletterTables(ctx, conn); err != nil {
		t.Fatalf("ensure newsletter tables: %v", err)
	}
	return conn
}

func runLegacyImport(t *testing.T, conn *sql.DB, subs []LegacySubscriber, commit bool) LegacyImportReport {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	report, err := ImportLegacySubscribers(ctx, tx, subs)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if commit {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return report
}

func countImportRows(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestLegacySubscriberImport_RerunsSafely(t *testing.T) {
	conn := legacyImportTestDB(t)
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	left := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	subs := []LegacySubscriber{
		{WPID: 10, Email: "a@drexel.edu", Name: "Ada", Token: "tok-a", Status: models.SubscriberStatusConfirmed, Lists: []int{1, 5}, SubscribedAt: created},
		{WPID: 11, Email: "b@gmail.com", Status: models.SubscriberStatusConfirmed, SubscribedAt: created},
		{WPID: 12, Email: "c@yahoo.com", Status: models.SubscriberStatusUnsubscribed, SubscribedAt: created, UnsubscribedAt: &left},
	}

	// A dry run writes nothing.
	if r := runLegacyImport(t, conn, subs, false); r.Inserted != 3 {
		t.Fatalf("dry run report: %+v", r)
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers"); n != 0 {
		t.Fatalf("dry run left %d rows behind", n)
	}

	if r := runLegacyImport(t, conn, subs, true); r.Inserted != 3 || r.Unchanged != 0 {
		t.Fatalf("first run: %+v", r)
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscriber_lists l JOIN newsletter_subscribers s ON s.id = l.subscriber_id WHERE s.legacy_wp_id = 10"); n != 2 {
		t.Fatalf("expected 2 list memberships, got %d", n)
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE legacy_wp_id = 12 AND unsubscribed_at = ?", left); n != 1 {
		t.Fatal("unsubscribe time not carried over")
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE legacy_wp_id = 10 AND legacy_wp_token = 'tok-a' AND CHAR_LENGTH(token) = 32"); n != 1 {
		t.Fatal("tokens not stored")
	}

	// Second run: b has since unsubscribed in WordPress; c is still
	// unsubscribed; a reader unsubscribed a in the CMS, and WordPress still
	// says confirmed, which must not resubscribe them. d is new, and e's
	// address already belongs to a subscriber who signed up through the CMS.
	if _, err := conn.Exec("UPDATE newsletter_subscribers SET status = 'unsubscribed' WHERE legacy_wp_id = 10"); err != nil {
		t.Fatal(err)
	}
	token, _ := NewSubscriberToken()
	if _, err := conn.Exec(`INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
		VALUES ('E@gmail.com', 'confirmed', ?, UTC_TIMESTAMP(), UTC_TIMESTAMP(), UTC_TIMESTAMP())`, token); err != nil {
		t.Fatal(err)
	}
	subs[1].Status = models.SubscriberStatusUnsubscribed
	subs = append(subs,
		LegacySubscriber{WPID: 13, Email: "d@drexel.edu", Status: models.SubscriberStatusConfirmed, SubscribedAt: created},
		LegacySubscriber{WPID: 14, Email: "e@gmail.com", Status: models.SubscriberStatusConfirmed, SubscribedAt: created},
	)

	r := runLegacyImport(t, conn, subs, true)
	if r.Inserted != 1 || r.Unsubscribed != 1 || r.Unchanged != 2 || len(r.EmailTaken) != 1 || r.EmailTaken[0] != 14 {
		t.Fatalf("second run: %+v", r)
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE legacy_wp_id = 10 AND status = 'unsubscribed'"); n != 1 {
		t.Fatal("an import resubscribed someone")
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE legacy_wp_id = 11 AND status = 'unsubscribed' AND unsubscribed_at IS NOT NULL"); n != 1 {
		t.Fatal("a WordPress unsubscribe was not carried over")
	}
	if n := countImportRows(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers"); n != 5 {
		t.Fatalf("expected 5 subscribers, got %d", n)
	}
}
