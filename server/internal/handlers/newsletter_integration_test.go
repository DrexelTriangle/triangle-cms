package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/akismet"
	db "server/internal/database"
)

var newsletterHandlerTables = []string{
	"newsletter_campaign_lists", "newsletter_subscriber_lists",
	"newsletter_campaigns", "newsletter_subscribers", "newsletter_lists",
}

func newsletterHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping newsletter handler integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, table := range newsletterHandlerTables {
		if _, err := conn.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsureNewsletterTables(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func countRows(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

type fakeSpamChecker struct {
	spam  bool
	err   error
	calls int
	last  akismet.Comment
}

func (f *fakeSpamChecker) CheckComment(_ context.Context, c akismet.Comment) (bool, error) {
	f.calls++
	f.last = c
	return f.spam, f.err
}

type subscribeFixture struct {
	conn            *sql.DB
	public, private int64
}

func newSubscribeFixture(t *testing.T) subscribeFixture {
	t.Helper()
	conn := newsletterHandlerTestDB(t)
	ctx := context.Background()
	pub, err := db.CreateNewsletterList(ctx, conn, "Students", "", true)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := db.CreateNewsletterList(ctx, conn, "Staff test list", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return subscribeFixture{conn: conn, public: pub.ID, private: priv.ID}
}

func postSubscribe(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/newsletter/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-agent")
	req.RemoteAddr = "203.0.113.9:4444"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func subscribeBody(email string, lists []int64, extra map[string]any) string {
	m := map[string]any{"email": email, "lists": lists}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestPostNewsletterSubscribe_IdenticalResponses(t *testing.T) {
	fx := newSubscribeFixture(t)
	clean := PostNewsletterSubscribe(fx.conn, &fakeSpamChecker{})
	spam := PostNewsletterSubscribe(fx.conn, &fakeSpamChecker{spam: true})

	responses := map[string]*httptest.ResponseRecorder{
		"new":       postSubscribe(t, clean, subscribeBody("new@example.com", []int64{fx.public}, nil)),
		"duplicate": postSubscribe(t, clean, subscribeBody("NEW@example.com", []int64{fx.public}, nil)),
		"honeypot":  postSubscribe(t, clean, subscribeBody("bot@example.com", []int64{fx.public}, map[string]any{"website": "http://spam.example"})),
		"spam":      postSubscribe(t, spam, subscribeBody("spammer@example.com", []int64{fx.public}, nil)),
	}
	var reference []byte
	for name, rec := range responses {
		if rec.Code != http.StatusAccepted {
			t.Errorf("%s: status %d, want 202", name, rec.Code)
		}
		if reference == nil {
			reference = rec.Body.Bytes()
		}
		if !bytes.Equal(rec.Body.Bytes(), reference) {
			t.Errorf("%s: body %q differs from %q; responses must not reveal what happened", name, rec.Body.String(), reference)
		}
	}
	if string(reference) != "{\"ok\":true}\n" {
		t.Errorf("body = %q", reference)
	}

	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = 'new@example.com'"); n != 1 {
		t.Errorf("new/duplicate address rows = %d, want 1", n)
	}
	for _, email := range []string{"bot@example.com", "spammer@example.com"} {
		if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = ?", email); n != 0 {
			t.Errorf("%s was stored", email)
		}
	}
}

func TestPostNewsletterSubscribe_HoneypotSkipsSpamCheck(t *testing.T) {
	fx := newSubscribeFixture(t)
	checker := &fakeSpamChecker{}
	postSubscribe(t, PostNewsletterSubscribe(fx.conn, checker), subscribeBody("bot@example.com", []int64{fx.public}, map[string]any{"website": "x"}))
	if checker.calls != 0 {
		t.Errorf("honeypot submission reached Akismet (%d calls); it should cost nothing", checker.calls)
	}
}

func TestPostNewsletterSubscribe_AkismetErrorStillStores(t *testing.T) {
	fx := newSubscribeFixture(t)
	for i, err := range []error{errors.New("down"), &akismet.ConfigError{Body: "invalid"}} {
		email := []string{"a@example.com", "b@example.com"}[i]
		rec := postSubscribe(t, PostNewsletterSubscribe(fx.conn, &fakeSpamChecker{err: err}), subscribeBody(email, []int64{fx.public}, nil))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status %d", rec.Code)
		}
		if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = ?", email); n != 1 {
			t.Errorf("Akismet error %T dropped the subscription", err)
		}
	}
}

func TestPostNewsletterSubscribe_AkismetGetsSignup(t *testing.T) {
	fx := newSubscribeFixture(t)
	checker := &fakeSpamChecker{}
	postSubscribe(t, PostNewsletterSubscribe(fx.conn, checker), subscribeBody("  Mixed@Example.COM ", []int64{fx.public}, map[string]any{"name": " Ann "}))
	if checker.calls != 1 {
		t.Fatalf("Akismet calls = %d", checker.calls)
	}
	got := checker.last
	if got.Type != "signup" || got.AuthorEmail != "mixed@example.com" || got.Author != "Ann" || got.UserIP != "203.0.113.9" || got.UserAgent != "test-agent" {
		t.Errorf("Akismet comment = %+v", got)
	}
}

func TestPostNewsletterSubscribe_StoresListsNameAndIP(t *testing.T) {
	fx := newSubscribeFixture(t)
	rec := postSubscribe(t, PostNewsletterSubscribe(fx.conn, nil), subscribeBody("x@example.com", []int64{fx.public, fx.public}, map[string]any{"name": "Xi"}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var name, ip, source string
	if err := fx.conn.QueryRow("SELECT name, signup_ip, source FROM newsletter_subscribers WHERE email = 'x@example.com'").Scan(&name, &ip, &source); err != nil {
		t.Fatal(err)
	}
	if name != "Xi" || ip != "203.0.113.9" || source != "public_form" {
		t.Errorf("row = %q %q %q", name, ip, source)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscriber_lists"); n != 1 {
		t.Errorf("memberships = %d, want 1 (duplicate list id collapsed)", n)
	}
}

func TestPostNewsletterSubscribe_RejectsHostileInput(t *testing.T) {
	fx := newSubscribeFixture(t)
	h := PostNewsletterSubscribe(fx.conn, nil)
	many := make([]int64, 21)
	for i := range many {
		many[i] = int64(i + 1)
	}
	cases := map[string]string{
		"display name":     subscribeBody("Bob <bob@x.io>", []int64{fx.public}, nil),
		"header injection": subscribeBody("bob@x.io\r\nBcc: a@b.io", []int64{fx.public}, nil),
		"private list":     subscribeBody("bob@x.io", []int64{fx.private}, nil),
		"public+private":   subscribeBody("bob@x.io", []int64{fx.public, fx.private}, nil),
		"unknown list":     subscribeBody("bob@x.io", []int64{999999}, nil),
		"no lists":         subscribeBody("bob@x.io", []int64{}, nil),
		"21 lists":         subscribeBody("bob@x.io", many, nil),
		"control in name":  subscribeBody("bob@x.io", []int64{fx.public}, map[string]any{"name": "a\nb"}),
		"lists not array":  `{"email":"bob@x.io","lists":"1"}`,
		"not json":         `email=bob@x.io`,
		"5 KB body":        subscribeBody("bob@x.io", []int64{fx.public}, map[string]any{"name": strings.Repeat("a", 5<<10)}),
		"two documents":    subscribeBody("bob@x.io", []int64{fx.public}, nil) + `{"email":"c@x.io"}`,
	}
	for name, body := range cases {
		rec := postSubscribe(t, h, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"invalid subscription"}` {
			t.Errorf("%s: body %s", name, got)
		}
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers"); n != 0 {
		t.Errorf("hostile input stored %d rows", n)
	}
}

func TestPostNewsletterSubscribe_NilCheckerStores(t *testing.T) {
	fx := newSubscribeFixture(t)
	postSubscribe(t, PostNewsletterSubscribe(fx.conn, nil), subscribeBody("nil@example.com", []int64{fx.public}, nil))
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = 'nil@example.com'"); n != 1 {
		t.Error("subscription without a spam checker was not stored")
	}
}

func TestPostNewsletterSubscribe_DBErrorIsGeneric(t *testing.T) {
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		dsn = "u:p@tcp(127.0.0.1:1)/db"
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	rec := postSubscribe(t, PostNewsletterSubscribe(conn, nil), subscribeBody("x@example.com", []int64{1}, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"subscription failed"}` {
		t.Errorf("body %s leaks internals", got)
	}
}
