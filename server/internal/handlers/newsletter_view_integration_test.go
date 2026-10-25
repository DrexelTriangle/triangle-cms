package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	db "server/internal/database"
	"server/internal/models"
)

func viewRequest(t *testing.T, conn *sql.DB, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/newsletter/issues/{id}", GetNewsletterIssue(conn, unsubscribeSite+"/"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestNewsletterIssuePage(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	const html = `<!doctype html><html><head><title>x</title></head><body>%s <a href="{{unsubscribe_url}}">Unsubscribe</a> <a href="{{view_online_url}}">View online</a></body></html>`

	insert := func(subject string, sentAt *time.Time) int64 {
		n, err := db.InsertNewsletter(ctx, conn, models.NewsletterInput{Subject: subject, EmailHTML: strings.Replace(html, "%s", subject, 1)}, "editor@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if sentAt != nil {
			if _, err := conn.Exec("UPDATE newsletters SET status = 'sent', sent_at = ? WHERE id = ?", *sentAt, n.ID); err != nil {
				t.Fatal(err)
			}
		}
		return n.ID
	}

	if rec := viewRequest(t, conn, "/v1/newsletter/issues/latest"); rec.Code != http.StatusFound || rec.Header().Get("Location") != unsubscribeSite+"/" {
		t.Fatalf("latest with nothing sent: %d to %q, want a redirect to the site", rec.Code, rec.Header().Get("Location"))
	}
	older := time.Now().UTC().Add(-48 * time.Hour)
	newer := time.Now().UTC().Add(-time.Hour)
	oldID := insert("Older issue", &older)
	newID := insert("Newer issue", &newer)
	draftID := insert("Draft issue", nil)

	rec := viewRequest(t, conn, "/v1/newsletter/issues/"+strconv.FormatInt(oldID, 10))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Older issue") {
		t.Fatalf("sent issue: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "{{") {
		t.Errorf("placeholder left: %s", body)
	}
	for _, want := range []string{
		`<head><base target="_blank">`,
		`href="../unsubscribe">Unsubscribe`,
		`href="` + strconv.FormatInt(oldID, 10) + `">View online`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %s", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'self' https://www.example.org") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP = %q", csp)
	}

	if rec := viewRequest(t, conn, "/v1/newsletter/issues/latest"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Newer issue") {
		t.Errorf("latest: %d, want the newer issue", rec.Code)
	}
	for name, path := range map[string]string{
		"draft":   "/v1/newsletter/issues/" + strconv.FormatInt(draftID, 10),
		"missing": "/v1/newsletter/issues/999999",
		"junk":    "/v1/newsletter/issues/abc",
	} {
		if rec := viewRequest(t, conn, path); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Draft issue") {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	_ = newID

	// The keyless unsubscribe page those links lead to explains itself.
	rec = unsubscribeRequest(t, conn, http.MethodGet, "/v1/newsletter/unsubscribe", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "bottom of any issue in your inbox") {
		t.Errorf("keyless unsubscribe: %d %s", rec.Code, rec.Body.String())
	}
	rec = unsubscribeRequest(t, conn, http.MethodPost, "/v1/newsletter/unsubscribe", "List-Unsubscribe=One-Click")
	if rec.Code != http.StatusOK {
		t.Errorf("keyless POST: %d", rec.Code)
	}
}
