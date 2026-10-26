package newsletter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"net/textproto"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/mailer"
	"server/internal/models"
)

// The worker's guarantees (one sender per issue, nothing sent twice) rest on
// row locks and leases, which only a real MariaDB exercises. Skips unless
// CMS_TEST_DSN is set; run with -p 1, other packages share these tables.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/newsletter/ -v
func workerTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping newsletter worker integration test")
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
	for _, table := range []string{"newsletter_deliveries", "newsletter_links", "newsletter_subscriber_lists", "newsletter_subscribers", "newsletter_lists", "newsletters"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := db.EnsureNewsletterTables(ctx, conn); err != nil {
		t.Fatalf("ensure newsletter tables: %v", err)
	}
	return conn
}

func addSubscriber(t *testing.T, conn *sql.DB, email, status string) int64 {
	t.Helper()
	token, err := db.NewSubscriberToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	res, err := conn.Exec(`
		INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
	`, email, status, token, now, now, now)
	if err != nil {
		t.Fatalf("insert subscriber: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// scheduleIssue adds an issue already due by ago.
func scheduleIssue(t *testing.T, conn *sql.DB, ago time.Duration) int64 {
	t.Helper()
	issue, err := db.InsertNewsletter(context.Background(), conn, models.NewsletterInput{
		Subject:   "To The Point",
		EmailHTML: `<html><body><a href="https://www.thetriangle.org/news">News</a> <a href="{{unsubscribe_url}}">Unsubscribe</a></body></html>`,
	}, "test")
	if err != nil {
		t.Fatalf("insert newsletter: %v", err)
	}
	if _, err := conn.Exec("UPDATE newsletters SET status = ?, scheduled_for = ? WHERE id = ?",
		models.NewsletterStatusScheduled, time.Now().UTC().Add(-ago), issue.ID); err != nil {
		t.Fatalf("schedule newsletter: %v", err)
	}
	return issue.ID
}

type fakeSender struct {
	mu    sync.Mutex
	sent  []*mailer.Message
	delay time.Duration
	// fail, when set, decides each send's error by recipient.
	fail func(to string) error
}

func (s *fakeSender) Send(_ context.Context, m *mailer.Message) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		if err := s.fail(m.To.Address); err != nil {
			return err
		}
	}
	s.sent = append(s.sent, m)
	return nil
}

func (s *fakeSender) recipients() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	got := map[string]int{}
	for _, m := range s.sent {
		got[m.To.Address]++
	}
	return got
}

func newWorker(conn *sql.DB, sender mailer.Sender, owner string) *Worker {
	return &Worker{
		Conn:     conn,
		Composer: &mailer.Newsletter{From: &mail.Address{Address: "notify@thetriangle.org"}, PublicURL: "https://cms.example.org"},
		Sender:   sender,
		Rate:     1000,
		Owner:    owner,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func issueState(t *testing.T, conn *sql.DB, id int64) (status string, recipients int) {
	t.Helper()
	if err := conn.QueryRow("SELECT status, recipient_count FROM newsletters WHERE id = ?", id).Scan(&status, &recipients); err != nil {
		t.Fatalf("read newsletter: %v", err)
	}
	return status, recipients
}

func deliveryStatuses(t *testing.T, conn *sql.DB, id int64) map[string]string {
	t.Helper()
	rows, err := conn.Query(`
		SELECT s.email, d.status FROM newsletter_deliveries d JOIN newsletter_subscribers s ON s.id = d.subscriber_id
		WHERE d.newsletter_id = ? AND d.is_test = 0
	`, id)
	if err != nil {
		t.Fatalf("read deliveries: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var email, status string
		if err := rows.Scan(&email, &status); err != nil {
			t.Fatal(err)
		}
		got[email] = status
	}
	return got
}

func TestWorker_SendsADueIssueToEveryConfirmedSubscriber(t *testing.T) {
	conn := workerTestDB(t)
	for i := range 3 {
		addSubscriber(t, conn, fmt.Sprintf("reader%d@example.org", i), models.SubscriberStatusConfirmed)
	}
	addSubscriber(t, conn, "gone@example.org", models.SubscriberStatusUnsubscribed)
	future := scheduleIssue(t, conn, -time.Hour)
	id := scheduleIssue(t, conn, time.Minute)

	sender := &fakeSender{}
	if err := newWorker(conn, sender, "a").Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	got := sender.recipients()
	if len(got) != 3 || got["gone@example.org"] != 0 {
		t.Fatalf("expected one copy each to the 3 confirmed readers, got %v", got)
	}
	for addr, n := range got {
		if n != 1 {
			t.Fatalf("%s got %d copies", addr, n)
		}
	}
	unsubscribes := map[string]bool{}
	for _, m := range sender.sent {
		if m.Subject != "To The Point" || unsubscribes[m.UnsubscribeURL] {
			t.Fatalf("expected the real subject and a personal unsubscribe link, got %q %q", m.Subject, m.UnsubscribeURL)
		}
		unsubscribes[m.UnsubscribeURL] = true
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 3 {
		t.Fatalf("expected sent to 3, got %s to %d", status, recipients)
	}
	if status, _ := issueState(t, conn, future); status != models.NewsletterStatusScheduled {
		t.Fatalf("an issue scheduled for later must wait, got %s", status)
	}

	// Nothing more goes out on later ticks.
	if err := newWorker(conn, sender, "a").Tick(context.Background()); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("a sent issue was sent again: %d messages", len(sender.sent))
	}
}

func TestWorker_TwoSlotsNeverSendTheSameCopy(t *testing.T) {
	conn := workerTestDB(t)
	for i := range 20 {
		addSubscriber(t, conn, fmt.Sprintf("reader%d@example.org", i), models.SubscriberStatusConfirmed)
	}
	id := scheduleIssue(t, conn, time.Minute)

	sender := &fakeSender{delay: 5 * time.Millisecond}
	var wg sync.WaitGroup
	for _, owner := range []string{"blue", "green"} {
		wg.Go(func() {
			w := newWorker(conn, sender, owner)
			for range 3 {
				if err := w.Tick(context.Background()); err != nil {
					t.Errorf("%s tick: %v", owner, err)
				}
			}
		})
	}
	wg.Wait()

	got := sender.recipients()
	if len(got) != 20 {
		t.Fatalf("expected all 20 readers, got %d", len(got))
	}
	for addr, n := range got {
		if n != 1 {
			t.Fatalf("%s got %d copies", addr, n)
		}
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 20 {
		t.Fatalf("expected sent to 20, got %s to %d", status, recipients)
	}
}

func TestWorker_PausesAtTheDailyCapAndResumes(t *testing.T) {
	conn := workerTestDB(t)
	for i := range 5 {
		addSubscriber(t, conn, fmt.Sprintf("reader%d@example.org", i), models.SubscriberStatusConfirmed)
	}
	id := scheduleIssue(t, conn, time.Minute)
	// A test copy sent earlier today counts against the mailbox too.
	deliveryID, _, err := db.CreateTestDelivery(context.Background(), conn, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishDelivery(context.Background(), conn, deliveryID, true); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	w := newWorker(conn, sender, "a")
	w.DailyCap = 3
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("expected 2 sends under a cap of 3 with 1 test copy out, got %d", len(sender.sent))
	}
	if status, _ := issueState(t, conn, id); status != models.NewsletterStatusSending {
		t.Fatalf("a paused issue stays sending, got %s", status)
	}

	w.DailyCap = 100
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(sender.recipients()) != 5 || len(sender.sent) != 5 {
		t.Fatalf("expected the other 3 after the cap lifted, got %v", sender.recipients())
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 5 {
		t.Fatalf("expected sent to 5, got %s to %d", status, recipients)
	}
}

func TestWorker_RetriesTransientFailuresAndGivesUpOnPermanentOnes(t *testing.T) {
	conn := workerTestDB(t)
	addSubscriber(t, conn, "ok@example.org", models.SubscriberStatusConfirmed)
	addSubscriber(t, conn, "flaky@example.org", models.SubscriberStatusConfirmed)
	addSubscriber(t, conn, "bad@example.org", models.SubscriberStatusConfirmed)
	id := scheduleIssue(t, conn, time.Minute)

	flaky := 0
	sender := &fakeSender{fail: func(to string) error {
		switch to {
		case "bad@example.org":
			return fmt.Errorf("smtp RCPT TO: %w", &textproto.Error{Code: 550, Msg: "no such user"})
		case "flaky@example.org":
			if flaky++; flaky == 1 {
				return errors.New("smtp dial: connection refused")
			}
		}
		return nil
	}}
	w := newWorker(conn, sender, "a")
	for range 3 {
		if err := w.Tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}

	got := deliveryStatuses(t, conn, id)
	want := map[string]string{"ok@example.org": "sent", "flaky@example.org": "sent", "bad@example.org": "failed"}
	for addr, status := range want {
		if got[addr] != status {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
	if flaky != 2 {
		t.Fatalf("expected the flaky address to be tried twice, got %d", flaky)
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 2 {
		t.Fatalf("expected sent to 2 (the failure is not a recipient), got %s to %d", status, recipients)
	}
}

func TestWorker_NeverResendsACopyInterruptedMidSend(t *testing.T) {
	conn := workerTestDB(t)
	addSubscriber(t, conn, "first@example.org", models.SubscriberStatusConfirmed)
	addSubscriber(t, conn, "second@example.org", models.SubscriberStatusConfirmed)
	id := scheduleIssue(t, conn, time.Minute)
	ctx := context.Background()

	// A slot starts the issue, claims the first copy and dies with it in hand.
	if _, err := db.StartDueNewsletter(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.AcquireNewsletterSend(ctx, conn, "dead", time.Minute); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if _, outcome, err := db.ClaimNewsletterDelivery(ctx, conn, id, "dead", time.Minute, 100); err != nil || outcome != db.DeliveryClaimed {
		t.Fatalf("claim: %v %v", outcome, err)
	}

	sender := &fakeSender{}
	survivor := newWorker(conn, sender, "alive")
	// The lease is still live: the other slot must keep its hands off.
	if err := survivor.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent under another slot's live lease: %v", sender.recipients())
	}

	if _, err := conn.Exec("UPDATE newsletters SET send_lease_until = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 1 SECOND) WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	if err := survivor.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got := sender.recipients()
	if len(got) != 1 || got["second@example.org"] != 1 {
		t.Fatalf("expected only the copy nobody had touched, got %v", got)
	}
	if statuses := deliveryStatuses(t, conn, id); statuses["first@example.org"] != "failed" {
		t.Fatalf("the interrupted copy should be failed, got %v", statuses)
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 1 {
		t.Fatalf("expected sent to 1, got %s to %d", status, recipients)
	}
}

func TestWorker_SkipsReadersWhoUnsubscribeMidSend(t *testing.T) {
	conn := workerTestDB(t)
	addSubscriber(t, conn, "stays@example.org", models.SubscriberStatusConfirmed)
	leaver := addSubscriber(t, conn, "leaves@example.org", models.SubscriberStatusConfirmed)
	erased := addSubscriber(t, conn, "erased@example.org", models.SubscriberStatusConfirmed)
	id := scheduleIssue(t, conn, time.Minute)
	ctx := context.Background()

	if _, err := db.StartDueNewsletter(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec("UPDATE newsletter_subscribers SET status = ? WHERE id = ?", models.SubscriberStatusUnsubscribed, leaver); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec("DELETE FROM newsletter_subscribers WHERE id = ?", erased); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	if err := newWorker(conn, sender, "a").Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sender.recipients(); len(got) != 1 || got["stays@example.org"] != 1 {
		t.Fatalf("expected only the reader still subscribed, got %v", got)
	}
	if status, recipients := issueState(t, conn, id); status != models.NewsletterStatusSent || recipients != 1 {
		t.Fatalf("expected sent to 1, got %s to %d", status, recipients)
	}
}

func TestWorker_LeavesAStaleIssueForAnEditor(t *testing.T) {
	conn := workerTestDB(t)
	addSubscriber(t, conn, "reader@example.org", models.SubscriberStatusConfirmed)
	id := scheduleIssue(t, conn, db.MaxNewsletterLateness+time.Hour)

	sender := &fakeSender{}
	if err := newWorker(conn, sender, "a").Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatal("sent an issue that was long overdue")
	}
	if status, _ := issueState(t, conn, id); status != models.NewsletterStatusScheduled {
		t.Fatalf("expected the stale issue left scheduled, got %s", status)
	}
}

func TestWorker_EditorsCannotChangeAnIssueBeingSent(t *testing.T) {
	conn := workerTestDB(t)
	addSubscriber(t, conn, "reader@example.org", models.SubscriberStatusConfirmed)
	id := scheduleIssue(t, conn, time.Minute)
	ctx := context.Background()
	if _, err := db.StartDueNewsletter(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnscheduleNewsletter(ctx, conn, id, "editor"); !errors.Is(err, db.ErrNewsletterLocked) {
		t.Fatalf("expected unscheduling a sending issue to be refused, got %v", err)
	}
}
