package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"server/internal/models"
)

// Skips unless CMS_TEST_DSN is set; run with -p 1, other packages share these
// tables.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run Campaign -v

type statsFixture struct {
	t    *testing.T
	conn *sql.DB
	n    int
}

func newStatsFixture(t *testing.T) *statsFixture {
	return &statsFixture{t: t, conn: legacyImportTestDB(t)}
}

func (f *statsFixture) subscriber(status string) (id int64, token string) {
	f.t.Helper()
	f.n++
	token, err := NewSubscriberToken()
	if err != nil {
		f.t.Fatal(err)
	}
	now := time.Now().UTC()
	res, err := f.conn.Exec(`INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, fmt.Sprintf("reader%d@example.org", f.n), status, token, now, now, now)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ = res.LastInsertId()
	return id, token
}

// issue adds a sent issue. version is its tracking_version: 0 for one sent
// before detailed tracking.
func (f *statsFixture) issue(subject string, sentAt time.Time, version int) int64 {
	f.t.Helper()
	n, err := InsertNewsletter(context.Background(), f.conn, models.NewsletterInput{Subject: subject, EmailHTML: "<p>x</p>"}, "test")
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.conn.Exec("UPDATE newsletters SET status = 'sent', sent_at = ?, tracking_version = ? WHERE id = ?",
		sentAt, version, n.ID); err != nil {
		f.t.Fatal(err)
	}
	return n.ID
}

// delivery adds a real copy of issue to subscriber in the given final state.
func (f *statsFixture) delivery(issue, subscriber int64, status string, version int, errMsg any) string {
	f.t.Helper()
	token, err := NewSubscriberToken()
	if err != nil {
		f.t.Fatal(err)
	}
	now := time.Now().UTC()
	var sentAt any
	if status == DeliveryStatusSent {
		sentAt = now
	}
	if _, err := f.conn.Exec(`INSERT INTO newsletter_deliveries
		(newsletter_id, subscriber_id, token, status, sent_at, tracking_version, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		issue, subscriber, token, status, sentAt, version, errMsg, now); err != nil {
		f.t.Fatal(err)
	}
	return token
}

func (f *statsFixture) stats(id int64) models.NewsletterCampaignStats {
	f.t.Helper()
	s, err := GetNewsletterCampaignStats(context.Background(), f.conn, id)
	if err != nil {
		f.t.Fatalf("campaign stats: %v", err)
	}
	return s
}

func (f *statsFixture) click(token string, link int64) {
	f.t.Helper()
	if _, err := RecordNewsletterClick(context.Background(), f.conn, token, link); err != nil {
		f.t.Fatalf("click: %v", err)
	}
}

// Startup adds the new columns and table to a database that has the tables as
// they first shipped, keeping every row, and existing issues read as sent
// before detailed tracking.
func TestEnsureNewsletterTables_MigratesExistingTables(t *testing.T) {
	conn := legacyImportTestDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		"DROP TABLE newsletter_delivery_links",
		"ALTER TABLE newsletters DROP COLUMN tracking_version",
		"ALTER TABLE newsletter_deliveries DROP COLUMN tracking_version, DROP COLUMN unsubscribed_at",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	old, err := InsertNewsletter(ctx, conn, models.NewsletterInput{Subject: "Before", EmailHTML: "<p>x</p>"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO newsletter_deliveries (newsletter_id, token, status, created_at)
		VALUES (?, '0123456789abcdef0123456789abcdef', 'sent', UTC_TIMESTAMP())`, old.ID); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := EnsureNewsletterTables(ctx, conn); err != nil {
			t.Fatalf("ensure: %v", err)
		}
	}

	var issueVersion, deliveryVersion int
	var unsubscribedAt sql.NullTime
	if err := conn.QueryRow("SELECT tracking_version FROM newsletters WHERE id = ?", old.ID).Scan(&issueVersion); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow("SELECT tracking_version, unsubscribed_at FROM newsletter_deliveries WHERE newsletter_id = ?", old.ID).
		Scan(&deliveryVersion, &unsubscribedAt); err != nil {
		t.Fatal(err)
	}
	if issueVersion != 0 || deliveryVersion != 0 || unsubscribedAt.Valid {
		t.Fatalf("existing rows must read as untracked: issue %d delivery %d unsubscribed %v", issueVersion, deliveryVersion, unsubscribedAt)
	}
	if _, err := conn.Exec("SELECT delivery_id, link_id, click_count, first_clicked_at, last_clicked_at FROM newsletter_delivery_links"); err != nil {
		t.Fatalf("newsletter_delivery_links: %v", err)
	}
}

func TestCampaignStats_DetailedCampaign(t *testing.T) {
	f := newStatsFixture(t)
	ctx := context.Background()
	sentAt := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)
	issue := f.issue("To The Point", sentAt, NewsletterTrackingVersion)

	var tokens []string
	var subs []int64
	for range 4 {
		id, _ := f.subscriber(models.SubscriberStatusConfirmed)
		subs = append(subs, id)
		tokens = append(tokens, f.delivery(issue, id, DeliveryStatusSent, NewsletterTrackingVersion, nil))
	}
	failedSub, _ := f.subscriber(models.SubscriberStatusConfirmed)
	f.delivery(issue, failedSub, DeliveryStatusFailed, 0, "550 5.1.1 <reader5@example.org>: no such user")
	otherFailed, _ := f.subscriber(models.SubscriberStatusConfirmed)
	f.delivery(issue, otherFailed, DeliveryStatusFailed, 0, "550 5.1.1 <someone@example.org>: no such user")
	skipped, _ := f.subscriber(models.SubscriberStatusUnsubscribed)
	f.delivery(issue, skipped, DeliveryStatusSkipped, 0, nil)
	// A test copy is tracked but never counted.
	testID, testToken, err := CreateTestDelivery(ctx, f.conn, issue)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinishDelivery(ctx, f.conn, testID, true); err != nil {
		t.Fatal(err)
	}

	links, err := EnsureNewsletterLinks(ctx, f.conn, issue, []string{"https://www.example.org/a", "https://www.example.org/b", "https://x.com/triangle"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := links["https://www.example.org/a"], links["https://www.example.org/b"]
	f.click(tokens[0], a)
	f.click(tokens[0], a)
	f.click(tokens[0], b)
	f.click(tokens[1], a)
	f.click(testToken, a)
	if err := RecordNewsletterOpen(ctx, f.conn, tokens[2]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UnsubscribeByKey(ctx, f.conn, SubscriberKey{Token: subscriberToken(t, f.conn, subs[1]), Delivery: tokens[1]}, time.Now()); err != nil {
		t.Fatal(err)
	}

	s := f.stats(issue)
	if s.ID != issue || s.Subject != "To The Point" || s.Status != models.NewsletterStatusSent || s.SentAt == nil || !s.SentAt.Equal(sentAt) {
		t.Errorf("header: %+v", s.NewsletterCampaignSummary)
	}
	if !s.DetailedTracking {
		t.Error("a campaign sent with detailed tracking should say so")
	}
	if want := (models.NewsletterCampaignRecipients{Queued: 7, Sent: 4, Failed: 2, Skipped: 1}); s.Recipients != want {
		t.Errorf("recipients %+v, want %+v", s.Recipients, want)
	}
	if s.UniqueOpens != 3 || s.UniqueClickers != 2 || s.TotalClicks != 4 {
		t.Errorf("opens %d clickers %d clicks %d, want 3, 2, 4 (a click is an open; the test copy is not counted)", s.UniqueOpens, s.UniqueClickers, s.TotalClicks)
	}
	if s.Unsubscribes == nil || *s.Unsubscribes != 1 {
		t.Errorf("unsubscribes %v, want 1", s.Unsubscribes)
	}
	if s.OpenRate == nil || *s.OpenRate != 0.75 || s.ClickRate == nil || *s.ClickRate != 0.5 || s.ClickToOpenRate == nil || *s.ClickToOpenRate != 2.0/3.0 {
		t.Errorf("rates %v %v %v", s.OpenRate, s.ClickRate, s.ClickToOpenRate)
	}
	wantLinks := []models.NewsletterCampaignLink{
		{ID: a, URL: "https://www.example.org/a", TotalClicks: 3, UniqueClickers: ptr(2)},
		{ID: b, URL: "https://www.example.org/b", TotalClicks: 1, UniqueClickers: ptr(1)},
		{ID: links["https://x.com/triangle"], URL: "https://x.com/triangle", TotalClicks: 0, UniqueClickers: ptr(0)},
	}
	if len(s.Links) != len(wantLinks) {
		t.Fatalf("links %+v", s.Links)
	}
	for i, want := range wantLinks {
		got := s.Links[i]
		if got.ID != want.ID || got.URL != want.URL || got.TotalClicks != want.TotalClicks || got.UniqueClickers == nil || *got.UniqueClickers != *want.UniqueClickers {
			t.Errorf("link %d = %+v (unique %v), want %+v (unique %d)", i, got, got.UniqueClickers, want, *want.UniqueClickers)
		}
	}
	if len(s.Errors) != 1 || s.Errors[0] != (models.NewsletterSendError{Message: "550 5.1.1 <[address]>: no such user", Count: 2}) {
		t.Errorf("errors %+v", s.Errors)
	}
	if len(s.Recent) != 1 || s.Recent[0].ID != issue || s.Recent[0].Unsubscribes == nil {
		t.Errorf("recent %+v", s.Recent)
	}
}

// Campaigns sent before detailed tracking keep their open and click counts but
// say unique clickers per link and unsubscribes are unknown rather than zero.
func TestCampaignStats_PartialCampaignsAreHonest(t *testing.T) {
	f := newStatsFixture(t)
	ctx := context.Background()
	old := f.issue("Old", time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC), 0)
	sub, subToken := f.subscriber(models.SubscriberStatusConfirmed)
	token := f.delivery(old, sub, DeliveryStatusSent, 0, nil)
	links, err := EnsureNewsletterLinks(ctx, f.conn, old, []string{"https://www.example.org/a"})
	if err != nil {
		t.Fatal(err)
	}
	f.click(token, links["https://www.example.org/a"])
	// Even an attributable unsubscribe can't make an old campaign's count whole.
	if _, _, err := UnsubscribeByKey(ctx, f.conn, SubscriberKey{Token: subToken, Delivery: token}, time.Now()); err != nil {
		t.Fatal(err)
	}

	s := f.stats(old)
	if s.DetailedTracking || s.Unsubscribes != nil || s.UnsubscribeRate != nil {
		t.Errorf("old campaign: detailed %v unsubscribes %v", s.DetailedTracking, s.Unsubscribes)
	}
	if s.UniqueClickers != 1 || s.TotalClicks != 1 || len(s.Links) != 1 || s.Links[0].TotalClicks != 1 || s.Links[0].UniqueClickers != nil {
		t.Errorf("old campaign clicks: %+v links %+v", s.NewsletterCampaignSummary, s.Links)
	}

	// A campaign marked for detailed tracking, but with a copy the idle slot's
	// older code sent (no delivery token in its unsubscribe link), is partial
	// too.
	mixed := f.issue("Mixed", time.Date(2026, 10, 31, 18, 0, 0, 0, time.UTC), NewsletterTrackingVersion)
	sub2, _ := f.subscriber(models.SubscriberStatusConfirmed)
	sub3, _ := f.subscriber(models.SubscriberStatusConfirmed)
	f.delivery(mixed, sub2, DeliveryStatusSent, NewsletterTrackingVersion, nil)
	f.delivery(mixed, sub3, DeliveryStatusSent, 0, nil)
	if s := f.stats(mixed); s.DetailedTracking || s.Unsubscribes != nil {
		t.Errorf("mixed campaign: detailed %v unsubscribes %v", s.DetailedTracking, s.Unsubscribes)
	}
	// Recent lists this campaign and the ones before it, newest first.
	s = f.stats(mixed)
	if len(s.Recent) != 2 || s.Recent[0].ID != mixed || s.Recent[1].ID != old || s.Recent[1].Unsubscribes != nil {
		t.Errorf("recent %+v", s.Recent)
	}
	if s := f.stats(old); len(s.Recent) != 1 || s.Recent[0].ID != old {
		t.Errorf("an older campaign's comparison must not include later ones: %+v", s.Recent)
	}
}

func TestCampaignStats_UnknownAndUnsentIssues(t *testing.T) {
	f := newStatsFixture(t)
	ctx := context.Background()
	if _, err := GetNewsletterCampaignStats(ctx, f.conn, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown issue: %v", err)
	}
	draft, err := InsertNewsletter(ctx, f.conn, models.NewsletterInput{Subject: "Draft", EmailHTML: "<p>x</p>"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetNewsletterCampaignStats(ctx, f.conn, draft.ID); !errors.Is(err, ErrNewsletterNotSent) {
		t.Errorf("draft: %v", err)
	}
}

// Clicks arrive in bursts (scanners, eager readers); every one must land in
// both the link's total and the reader's own per-link count.
func TestRecordNewsletterClick_ConcurrentClicksAreExact(t *testing.T) {
	f := newStatsFixture(t)
	ctx := context.Background()
	issue := f.issue("Busy", time.Now().UTC(), NewsletterTrackingVersion)
	links, err := EnsureNewsletterLinks(ctx, f.conn, issue, []string{"https://www.example.org/a", "https://www.example.org/b"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := links["https://www.example.org/a"], links["https://www.example.org/b"]
	var tokens []string
	for range 3 {
		sub, _ := f.subscriber(models.SubscriberStatusConfirmed)
		tokens = append(tokens, f.delivery(issue, sub, DeliveryStatusSent, NewsletterTrackingVersion, nil))
	}

	const perPair = 8
	var wg sync.WaitGroup
	errs := make(chan error, len(tokens)*2*perPair)
	for _, token := range tokens {
		for _, link := range []int64{a, b} {
			for range perPair {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// No retry: a reader's click that deadlocks is a 500.
					_, err := RecordNewsletterClick(ctx, f.conn, token, link)
					errs <- err
				}()
			}
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("click: %v", err)
		}
	}

	s := f.stats(issue)
	if s.TotalClicks != 3*2*perPair || s.UniqueClickers != 3 || s.UniqueOpens != 3 {
		t.Errorf("clicks %d clickers %d opens %d", s.TotalClicks, s.UniqueClickers, s.UniqueOpens)
	}
	for _, l := range s.Links {
		if l.TotalClicks != 3*perPair || l.UniqueClickers == nil || *l.UniqueClickers != 3 {
			t.Errorf("link %s: total %d unique %v", l.URL, l.TotalClicks, l.UniqueClickers)
		}
	}
	var pairs, pairClicks int
	if err := f.conn.QueryRow("SELECT COUNT(*), SUM(click_count) FROM newsletter_delivery_links").Scan(&pairs, &pairClicks); err != nil {
		t.Fatal(err)
	}
	if pairs != 6 || pairClicks != 3*2*perPair {
		t.Errorf("delivery-link rows %d with %d clicks", pairs, pairClicks)
	}
}

func subscriberToken(t *testing.T, conn *sql.DB, id int64) string {
	t.Helper()
	var token string
	if err := conn.QueryRow("SELECT token FROM newsletter_subscribers WHERE id = ?", id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func unsubscribedAt(t *testing.T, conn *sql.DB, delivery string) sql.NullTime {
	t.Helper()
	var at sql.NullTime
	if err := conn.QueryRow("SELECT unsubscribed_at FROM newsletter_deliveries WHERE token = ?", delivery).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// An unsubscribe is put down to the copy it came from only when the delivery
// belongs to the same subscriber and the request actually unsubscribed them.
func TestUnsubscribeByKey_AttributesToTheDelivery(t *testing.T) {
	f := newStatsFixture(t)
	ctx := context.Background()
	issue := f.issue("Issue", time.Now().UTC(), NewsletterTrackingVersion)
	reader, readerToken := f.subscriber(models.SubscriberStatusConfirmed)
	other, otherToken := f.subscriber(models.SubscriberStatusConfirmed)
	readerCopy := f.delivery(issue, reader, DeliveryStatusSent, NewsletterTrackingVersion, nil)
	otherCopy := f.delivery(issue, other, DeliveryStatusSent, NewsletterTrackingVersion, nil)

	// Someone else's delivery token can't pin an unsubscribe on it.
	if _, changed, err := UnsubscribeByKey(ctx, f.conn, SubscriberKey{Token: otherToken, Delivery: readerCopy}, time.Now()); err != nil || !changed {
		t.Fatalf("unsubscribe with a stranger's delivery: changed %v err %v", changed, err)
	}
	if unsubscribedAt(t, f.conn, readerCopy).Valid || unsubscribedAt(t, f.conn, otherCopy).Valid {
		t.Fatal("an unrelated delivery token forged an attribution")
	}

	// Inboxes retry one-click POSTs, sometimes all at once: one attribution.
	var wg sync.WaitGroup
	var mu sync.Mutex
	changes := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, changed, err := UnsubscribeByKey(ctx, f.conn, SubscriberKey{Token: readerToken, Delivery: readerCopy}, time.Now())
			if err != nil {
				t.Errorf("unsubscribe: %v", err)
			}
			mu.Lock()
			if changed {
				changes++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	first := unsubscribedAt(t, f.conn, readerCopy)
	if changes != 1 || !first.Valid {
		t.Fatalf("changes %d, attributed %v", changes, first)
	}
	if s := f.stats(issue); s.Unsubscribes == nil || *s.Unsubscribes != 1 {
		t.Fatalf("unsubscribes %v, want 1", s.Unsubscribes)
	}

	// Resubscribing and unsubscribing again from the same copy is still one
	// reader lost to this issue.
	if _, _, err := ResubscribeByKey(ctx, f.conn, SubscriberKey{Token: readerToken}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := UnsubscribeByKey(ctx, f.conn, SubscriberKey{Token: readerToken, Delivery: readerCopy}, time.Now().Add(time.Hour)); err != nil || !changed {
		t.Fatalf("second unsubscribe: %v %v", changed, err)
	}
	if again := unsubscribedAt(t, f.conn, readerCopy); !again.Time.Equal(first.Time) {
		t.Errorf("attribution time moved from %v to %v", first.Time, again.Time)
	}
	if s := f.stats(issue); *s.Unsubscribes != 1 {
		t.Errorf("unsubscribes %d after a repeat, want 1", *s.Unsubscribes)
	}
}
