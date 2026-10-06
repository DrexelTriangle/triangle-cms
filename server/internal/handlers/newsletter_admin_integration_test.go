package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

const testSiteURL = "https://www.thetriangle.org"

var (
	testEditor = &models.User{ID: 2, Email: "editor@drexel.edu", Role: models.RoleEditor}
	testAdmin  = &models.User{ID: 1, Email: "admin@drexel.edu", Role: models.RoleAdmin}
)

// call runs a handler as user (nil = anonymous) and decodes the JSON reply.
func call(t *testing.T, h http.Handler, user *models.User, method, target, body string, pathValues map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	if user != nil {
		req = req.WithContext(middleware.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") && rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func idPath(id int64) map[string]string { return map[string]string{"id": fmt.Sprint(id)} }

// adminFixture: two lists, and a minimal articles table with one live article
// (id 501, slug denim-day) and one draft (502).
type adminFixture struct {
	conn  *sql.DB
	listA int64
	listB int64
}

func newAdminFixture(t *testing.T) adminFixture {
	t.Helper()
	conn := newsletterHandlerTestDB(t)
	for _, q := range []string{
		"DROP TABLE IF EXISTS articles",
		`CREATE TABLE articles (id BIGINT NOT NULL PRIMARY KEY, slug VARCHAR(255) NOT NULL, title TEXT NOT NULL,
			excerpt TEXT NULL, description TEXT NULL, photo_url TEXT NULL, photo_alt TEXT NULL,
			pub_date DATETIME NULL, archived_at DATETIME NULL) DEFAULT CHARSET=utf8mb4`,
		`INSERT INTO articles (id, slug, title, excerpt, pub_date) VALUES
			(501, 'denim-day', 'Denim Day', 'Awareness on campus', UTC_TIMESTAMP() - INTERVAL 1 DAY),
			(502, 'draft-piece', 'Draft piece', NULL, NULL)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { conn.Exec("DROP TABLE IF EXISTS articles") })
	ctx := context.Background()
	a, err := db.CreateNewsletterList(ctx, conn, "Students", "", true)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := db.CreateNewsletterList(ctx, conn, "Alumni", "", true)
	return adminFixture{conn: conn, listA: a.ID, listB: b.ID}
}

func (fx adminFixture) createCampaign(t *testing.T, body string) int64 {
	t.Helper()
	rec, out := call(t, PostNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPost, "/v1/newsletter/campaigns", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create campaign: %d %s", rec.Code, rec.Body.String())
	}
	return int64(out["id"].(float64))
}

func campaignJSON(subject string, lists []int64, blocks string) string {
	l, _ := json.Marshal(lists)
	return fmt.Sprintf(`{"subject":%q,"preview_text":"","list_ids":%s,"body_blocks":%s}`, subject, l, blocks)
}

const headingBlocks = `{"version":1,"blocks":[{"type":"heading","text":"More From News"}]}`

func TestPatchNewsletterCampaign_ScheduleBlocked(t *testing.T) {
	fx := newAdminFixture(t)
	id := fx.createCampaign(t, campaignJSON("Weekly", []int64{fx.listA}, headingBlocks))
	for _, status := range []string{"scheduled", "sent", "sending"} {
		rec, out := call(t, PatchNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPatch, "/", fmt.Sprintf(`{"status":%q,"subject":"Changed"}`, status), idPath(id))
		if rec.Code != http.StatusConflict || out["error"] != "sending isn't available yet" {
			t.Errorf("status %s: %d %v", status, rec.Code, out)
		}
	}
	c, err := db.GetNewsletterCampaign(context.Background(), fx.conn, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "draft" || c.Subject != "Weekly" {
		t.Errorf("a refused patch still wrote: status %q subject %q", c.Status, c.Subject)
	}
	// "draft" is allowed (a no-op), so a client echoing the status back works.
	rec, _ := call(t, PatchNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPatch, "/", `{"status":"draft","subject":"Renamed"}`, idPath(id))
	if rec.Code != http.StatusOK {
		t.Errorf("patch with status draft = %d", rec.Code)
	}
}

func TestPatchNewsletterCampaign_SentConflict(t *testing.T) {
	fx := newAdminFixture(t)
	id := fx.createCampaign(t, campaignJSON("Original", []int64{fx.listA}, headingBlocks))
	if _, err := fx.conn.Exec("UPDATE newsletter_campaigns SET status = 'sent' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	rec, _ := call(t, PatchNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPatch, "/", `{"subject":"Rewritten"}`, idPath(id))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_campaigns WHERE id = ? AND subject = 'Original'", id); n != 1 {
		t.Error("sent campaign was modified")
	}
}

func TestPatchNewsletterCampaign_Missing(t *testing.T) {
	fx := newAdminFixture(t)
	rec, _ := call(t, PatchNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPatch, "/", `{"subject":"x"}`, idPath(424242))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestPostNewsletterCampaign_Validation(t *testing.T) {
	fx := newAdminFixture(t)
	cases := map[string]string{
		"unknown list":  campaignJSON("x", []int64{999999}, headingBlocks),
		"empty subject": campaignJSON("   ", []int64{fx.listA}, headingBlocks),
		"long subject":  campaignJSON(strings.Repeat("s", 256), []int64{fx.listA}, headingBlocks),
		"bad block":     campaignJSON("x", []int64{fx.listA}, `{"version":1,"blocks":[{"type":"script"}]}`),
		"bad button":    campaignJSON("x", []int64{fx.listA}, `{"version":1,"blocks":[{"type":"button","label":"x","href":"javascript:alert(1)"}]}`),
		"not json":      `{"subject":`,
	}
	for name, body := range cases {
		rec, out := call(t, PostNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPost, "/", body, nil)
		if rec.Code != http.StatusBadRequest || out["error"] == "" {
			t.Errorf("%s: %d %v, want 400 with a message", name, rec.Code, out)
		}
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_campaigns"); n != 0 {
		t.Errorf("invalid campaigns stored: %d", n)
	}
}

func TestPostNewsletterCampaign_LocksArticleLinks(t *testing.T) {
	fx := newAdminFixture(t)
	blocks := `{"version":1,"blocks":[{"type":"text","html":"<p>Read <a href=\"https://www.thetriangle.org/article/denim-day\">this</a></p>"}]}`
	id := fx.createCampaign(t, campaignJSON("Links", []int64{fx.listA}, blocks))

	var stored string
	if err := fx.conn.QueryRow("SELECT body_blocks FROM newsletter_campaigns WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "cms-article:501") || strings.Contains(stored, "thetriangle.org/article") {
		t.Fatalf("stored body did not lock the link to the article id: %s", stored)
	}

	// The article is renamed after the link was placed.
	if _, err := fx.conn.Exec("UPDATE articles SET slug = 'denim-day-raises-awareness' WHERE id = 501"); err != nil {
		t.Fatal(err)
	}
	rec, out := call(t, GetNewsletterCampaignPreview(fx.conn, testSiteURL), testEditor, http.MethodGet, "/", "", idPath(id))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	html, _ := out["html"].(string)
	if !strings.Contains(html, `href="https://www.thetriangle.org/article/denim-day-raises-awareness"`) {
		t.Errorf("preview does not follow the renamed slug")
	}
	if strings.Contains(html, "/article/denim-day\"") {
		t.Errorf("preview still uses the old slug")
	}
}

func TestPostNewsletterCampaign_UnknownArticleURL(t *testing.T) {
	fx := newAdminFixture(t)
	blocks := `{"version":1,"blocks":[{"type":"button","label":"Go","href":"https://www.thetriangle.org/article/no-such-story"}]}`
	rec, out := call(t, PostNewsletterCampaign(fx.conn, testSiteURL), testEditor, http.MethodPost, "/", campaignJSON("x", []int64{fx.listA}, blocks), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "https://www.thetriangle.org/article/no-such-story") {
		t.Errorf("error %q does not name the link", msg)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_campaigns"); n != 0 {
		t.Error("campaign with a dead article link was stored")
	}
}

func TestPostNewsletterRender_WritesNothing(t *testing.T) {
	fx := newAdminFixture(t)
	body := `{"subject":"Preview","body_blocks":{"version":1,"blocks":[
		{"type":"text","html":"<p>Hi<script>alert(1)</script> <a href=\"/article/denim-day\">x</a> <a href=\"/article/draft-piece\">y</a></p>"}]}}`
	rec, out := call(t, PostNewsletterRender(fx.conn, testSiteURL), testEditor, http.MethodPost, "/", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("render: %d %s", rec.Code, rec.Body.String())
	}
	html, _ := out["html"].(string)
	if strings.Contains(html, "alert(1)") {
		t.Error("script survived the render")
	}
	if !strings.Contains(html, `href="https://www.thetriangle.org/article/denim-day"`) {
		t.Error("live article link missing from preview")
	}
	warnings, _ := out["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(fmt.Sprint(warnings[0]), "Draft piece") {
		t.Errorf("warnings = %v, want one about the draft", warnings)
	}
	articles, _ := out["articles"].([]any)
	if len(articles) != 2 {
		t.Errorf("articles = %v, want the two linked articles", articles)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_campaigns"); n != 0 {
		t.Error("render wrote a campaign")
	}
}

func TestDeleteNewsletterCampaign_DraftsOnly(t *testing.T) {
	fx := newAdminFixture(t)
	draft := fx.createCampaign(t, campaignJSON("Draft", nil, headingBlocks))
	sent := fx.createCampaign(t, campaignJSON("Sent", nil, headingBlocks))
	fx.conn.Exec("UPDATE newsletter_campaigns SET status = 'sent' WHERE id = ?", sent)

	if rec, _ := call(t, DeleteNewsletterCampaign(fx.conn), testAdmin, http.MethodDelete, "/", "", idPath(sent)); rec.Code != http.StatusConflict {
		t.Errorf("delete sent = %d, want 409", rec.Code)
	}
	if rec, _ := call(t, DeleteNewsletterCampaign(fx.conn), testAdmin, http.MethodDelete, "/", "", idPath(draft)); rec.Code != http.StatusNoContent {
		t.Errorf("delete draft = %d, want 204", rec.Code)
	}
	if rec, _ := call(t, DeleteNewsletterCampaign(fx.conn), testAdmin, http.MethodDelete, "/", "", idPath(draft)); rec.Code != http.StatusNotFound {
		t.Errorf("delete again = %d, want 404", rec.Code)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_campaigns"); n != 1 {
		t.Errorf("campaigns left = %d, want only the sent one", n)
	}
}

func TestNewsletterSubscriberDelete_EditorForbidden(t *testing.T) {
	fx := newAdminFixture(t)
	s, err := db.CreateNewsletterSubscriber(context.Background(), fx.conn, "keep@example.com", "", []int64{fx.listA})
	if err != nil {
		t.Fatal(err)
	}
	h := middleware.RequireAdmin(DeleteNewsletterSubscriber(fx.conn))
	if rec, _ := call(t, h, testEditor, http.MethodDelete, "/", "", idPath(s.ID)); rec.Code != http.StatusForbidden {
		t.Errorf("editor delete = %d, want 403", rec.Code)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE id = ?", s.ID); n != 1 {
		t.Fatal("editor's forbidden delete removed the row")
	}
	if rec, _ := call(t, h, testAdmin, http.MethodDelete, "/", "", idPath(s.ID)); rec.Code != http.StatusNoContent {
		t.Errorf("admin delete = %d, want 204", rec.Code)
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE id = ?", s.ID); n != 0 {
		t.Error("admin delete left the row")
	}
}

func TestPatchNewsletterSubscriber_Unsubscribe(t *testing.T) {
	fx := newAdminFixture(t)
	s, _ := db.CreateNewsletterSubscriber(context.Background(), fx.conn, "u@example.com", "", []int64{fx.listA})
	rec, out := call(t, PatchNewsletterSubscriber(fx.conn), testEditor, http.MethodPatch, "/", `{"status":"unsubscribed"}`, idPath(s.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if out["status"] != "unsubscribed" || out["unsubscribed_at"] == nil {
		t.Errorf("response = %v", out)
	}
	if strings.Contains(rec.Body.String(), "token") {
		t.Error("response leaks the unsubscribe token")
	}
	if n := countRows(t, fx.conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE id = ? AND status = 'unsubscribed'", s.ID); n != 1 {
		t.Error("row not unsubscribed")
	}

	for name, body := range map[string]string{"bad status": `{"status":"deleted"}`, "unknown list": `{"list_ids":[999999]}`} {
		if rec, _ := call(t, PatchNewsletterSubscriber(fx.conn), testEditor, http.MethodPatch, "/", body, idPath(s.ID)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
}

func TestGetNewsletterSubscribers_NeverLeaksToken(t *testing.T) {
	fx := newAdminFixture(t)
	db.CreateNewsletterSubscriber(context.Background(), fx.conn, "a@example.com", "A", []int64{fx.listA})
	var token string
	fx.conn.QueryRow("SELECT token FROM newsletter_subscribers").Scan(&token)

	rec, out := call(t, GetNewsletterSubscribers(fx.conn), testEditor, http.MethodGet, "/v1/newsletter/subscribers?status=subscribed", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "token") || strings.Contains(rec.Body.String(), token) {
		t.Error("subscriber listing leaks the token")
	}
	subs, _ := out["subscribers"].([]any)
	if len(subs) != 1 {
		t.Errorf("subscribers = %v", subs)
	}
	counts, _ := out["counts"].(map[string]any)
	if counts["subscribed"] != float64(1) {
		t.Errorf("counts = %v", counts)
	}
	if rec, _ := call(t, GetNewsletterSubscribers(fx.conn), testEditor, http.MethodGet, "/v1/newsletter/subscribers?status=bogus", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bogus status filter = %d, want 400", rec.Code)
	}
}

func TestPostNewsletterSubscriber_DuplicateAndValidation(t *testing.T) {
	fx := newAdminFixture(t)
	h := PostNewsletterSubscriber(fx.conn)
	body := fmt.Sprintf(`{"email":"Dup@Example.com","name":"Dup","list_ids":[%d]}`, fx.listA)
	rec, out := call(t, h, testEditor, http.MethodPost, "/", body, nil)
	if rec.Code != http.StatusCreated || out["email"] != "dup@example.com" || out["source"] != "admin" {
		t.Fatalf("create = %d %v", rec.Code, out)
	}
	if rec, _ := call(t, h, testEditor, http.MethodPost, "/", body, nil); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}
	if rec, _ := call(t, h, testEditor, http.MethodPost, "/", `{"email":"Bob <b@x.io>","list_ids":[]}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("display-name email = %d, want 400", rec.Code)
	}
}

func TestGetNewsletterRecipientCount(t *testing.T) {
	fx := newAdminFixture(t)
	ctx := context.Background()
	db.CreateNewsletterSubscriber(ctx, fx.conn, "s1@example.com", "", []int64{fx.listA, fx.listB})
	db.CreateNewsletterSubscriber(ctx, fx.conn, "s2@example.com", "", []int64{fx.listB})
	gone, _ := db.CreateNewsletterSubscriber(ctx, fx.conn, "s3@example.com", "", []int64{fx.listA})
	db.UpdateNewsletterSubscriber(ctx, fx.conn, gone.ID, models.NewsletterSubscriberPatchRequest{Status: strPtrH("unsubscribed")})
	id := fx.createCampaign(t, campaignJSON("To both", []int64{fx.listA, fx.listB}, headingBlocks))

	rec, out := call(t, GetNewsletterRecipientCount(fx.conn), testEditor, http.MethodGet, "/", "", idPath(id))
	if rec.Code != http.StatusOK || out["count"] != float64(2) {
		t.Errorf("count = %d %v, want 2", rec.Code, out)
	}
	if rec, _ := call(t, GetNewsletterRecipientCount(fx.conn), testEditor, http.MethodGet, "/", "", idPath(999999)); rec.Code != http.StatusNotFound {
		t.Errorf("missing campaign = %d, want 404", rec.Code)
	}
}

func TestGetNewsletterStats_Shape(t *testing.T) {
	conn := newsletterHandlerTestDB(t)
	rec, out := call(t, GetNewsletterStats(conn), testEditor, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	subs, _ := out["subscribers"].(map[string]any)
	camps, _ := out["campaigns"].(map[string]any)
	for _, k := range []string{"subscribed", "unsubscribed", "all"} {
		if subs[k] != float64(0) {
			t.Errorf("subscribers[%s] = %v, want 0", k, subs[k])
		}
	}
	for _, k := range []string{"draft", "scheduled", "sent", "all"} {
		if camps[k] != float64(0) {
			t.Errorf("campaigns[%s] = %v, want 0", k, camps[k])
		}
	}
	if lists, ok := out["lists"].([]any); !ok || len(lists) != 0 {
		t.Errorf("lists = %v, want []", out["lists"])
	}
}

func TestNewsletterLists_CreatePatchConflict(t *testing.T) {
	conn := newsletterHandlerTestDB(t)
	rec, out := call(t, PostNewsletterList(conn), testAdmin, http.MethodPost, "/", `{"name":"Parents"}`, nil)
	if rec.Code != http.StatusCreated || out["is_public"] != true || out["id"].(float64) < 1000 {
		t.Fatalf("create = %d %v", rec.Code, out)
	}
	id := int64(out["id"].(float64))
	if rec, _ := call(t, PostNewsletterList(conn), testAdmin, http.MethodPost, "/", `{"name":"Parents"}`, nil); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}
	if rec, _ := call(t, PostNewsletterList(conn), testAdmin, http.MethodPost, "/", `{"name":"  "}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("blank name = %d, want 400", rec.Code)
	}
	rec, out = call(t, PatchNewsletterList(conn), testAdmin, http.MethodPatch, "/", `{"is_public":false}`, idPath(id))
	if rec.Code != http.StatusOK || out["is_public"] != false || out["name"] != "Parents" {
		t.Errorf("patch = %d %v", rec.Code, out)
	}
}

func strPtrH(s string) *string { return &s }
