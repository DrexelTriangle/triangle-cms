package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "server/internal/database"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run Featured -v

func seedFeaturedTestArticle(t *testing.T, conn *sql.DB, slug string, priority bool, pubDate any, archivedAt any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, priority, pub_date, archived_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		slug, slug, "Body", "News", priority, pubDate, archivedAt,
	); err != nil {
		t.Fatalf("seed %s: %v", slug, err)
	}
}

func featuredSlugs(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "SELECT slug FROM articles WHERE priority = 1 ORDER BY slug")
	if err != nil {
		t.Fatalf("read featured slugs: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("scan slug: %v", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

// The homepage has one lead card. If featuring a second article left the first
// one flagged, the tiebreak in GetFeaturedArticle -- not the editor -- would be
// deciding which story runs.
func TestFeaturedArticleHTTP_PatchUnfeaturesThePreviousPick(t *testing.T) {
	conn := articlePatchTestDB(t)
	published := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	seedFeaturedTestArticle(t, conn, "old-lead", true, published, nil)
	seedFeaturedTestArticle(t, conn, "new-lead", false, published, nil)

	rec := httptest.NewRecorder()
	body := `{"title":"new-lead","excerpt":"","content":"Body","comment_status":"open",` +
		`"photo_url":"","breaking_news":false,"is_featured":true,"categories":["News"],` +
		`"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("new-lead", body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	slugs := featuredSlugs(t, conn)
	if len(slugs) != 1 || slugs[0] != "new-lead" {
		t.Fatalf("featured slugs = %v, want [new-lead]", slugs)
	}
}

// Unfeaturing must not silently re-feature anything: the homepage falls back to
// its normal newest-first lead.
func TestFeaturedArticleHTTP_PatchCanClearTheFeaturedFlag(t *testing.T) {
	conn := articlePatchTestDB(t)
	published := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	seedFeaturedTestArticle(t, conn, "only-lead", true, published, nil)

	rec := httptest.NewRecorder()
	body := `{"title":"only-lead","excerpt":"","content":"Body","comment_status":"open",` +
		`"photo_url":"","breaking_news":false,"is_featured":false,"categories":["News"],` +
		`"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("only-lead", body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if slugs := featuredSlugs(t, conn); len(slugs) != 0 {
		t.Fatalf("featured slugs = %v, want none", slugs)
	}
	featured, err := db.GetFeaturedArticle(context.Background(), conn)
	if err != nil {
		t.Fatalf("get featured article: %v", err)
	}
	if featured != nil {
		t.Fatalf("featured article = %q, want none", featured.Slug)
	}
}

// The flag alone must not put a headline on the homepage: an editor who
// features a draft, a scheduled story or an archived one gets the normal lead
// rather than something the public should not see.
func TestFeaturedArticle_UnpublishedRowsNeverLead(t *testing.T) {
	conn := articlePatchTestDB(t)
	past := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	future := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02 15:04:05")

	seedFeaturedTestArticle(t, conn, "featured-draft", true, nil, nil)
	seedFeaturedTestArticle(t, conn, "featured-scheduled", true, future, nil)
	seedFeaturedTestArticle(t, conn, "featured-archived", true, past, past)

	featured, err := db.GetFeaturedArticle(context.Background(), conn)
	if err != nil {
		t.Fatalf("get featured article: %v", err)
	}
	if featured != nil {
		t.Fatalf("featured article = %q, want none", featured.Slug)
	}

	seedFeaturedTestArticle(t, conn, "featured-live", true, past, nil)
	featured, err = db.GetFeaturedArticle(context.Background(), conn)
	if err != nil {
		t.Fatalf("get featured article: %v", err)
	}
	if featured == nil || featured.Slug != "featured-live" {
		t.Fatalf("featured article = %v, want featured-live", featured)
	}
}

// The end-to-end shape editors actually care about: feature a sports story, and
// the homepage news block -- which Scalene renders news[0] of as the big centre
// card -- leads with it.
func TestFeaturedArticleHTTP_HomepageLeadsWithTheFeaturedArticle(t *testing.T) {
	conn := articlePatchTestDB(t)
	ctx := context.Background()
	if err := db.EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	if err := db.EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}

	older := time.Now().UTC().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	newer := time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO articles (title, slug, `text`, categories, priority, pub_date) VALUES "+
			"(?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		"Newest news", "newest-news", "Body", `["News"]`, false, newer,
		"Old sports story", "old-sports-story", "Body", `["Sports"]`, false, older,
	); err != nil {
		t.Fatalf("seed articles: %v", err)
	}

	homepageNewsSlugs := func() []string {
		rec := httptest.NewRecorder()
		GetHomepage(conn).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/homepage", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("homepage status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		// Without a stated freshness bound, a featured-article change reaches
		// readers whenever some intermediary's heuristic decides it should.
		if got := rec.Header().Get("Cache-Control"); got != homepageCacheControl {
			t.Errorf("homepage Cache-Control = %q, want %q", got, homepageCacheControl)
		}
		var body struct {
			News []struct {
				Slug string `json:"slug"`
			} `json:"news"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode homepage: %v", err)
		}
		slugs := make([]string, 0, len(body.News))
		for _, item := range body.News {
			slugs = append(slugs, item.Slug)
		}
		return slugs
	}

	// Nothing featured: the lead is just the newest news story.
	if slugs := homepageNewsSlugs(); len(slugs) == 0 || slugs[0] != "newest-news" {
		t.Fatalf("default news block = %v, want it to lead with newest-news", slugs)
	}

	rec := httptest.NewRecorder()
	body := `{"title":"Old sports story","excerpt":"","content":"Body","comment_status":"open",` +
		`"photo_url":"","breaking_news":false,"is_featured":true,"categories":["Sports"],` +
		`"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("old-sports-story", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	slugs := homepageNewsSlugs()
	if len(slugs) == 0 || slugs[0] != "old-sports-story" {
		t.Fatalf("featured news block = %v, want it to lead with old-sports-story", slugs)
	}
}
