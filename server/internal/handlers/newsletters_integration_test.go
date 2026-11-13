package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/mailer"
	"server/internal/models"
)

// The status guard on newsletters is a row lock inside a transaction, which
// only a real MariaDB exercises. Skips unless CMS_TEST_DSN is set.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run NewsletterHTTP -v
func newsletterTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping newsletter integration test")
	}
	// Connect the way the server does (database.buildDSN), so a test sees the
	// RowsAffected the server sees.
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse CMS_TEST_DSN: %v", err)
	}
	cfg.ClientFoundRows = true
	conn, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx := context.Background()
	// Children first; the foreign keys block the reverse.
	for _, table := range []string{"newsletter_delivery_links", "newsletter_deliveries", "newsletter_links", "newsletter_subscriber_lists", "newsletter_subscribers", "newsletter_lists", "newsletters"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	// Twice, because startup runs it against databases that already have the
	// tables and the lists.
	for range 2 {
		if err := db.EnsureNewsletterTables(ctx, conn); err != nil {
			t.Fatalf("ensure newsletter tables: %v", err)
		}
	}
	return conn
}

func serveNewsletter(t *testing.T, h http.Handler, method, path string, body any, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeNewsletter(t *testing.T, rec *httptest.ResponseRecorder) models.Newsletter {
	t.Helper()
	var n models.Newsletter
	if err := json.Unmarshal(rec.Body.Bytes(), &n); err != nil {
		t.Fatalf("decode newsletter: %v (%s)", err, rec.Body.String())
	}
	return n
}

func TestNewsletterHTTP_Lifecycle(t *testing.T) {
	conn := newsletterTestDB(t)

	input := models.NewsletterInput{
		Subject:   "  To The Point  ",
		Preheader: "This week at Drexel",
		Sponsor:   &models.NewsletterSponsor{ImageURL: "https://www.thetriangle.org/ad.png", LinkURL: "https://example.org", Alt: "Ad"},
		BodyHTML:  "<p>Hello</p>",
		EmailHTML: "<html>{{unsubscribe_url}}</html>",
	}
	rec := serveNewsletter(t, PostNewsletter(conn), http.MethodPost, "/v1/newsletters", input, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeNewsletter(t, rec)
	if created.Status != models.NewsletterStatusDraft || created.Subject != "To The Point" || created.Sponsor == nil {
		t.Fatalf("create returned %+v", created)
	}
	id := strconv.FormatInt(created.ID, 10)
	idPath := map[string]string{"id": id}

	rec = serveNewsletter(t, GetNewsletter(conn), http.MethodGet, "/v1/newsletters/"+id, nil, idPath)
	got := decodeNewsletter(t, rec)
	if rec.Code != http.StatusOK || got.EmailHTML != input.EmailHTML || got.Sponsor.LinkURL != "https://example.org" {
		t.Fatalf("get: %d %+v", rec.Code, got)
	}

	// Dropping the sponsor image drops the whole sponsor.
	input.Subject = "To The Point, edited"
	input.Sponsor = &models.NewsletterSponsor{ImageURL: " "}
	rec = serveNewsletter(t, PutNewsletter(conn), http.MethodPut, "/v1/newsletters/"+id, input, idPath)
	updated := decodeNewsletter(t, rec)
	if rec.Code != http.StatusOK || updated.Subject != "To The Point, edited" || updated.Sponsor != nil {
		t.Fatalf("put: %d %+v", rec.Code, updated)
	}

	rec = serveNewsletter(t, PostNewsletterSchedule(conn), http.MethodPost, "/v1/newsletters/"+id+"/schedule",
		map[string]string{"send_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}, idPath)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("schedule in the past: expected 400, got %d", rec.Code)
	}

	sendAt := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	rec = serveNewsletter(t, PostNewsletterSchedule(conn), http.MethodPost, "/v1/newsletters/"+id+"/schedule",
		map[string]string{"send_at": sendAt.Format(time.RFC3339)}, idPath)
	scheduled := decodeNewsletter(t, rec)
	if rec.Code != http.StatusOK || scheduled.Status != models.NewsletterStatusScheduled ||
		scheduled.ScheduledFor == nil || !scheduled.ScheduledFor.Equal(sendAt) {
		t.Fatalf("schedule: %d %+v", rec.Code, scheduled)
	}

	// Editing a scheduled issue keeps it scheduled.
	rec = serveNewsletter(t, PutNewsletter(conn), http.MethodPut, "/v1/newsletters/"+id, input, idPath)
	if got := decodeNewsletter(t, rec); got.Status != models.NewsletterStatusScheduled {
		t.Fatalf("editing a scheduled issue changed its status to %q", got.Status)
	}

	rec = serveNewsletter(t, DeleteNewsletterSchedule(conn), http.MethodDelete, "/v1/newsletters/"+id+"/schedule", nil, idPath)
	unscheduled := decodeNewsletter(t, rec)
	if rec.Code != http.StatusOK || unscheduled.Status != models.NewsletterStatusDraft || unscheduled.ScheduledFor != nil {
		t.Fatalf("unschedule: %d %+v", rec.Code, unscheduled)
	}

	rec = serveNewsletter(t, GetNewsletters(conn), http.MethodGet, "/v1/newsletters", nil, nil)
	var list models.NewslettersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Newsletters) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}

	rec = serveNewsletter(t, DeleteNewsletter(conn), http.MethodDelete, "/v1/newsletters/"+id, nil, idPath)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, h := range []http.Handler{GetNewsletter(conn), DeleteNewsletter(conn)} {
		if rec := serveNewsletter(t, h, http.MethodGet, "/v1/newsletters/"+id, nil, idPath); rec.Code != http.StatusNotFound {
			t.Fatalf("after delete: expected 404, got %d", rec.Code)
		}
	}
}

// Once the sender has an issue, nothing an editor does may change it.
func TestNewsletterHTTP_SentIssueIsFrozen(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()

	input := models.NewsletterInput{Subject: "Sent", EmailHTML: "<html></html>"}
	for _, status := range []string{models.NewsletterStatusSending, models.NewsletterStatusSent} {
		t.Run(status, func(t *testing.T) {
			created, err := db.InsertNewsletter(ctx, conn, input, "")
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			if _, err := conn.ExecContext(ctx, "UPDATE newsletters SET status = ? WHERE id = ?", status, created.ID); err != nil {
				t.Fatalf("set status: %v", err)
			}
			id := strconv.FormatInt(created.ID, 10)
			idPath := map[string]string{"id": id}
			future := map[string]string{"send_at": time.Now().Add(time.Hour).Format(time.RFC3339)}

			for name, rec := range map[string]*httptest.ResponseRecorder{
				"put":        serveNewsletter(t, PutNewsletter(conn), http.MethodPut, "/", input, idPath),
				"delete":     serveNewsletter(t, DeleteNewsletter(conn), http.MethodDelete, "/", nil, idPath),
				"schedule":   serveNewsletter(t, PostNewsletterSchedule(conn), http.MethodPost, "/", future, idPath),
				"unschedule": serveNewsletter(t, DeleteNewsletterSchedule(conn), http.MethodDelete, "/", nil, idPath),
			} {
				if rec.Code != http.StatusConflict {
					t.Errorf("%s: expected 409, got %d: %s", name, rec.Code, rec.Body.String())
				}
			}

			after, err := db.GetNewsletter(ctx, conn, created.ID)
			if err != nil || after.Status != status || after.Subject != "Sent" {
				t.Fatalf("issue changed: %v %+v", err, after)
			}
		})
	}
}

func TestNewsletterHTTP_RejectsBadInput(t *testing.T) {
	conn := newsletterTestDB(t)

	cases := map[string]models.NewsletterInput{
		"no subject":      {Subject: "  ", EmailHTML: "<html></html>"},
		"no email html":   {Subject: "S"},
		"sponsor image":   {Subject: "S", EmailHTML: "x", Sponsor: &models.NewsletterSponsor{ImageURL: "javascript:alert(1)"}},
		"sponsor link":    {Subject: "S", EmailHTML: "x", Sponsor: &models.NewsletterSponsor{ImageURL: "https://a.org/i.png", LinkURL: "ftp://a.org"}},
		"subject too big": {Subject: string(bytes.Repeat([]byte("a"), 256)), EmailHTML: "x"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			rec := serveNewsletter(t, PostNewsletter(conn), http.MethodPost, "/v1/newsletters", input, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	rec := serveNewsletter(t, PostNewsletterTest(conn, nil), http.MethodPost, "/v1/newsletters/1/test", map[string][]string{"emails": {"a@b.c"}}, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("test send: expected 501 with no sender configured, got %d", rec.Code)
	}
}

type recordingSender struct{ sent []*mailer.Message }

func (s *recordingSender) Send(_ context.Context, m *mailer.Message) error {
	s.sent = append(s.sent, m)
	return nil
}

func TestNewsletterHTTP_TestSend(t *testing.T) {
	conn := newsletterTestDB(t)
	sender := &recordingSender{}
	mail := &NewsletterMail{
		Composer: &mailer.Newsletter{From: &netmail.Address{Address: "notify@thetriangle.org"}, PublicURL: "https://cms.example.org"},
		Sender:   sender,
	}

	created, err := db.InsertNewsletter(context.Background(), conn, models.NewsletterInput{
		Subject:   "To The Point",
		EmailHTML: `<a href="{{unsubscribe_url}}">Unsubscribe</a>`,
	}, "editor@example.com")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	path := "/v1/newsletters/" + strconv.FormatInt(created.ID, 10) + "/test"

	rec := serveNewsletter(t, PostNewsletterTest(conn, mail), http.MethodPost, path,
		map[string][]string{"emails": {"a@example.com", " A@example.com ", "b@example.com"}}, map[string]string{"id": strconv.FormatInt(created.ID, 10)})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(sender.sent) != 2 {
		t.Fatalf("expected 2 messages (duplicate address dropped), got %d", len(sender.sent))
	}
	m := sender.sent[0]
	if m.To.Address != "a@example.com" || m.Subject != "[Test] To The Point" {
		t.Errorf("first message to %q with subject %q", m.To.Address, m.Subject)
	}
	if want := "https://cms.example.org/v1/newsletter/unsubscribe?t=" + mailer.TestToken; m.UnsubscribeURL != want || !strings.Contains(m.HTML, want) {
		t.Errorf("unsubscribe link = %q, HTML %q", m.UnsubscribeURL, m.HTML)
	}

	bad := map[string][]string{
		"none":        {" "},
		"not address": {"Someone <a@example.com>"},
		"too many":    {"1@x.org", "2@x.org", "3@x.org", "4@x.org", "5@x.org", "6@x.org"},
	}
	for name, emails := range bad {
		rec := serveNewsletter(t, PostNewsletterTest(conn, mail), http.MethodPost, path,
			map[string][]string{"emails": emails}, map[string]string{"id": strconv.FormatInt(created.ID, 10)})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, rec.Code)
		}
	}
	rec = serveNewsletter(t, PostNewsletterTest(conn, mail), http.MethodPost, "/v1/newsletters/999999/test",
		map[string][]string{"emails": {"a@example.com"}}, map[string]string{"id": "999999"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing issue: expected 404, got %d", rec.Code)
	}
	if len(sender.sent) != 2 {
		t.Errorf("rejected requests still sent mail: %d messages", len(sender.sent))
	}
}

func TestNewsletterHTTP_StatsAndSubscribers(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	subscribers := []struct {
		email  string
		status string
		lists  []int
	}{
		{"a@drexel.edu", models.SubscriberStatusConfirmed, []int{1, 5}},
		{"b@gmail.com", models.SubscriberStatusConfirmed, nil},
		{"c@yahoo.com", models.SubscriberStatusUnsubscribed, []int{2}},
	}
	ids := map[string]int64{}
	for i, s := range subscribers {
		token, err := db.NewSubscriberToken()
		if err != nil {
			t.Fatal(err)
		}
		res, err := conn.ExecContext(ctx, `
			INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, s.email, s.status, token, now.Add(time.Duration(i)*time.Minute), now, now)
		if err != nil {
			t.Fatalf("seed subscriber: %v", err)
		}
		id, _ := res.LastInsertId()
		ids[s.email] = id
		for _, list := range s.lists {
			if _, err := conn.ExecContext(ctx, "INSERT INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (?, ?)", id, list); err != nil {
				t.Fatalf("seed list: %v", err)
			}
		}
	}

	// Two sent issues at 50% and 25%, one sent to nobody (left out of the
	// average), and a draft that must not count at all.
	for _, issue := range []struct {
		status            string
		recipients, opens int
	}{
		{models.NewsletterStatusSent, 100, 50},
		{models.NewsletterStatusSent, 400, 100},
		{models.NewsletterStatusSent, 0, 0},
		{models.NewsletterStatusDraft, 999, 999},
	} {
		created, err := db.InsertNewsletter(ctx, conn, models.NewsletterInput{Subject: "S", EmailHTML: "x"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE newsletters SET status = ?, recipient_count = ?, open_count = ? WHERE id = ?",
			issue.status, issue.recipients, issue.opens, created.ID); err != nil {
			t.Fatal(err)
		}
	}

	rec := serveNewsletter(t, GetNewsletterStats(conn), http.MethodGet, "/v1/newsletters/stats", nil, nil)
	var stats models.NewsletterStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.SubscriberCount != 2 || stats.SentCount != 3 || stats.RecipientsReached != 500 ||
		stats.AvgOpenRate == nil || math.Abs(*stats.AvgOpenRate-0.375) > 1e-6 {
		t.Fatalf("stats: %+v (avg %v)", stats, stats.AvgOpenRate)
	}

	listSubs := func(query string) []models.NewsletterSubscriber {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/newsletter-subscribers"+query, nil)
		rec := httptest.NewRecorder()
		GetNewsletterSubscribers(conn).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list subscribers%s: %d %s", query, rec.Code, rec.Body.String())
		}
		var resp models.NewsletterSubscribersResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Subscribers
	}

	confirmed := listSubs("")
	if len(confirmed) != 2 || confirmed[0].Email != "b@gmail.com" || len(confirmed[0].Lists) != 0 ||
		confirmed[1].Email != "a@drexel.edu" || len(confirmed[1].Lists) != 2 || confirmed[1].Lists[1] != 5 {
		t.Fatalf("confirmed subscribers: %+v", confirmed)
	}
	if all := listSubs("?status=all"); len(all) != 3 {
		t.Fatalf("all subscribers: %+v", all)
	}
	badFilter := httptest.NewRecorder()
	GetNewsletterSubscribers(conn).ServeHTTP(badFilter, httptest.NewRequest(http.MethodGet, "/?status=nope", nil))
	if badFilter.Code != http.StatusBadRequest {
		t.Fatalf("bad status filter: expected 400, got %d", badFilter.Code)
	}

	idPath := map[string]string{"id": strconv.FormatInt(ids["a@drexel.edu"], 10)}
	if rec := serveNewsletter(t, DeleteNewsletterSubscriber(conn), http.MethodDelete, "/", nil, idPath); rec.Code != http.StatusNoContent {
		t.Fatalf("delete subscriber: expected 204, got %d", rec.Code)
	}
	if rec := serveNewsletter(t, DeleteNewsletterSubscriber(conn), http.MethodDelete, "/", nil, idPath); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: expected 404, got %d", rec.Code)
	}
	var memberships int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM newsletter_subscriber_lists WHERE subscriber_id = ?", ids["a@drexel.edu"]).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("list memberships survived the delete: %d %v", memberships, err)
	}

	var lists int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM newsletter_lists").Scan(&lists); err != nil || lists != 7 {
		t.Fatalf("expected the 7 WordPress lists, got %d %v", lists, err)
	}
}
