package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"server/internal/mailer"
)

const unsubscribeSite = "https://www.example.org"

// unsubscribeRequest runs one request against the unsubscribe routes the way
// the mux would route it.
func unsubscribeRequest(t *testing.T, conn *sql.DB, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/newsletter/unsubscribe", GetNewsletterUnsubscribe(conn, unsubscribeSite))
	mux.Handle("POST /v1/newsletter/unsubscribe", PostNewsletterUnsubscribe(conn, unsubscribeSite))
	mux.Handle("POST /v1/newsletter/resubscribe", PostNewsletterResubscribe(conn, unsubscribeSite))
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func subscriberStatus(t *testing.T, conn *sql.DB, email string) (string, sql.NullTime) {
	t.Helper()
	var status string
	var unsubscribedAt sql.NullTime
	if err := conn.QueryRow("SELECT status, unsubscribed_at FROM newsletter_subscribers WHERE email = ?", email).Scan(&status, &unsubscribedAt); err != nil {
		t.Fatalf("read %s: %v", email, err)
	}
	return status, unsubscribedAt
}

func TestNewsletterUnsubscribe_Flow(t *testing.T) {
	conn := newsletterTestDB(t)
	now := time.Now().UTC()
	const token = "0123456789abcdef0123456789abcdef"
	seed := []struct {
		email, status, token string
		legacyID             any
		legacyToken          any
	}{
		{"reader@example.com", "confirmed", token, nil, nil},
		{"legacy@example.com", "confirmed", "fedcba9876543210fedcba9876543210", 42, "93de777cd3"},
		{"bounced@example.com", "bounced", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil, nil},
	}
	for _, s := range seed {
		if _, err := conn.Exec(`
			INSERT INTO newsletter_subscribers (email, status, token, legacy_wp_id, legacy_wp_token, subscribed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.email, s.status, s.token, s.legacyID, s.legacyToken, now, now, now); err != nil {
			t.Fatalf("seed %s: %v", s.email, err)
		}
	}
	link := "/v1/newsletter/unsubscribe?t=" + token

	// Opening the link changes nothing: scanners open every link.
	rec := unsubscribeRequest(t, conn, http.MethodGet, link, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Unsubscribe from To The Point?") {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "reader@example.com") || !strings.Contains(rec.Body.String(), "r•••@example.com") {
		t.Error("page should show the address masked")
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: %v", rec.Header())
	}
	if status, _ := subscriberStatus(t, conn, "reader@example.com"); status != "confirmed" {
		t.Fatalf("GET changed status to %s", status)
	}

	// RFC 8058 one-click, twice, as inboxes retry.
	for range 2 {
		rec = unsubscribeRequest(t, conn, http.MethodPost, link, "List-Unsubscribe=One-Click")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "You&#39;re unsubscribed") {
			t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
		}
	}
	status, unsubscribedAt := subscriberStatus(t, conn, "reader@example.com")
	if status != "unsubscribed" || !unsubscribedAt.Valid {
		t.Fatalf("after unsubscribe: %s %v", status, unsubscribedAt)
	}
	if !strings.Contains(rec.Body.String(), `action="/v1/newsletter/resubscribe?t=`+token+`"`) {
		t.Errorf("no resubscribe form: %s", rec.Body.String())
	}

	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/resubscribe?t="+token, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "subscribed again") {
		t.Fatalf("resubscribe: %d %s", rec.Code, rec.Body.String())
	}
	if status, unsubscribedAt := subscriberStatus(t, conn, "reader@example.com"); status != "confirmed" || unsubscribedAt.Valid {
		t.Fatalf("after resubscribe: %s %v", status, unsubscribedAt)
	}

	// A WordPress link: the id alone is not enough.
	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/unsubscribe?nk=42-wrongtoken", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("wrong legacy token: %d", rec.Code)
	}
	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/unsubscribe?"+url.Values{"nk": {"42-93de777cd3"}}.Encode(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy unsubscribe: %d %s", rec.Code, rec.Body.String())
	}
	if status, _ := subscriberStatus(t, conn, "legacy@example.com"); status != "unsubscribed" {
		t.Fatalf("legacy subscriber is %s", status)
	}

	// Bounced addresses can't be resubscribed from a link.
	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/resubscribe?t=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "bounced") {
		t.Fatalf("bounced resubscribe: %d %s", rec.Code, rec.Body.String())
	}
	if status, _ := subscriberStatus(t, conn, "bounced@example.com"); status != "bounced" {
		t.Fatalf("bounced subscriber is %s", status)
	}

	for name, path := range map[string]string{
		"unknown token":   "/v1/newsletter/unsubscribe?t=ffffffffffffffffffffffffffffffff",
		"malformed token": "/v1/newsletter/unsubscribe?t=' OR 1=1",
		"no key":          "/v1/newsletter/unsubscribe",
		"bad legacy":      "/v1/newsletter/unsubscribe?nk=abc",
	} {
		if rec := unsubscribeRequest(t, conn, http.MethodGet, strings.ReplaceAll(path, " ", "%20"), ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", name, rec.Code)
		}
	}

	// A test send's link explains itself and touches nobody.
	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/unsubscribe?t="+mailer.TestToken, "List-Unsubscribe=One-Click")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "test email") {
		t.Fatalf("test token: %d %s", rec.Code, rec.Body.String())
	}
	var confirmed int
	if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM newsletter_subscribers WHERE status = 'confirmed'").Scan(&confirmed); err != nil || confirmed != 1 {
		t.Fatalf("confirmed subscribers = %d (%v), want only reader@", confirmed, err)
	}
}
