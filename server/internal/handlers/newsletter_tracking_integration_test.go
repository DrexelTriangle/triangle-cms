package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"regexp"
	"strconv"
	"testing"
	"time"

	db "server/internal/database"
	"server/internal/mailer"
	"server/internal/models"
)

func trackingRequest(t *testing.T, conn *sql.DB, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/newsletter/o/{token}", GetNewsletterOpen(conn))
	mux.Handle("GET /v1/newsletter/c/{token}/{link}", GetNewsletterClick(conn, unsubscribeSite))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func newsletterCounts(t *testing.T, conn *sql.DB, id int64) (opens, clicks int) {
	t.Helper()
	if err := conn.QueryRow("SELECT open_count, click_count FROM newsletters WHERE id = ?", id).Scan(&opens, &clicks); err != nil {
		t.Fatal(err)
	}
	return opens, clicks
}

func TestNewsletterTracking(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	const html = `<html><body><a href="https://www.example.org/story">Story</a><a href="{{unsubscribe_url}}">Unsubscribe</a></body></html>`
	issue, err := db.InsertNewsletter(ctx, conn, models.NewsletterInput{Subject: "Tracked", EmailHTML: html}, "editor@example.com")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.InsertNewsletter(ctx, conn, models.NewsletterInput{Subject: "Other", EmailHTML: `<a href="https://evil.example/">x</a>`}, "editor@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// A test send is tracked end to end but counts for nothing.
	sender := &recordingSender{}
	mail := &NewsletterMail{
		Composer: &mailer.Newsletter{From: &netmail.Address{Address: "notify@thetriangle.org"}, PublicURL: "https://cms.example.org"},
		Sender:   sender,
	}
	id := strconv.FormatInt(issue.ID, 10)
	if rec := serveNewsletter(t, PostNewsletterTest(conn, mail), http.MethodPost, "/v1/newsletters/"+id+"/test",
		map[string][]string{"emails": {"editor@example.com"}}, map[string]string{"id": id}); rec.Code != http.StatusNoContent {
		t.Fatalf("test send: %d %s", rec.Code, rec.Body.String())
	}
	click := regexp.MustCompile(`href="https://cms\.example\.org(/v1/newsletter/c/[0-9a-f]{32}/\d+)"`).FindStringSubmatch(sender.sent[0].HTML)
	pixel := regexp.MustCompile(`src="https://cms\.example\.org(/v1/newsletter/o/[0-9a-f]{32})"`).FindStringSubmatch(sender.sent[0].HTML)
	if click == nil || pixel == nil {
		t.Fatalf("test copy is not tracked: %s", sender.sent[0].HTML)
	}
	if rec := trackingRequest(t, conn, http.MethodGet, click[1]); rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://www.example.org/story" {
		t.Fatalf("test click: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if opens, clicks := newsletterCounts(t, conn, issue.ID); opens != 0 || clicks != 0 {
		t.Fatalf("a test copy counted: opens %d clicks %d", opens, clicks)
	}
	var testOpens, testClicks int
	if err := conn.QueryRow("SELECT open_count, click_count FROM newsletter_deliveries WHERE is_test = 1 AND status = 'sent'").Scan(&testOpens, &testClicks); err != nil || testOpens != 1 || testClicks != 1 {
		t.Fatalf("test delivery: opens %d clicks %d (%v); a click should count as an open", testOpens, testClicks, err)
	}

	// A real delivery.
	now := time.Now().UTC()
	res, err := conn.Exec(`INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
		VALUES ('reader@example.com', 'confirmed', ?, ?, ?, ?)`, "11111111111111111111111111111111", now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	subscriberID, _ := res.LastInsertId()
	const token = "22222222222222222222222222222222"
	if _, err := conn.Exec(`INSERT INTO newsletter_deliveries (newsletter_id, subscriber_id, token, status, sent_at, created_at)
		VALUES (?, ?, ?, 'sent', ?, ?)`, issue.ID, subscriberID, token, now, now); err != nil {
		t.Fatal(err)
	}
	links, err := db.EnsureNewsletterLinks(ctx, conn, issue.ID, []string{"https://www.example.org/story"})
	if err != nil {
		t.Fatal(err)
	}
	otherLinks, err := db.EnsureNewsletterLinks(ctx, conn, other.ID, []string{"https://evil.example/"})
	if err != nil {
		t.Fatal(err)
	}
	storyLink := strconv.FormatInt(links["https://www.example.org/story"], 10)

	for range 2 {
		rec := trackingRequest(t, conn, http.MethodGet, "/v1/newsletter/o/"+token)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("GIF89a")) {
			t.Fatalf("pixel: %d %q", rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	trackingRequest(t, conn, http.MethodHead, "/v1/newsletter/o/"+token)
	for range 2 {
		if rec := trackingRequest(t, conn, http.MethodGet, "/v1/newsletter/c/"+token+"/"+storyLink); rec.Code != http.StatusFound {
			t.Fatalf("click: %d", rec.Code)
		}
	}
	if opens, clicks := newsletterCounts(t, conn, issue.ID); opens != 1 || clicks != 1 {
		t.Errorf("issue counts unique readers: opens %d clicks %d, want 1 and 1", opens, clicks)
	}
	var deliveryOpens, deliveryClicks, linkClicks int
	_ = conn.QueryRow("SELECT open_count, click_count FROM newsletter_deliveries WHERE token = ?", token).Scan(&deliveryOpens, &deliveryClicks)
	_ = conn.QueryRow("SELECT click_count FROM newsletter_links WHERE id = ?", storyLink).Scan(&linkClicks)
	if deliveryOpens != 2 || deliveryClicks != 2 || linkClicks != 2 {
		t.Errorf("delivery opens %d clicks %d, link clicks %d; want 2, 2, 2 (HEAD not counted)", deliveryOpens, deliveryClicks, linkClicks)
	}

	// The click endpoint is not an open redirect.
	for name, path := range map[string]string{
		"other issue's link": "/v1/newsletter/c/" + token + "/" + strconv.FormatInt(otherLinks["https://evil.example/"], 10),
		"unknown token":      "/v1/newsletter/c/33333333333333333333333333333333/" + storyLink,
		"bad token":          "/v1/newsletter/c/nope/" + storyLink,
		"bad link":           "/v1/newsletter/c/" + token + "/abc",
	} {
		if rec := trackingRequest(t, conn, http.MethodGet, path); rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := trackingRequest(t, conn, http.MethodGet, "/v1/newsletter/o/33333333333333333333333333333333"); rec.Code != http.StatusOK {
		t.Errorf("unknown pixel should still be an image: %d", rec.Code)
	}

	stats, err := db.GetNewsletterStats(ctx, conn)
	if err != nil || stats.AvgClickRate != nil {
		t.Errorf("no sent issues yet, so no click rate: %v %v", stats.AvgClickRate, err)
	}
}
