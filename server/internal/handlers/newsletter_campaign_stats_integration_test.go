package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	db "server/internal/database"
	"server/internal/models"
)

func seedStatsSubscriber(t *testing.T, conn *sql.DB, email, token string) int64 {
	t.Helper()
	now := time.Now().UTC()
	res, err := conn.Exec(`INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
		VALUES (?, 'confirmed', ?, ?, ?, ?)`, email, token, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedSentIssue(t *testing.T, conn *sql.DB, version int) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO newsletters (subject, status, body_html, email_html, sent_at, tracking_version, created_at, updated_at)
		VALUES ('To The Point', 'sent', '', '<p>x</p>', UTC_TIMESTAMP(), ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())`, version)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedSentDelivery(t *testing.T, conn *sql.DB, issue, subscriber int64, token string, version int, errMsg any, status string) {
	t.Helper()
	if _, err := conn.Exec(`INSERT INTO newsletter_deliveries (newsletter_id, subscriber_id, token, status, sent_at, tracking_version, error, created_at)
		VALUES (?, ?, ?, ?, UTC_TIMESTAMP(), ?, ?, UTC_TIMESTAMP())`, issue, subscriber, token, status, version, errMsg); err != nil {
		t.Fatal(err)
	}
}

func TestNewsletterHTTP_CampaignStats(t *testing.T) {
	conn := newsletterTestDB(t)
	issue := seedSentIssue(t, conn, db.NewsletterTrackingVersion)
	reader := seedStatsSubscriber(t, conn, "reader@example.org", "11111111111111111111111111111111")
	bounced := seedStatsSubscriber(t, conn, "private.person@example.org", "22222222222222222222222222222222")
	const copyToken = "33333333333333333333333333333333"
	seedSentDelivery(t, conn, issue, reader, copyToken, db.NewsletterTrackingVersion, nil, db.DeliveryStatusSent)
	seedSentDelivery(t, conn, issue, bounced, "44444444444444444444444444444444", 0,
		"550 5.1.1 <private.person@example.org>: user unknown", db.DeliveryStatusFailed)

	id := strconv.FormatInt(issue, 10)
	rec := serveNewsletter(t, GetNewsletterCampaignStats(conn), http.MethodGet, "/v1/newsletters/"+id+"/stats", nil, map[string]string{"id": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "private.person") {
		t.Fatalf("stats expose a subscriber address: %s", rec.Body.String())
	}
	var got models.NewsletterCampaignStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.DetailedTracking || got.Recipients.Sent != 1 || got.Recipients.Failed != 1 || got.Unsubscribes == nil || *got.Unsubscribes != 0 ||
		len(got.Errors) != 1 || got.Errors[0].Count != 1 || len(got.Recent) != 1 {
		t.Fatalf("stats: %s", rec.Body.String())
	}

	// A campaign from before detailed tracking reports unknowns as null.
	old := seedSentIssue(t, conn, 0)
	if _, err := db.EnsureNewsletterLinks(t.Context(), conn, old, []string{"https://www.example.org/a"}); err != nil {
		t.Fatal(err)
	}
	oldID := strconv.FormatInt(old, 10)
	rec = serveNewsletter(t, GetNewsletterCampaignStats(conn), http.MethodGet, "/v1/newsletters/"+oldID+"/stats", nil, map[string]string{"id": oldID})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"detailed_tracking":false`) || !strings.Contains(body, `"unsubscribes":null`) ||
		!strings.Contains(body, `"unique_clickers":null`) || !strings.Contains(body, `"open_rate":null`) {
		t.Fatalf("partial stats: %d %s", rec.Code, body)
	}

	draft, err := db.InsertNewsletter(t.Context(), conn, models.NewsletterInput{Subject: "Draft", EmailHTML: "<p>x</p>"}, "editor@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want int
	}{
		{strconv.FormatInt(draft.ID, 10), http.StatusConflict},
		{"999999", http.StatusNotFound},
		{"abc", http.StatusBadRequest},
	} {
		rec := serveNewsletter(t, GetNewsletterCampaignStats(conn), http.MethodGet, "/v1/newsletters/"+tc.id+"/stats", nil, map[string]string{"id": tc.id})
		if rec.Code != tc.want {
			t.Errorf("id %s: %d, want %d (%s)", tc.id, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// The unsubscribe page keeps the delivery token through to its form, and the
// POST puts the unsubscribe down to that copy. A malformed or foreign delivery
// token never blocks the unsubscribe itself.
func TestNewsletterUnsubscribe_AttributesToTheCopy(t *testing.T) {
	conn := newsletterTestDB(t)
	issue := seedSentIssue(t, conn, db.NewsletterTrackingVersion)
	const readerToken, copyToken = "0123456789abcdef0123456789abcdef", "abcdefabcdefabcdefabcdefabcdefab"
	reader := seedStatsSubscriber(t, conn, "reader@example.com", readerToken)
	seedSentDelivery(t, conn, issue, reader, copyToken, db.NewsletterTrackingVersion, nil, db.DeliveryStatusSent)
	const strangerToken = "fedcba9876543210fedcba9876543210"
	seedStatsSubscriber(t, conn, "stranger@example.com", strangerToken)

	link := "/v1/newsletter/unsubscribe?t=" + readerToken + "&d=" + copyToken
	rec := unsubscribeRequest(t, conn, http.MethodGet, link, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="unsubscribe?d=`+copyToken+`&amp;t=`+readerToken+`"`) {
		t.Fatalf("GET should keep the delivery in the form: %d %s", rec.Code, rec.Body.String())
	}

	// A stranger with this copy's token can unsubscribe only themselves, and
	// it isn't put down to the copy.
	if rec := unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/unsubscribe?t="+strangerToken+"&d="+copyToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("stranger: %d", rec.Code)
	}
	// A malformed delivery token is ignored.
	if rec := unsubscribeRequest(t, conn, http.MethodGet, "/v1/newsletter/unsubscribe?t="+readerToken+"&d=nope", ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("bad d: %d %s", rec.Code, rec.Body.String())
	}

	for range 2 {
		rec := unsubscribeRequest(t, conn, http.MethodPost, link, "List-Unsubscribe=One-Click")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "You&#39;re unsubscribed") {
			t.Fatalf("one-click: %d %s", rec.Code, rec.Body.String())
		}
	}
	stats, err := db.GetNewsletterCampaignStats(t.Context(), conn, issue)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Unsubscribes == nil || *stats.Unsubscribes != 1 {
		t.Fatalf("unsubscribes %v, want exactly 1", stats.Unsubscribes)
	}
	// The resubscribe form keeps the delivery too, though resubscribing never
	// touches the attribution.
	rec = unsubscribeRequest(t, conn, http.MethodPost, link, "")
	if !strings.Contains(rec.Body.String(), `action="resubscribe?d=`+copyToken+`&amp;t=`+readerToken+`"`) {
		t.Fatalf("resubscribe form: %s", rec.Body.String())
	}
}
