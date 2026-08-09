package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run ArticlePatchHTTP -v
func articlePatchTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping article patch integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the article patch test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	for _, table := range []string{"articles_authors", "articles", "authors"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s table: %v", table, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title LONGTEXT,
			slug VARCHAR(255) NOT NULL UNIQUE,
			description LONGTEXT,
			excerpt LONGTEXT,
			tags LONGTEXT,
			`+"`text`"+` LONGTEXT,
			categories LONGTEXT,
			authors LONGTEXT,
			author_ids LONGTEXT,
			comment_status VARCHAR(32),
			photo_url LONGTEXT,
			breaking_news BOOL,
			priority BOOL,
			focus_keyword LONGTEXT,
			meta_description LONGTEXT,
			seo_title LONGTEXT,
			creation_date DATETIME NULL,
			mod_date DATETIME NULL,
			pub_date DATETIME NULL,
			scheduled_pub_date DATETIME NULL,
			last_pub_date DATETIME NULL,
			archived_at DATETIME NULL,
			canonical_url LONGTEXT,
			noindex BOOL NOT NULL DEFAULT 0,
			photo_alt LONGTEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles_authors (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			articles_id BIGINT NOT NULL,
			author_id BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles_authors table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE authors (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			login VARCHAR(255),
			display_name VARCHAR(255),
			archived_at DATETIME NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create authors table: %v", err)
	}
	if err := db.EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	// The write handlers keep this index in step with `articles`.`categories`,
	// so every save touches it.
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS article_categories"); err != nil {
		t.Fatalf("drop article_categories: %v", err)
	}
	if err := db.EnsureArticleCategoriesTable(ctx, conn); err != nil {
		t.Fatalf("ensure article_categories: %v", err)
	}

	return conn
}

// The editor sends the whole form on every save (and every autosave), so a save
// that only changes the body must not move the publish date of an article that
// is already live.
func patchArticleRequest(slug, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/v1/articles/"+slug, strings.NewReader(body))
	req.SetPathValue("slug", slug)
	return req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Name: "Editor", Role: models.RoleEditor}))
}

func articlePubDate(t *testing.T, conn *sql.DB, slug string) sql.NullTime {
	t.Helper()
	var pubDate sql.NullTime
	if err := conn.QueryRowContext(context.Background(), "SELECT pub_date FROM articles WHERE slug = ?", slug).Scan(&pubDate); err != nil {
		t.Fatalf("read pub_date: %v", err)
	}
	return pubDate
}

func TestArticlePatchHTTP_PublishedSaveKeepsOriginalPublishDate(t *testing.T) {
	conn := articlePatchTestDB(t)
	original := time.Date(2025, 3, 4, 15, 30, 0, 0, time.UTC)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, ?)",
		"Live story", "live-story", "Body", "News", original.Format("2006-01-02 15:04:05"),
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"title":"Live story","excerpt":"","content":"Edited body","status":"published",` +
		`"comment_status":"open","photo_url":"","breaking_news":false,"categories":["News"],` +
		`"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("live-story", body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	pubDate := articlePubDate(t, conn, "live-story")
	if !pubDate.Valid {
		t.Fatal("pub_date was cleared by a published save")
	}
	if !pubDate.Time.UTC().Equal(original) {
		t.Fatalf("pub_date = %s, want %s (save moved the publish date)", pubDate.Time.UTC(), original)
	}
}

// Autosave sends the form without status or published_date, precisely so it can
// never move an article across the draft/live line on its own. Content still has
// to land.
func TestArticlePatchHTTP_AutosaveWithoutStatusLeavesPublishStateAlone(t *testing.T) {
	conn := articlePatchTestDB(t)
	scheduled := time.Date(2030, 6, 1, 9, 0, 0, 0, time.UTC)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date, scheduled_pub_date) VALUES (?, ?, ?, ?, ?, ?)",
		"Unpublished story", "unpublished-story", "Body", "News", nil, nil,
	); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date, scheduled_pub_date) VALUES (?, ?, ?, ?, ?, ?)",
		"Queued story", "queued-story", "Body", "News", nil, scheduled.Format("2006-01-02 15:04:05"),
	); err != nil {
		t.Fatalf("seed scheduled article: %v", err)
	}

	autosaveBody := `{"title":"Edited title","excerpt":"","content":"Edited body",` +
		`"comment_status":"open","photo_url":"","breaking_news":false,"categories":["News"],` +
		`"tags":[],"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`

	rec := httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("unpublished-story", autosaveBody))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("draft autosave status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if pubDate := articlePubDate(t, conn, "unpublished-story"); pubDate.Valid {
		t.Fatalf("autosave published a draft: pub_date = %s, want NULL", pubDate.Time.UTC())
	}
	var savedText string
	if err := conn.QueryRowContext(context.Background(), "SELECT `text` FROM articles WHERE slug = ?", "unpublished-story").Scan(&savedText); err != nil {
		t.Fatalf("read text: %v", err)
	}
	if savedText != "Edited body" {
		t.Fatalf("text = %q, want the autosaved body", savedText)
	}

	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("queued-story", autosaveBody))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("scheduled autosave status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if pubDate := articlePubDate(t, conn, "queued-story"); pubDate.Valid {
		t.Fatalf("autosave published a scheduled article: pub_date = %s, want NULL", pubDate.Time.UTC())
	}
	var scheduledAfter sql.NullTime
	if err := conn.QueryRowContext(context.Background(), "SELECT scheduled_pub_date FROM articles WHERE slug = ?", "queued-story").Scan(&scheduledAfter); err != nil {
		t.Fatalf("read scheduled_pub_date: %v", err)
	}
	if !scheduledAfter.Valid || !scheduledAfter.Time.UTC().Equal(scheduled) {
		t.Fatalf("scheduled_pub_date = %v, want %s (autosave moved the schedule)", scheduledAfter, scheduled)
	}
}

func TestArticlePatchHTTP_DraftSaveKeepsPublishDateForRepublish(t *testing.T) {
	conn := articlePatchTestDB(t)
	original := time.Date(2025, 3, 4, 15, 30, 0, 0, time.UTC)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, ?)",
		"Pulled story", "pulled-story", "Body", "News", original.Format("2006-01-02 15:04:05"),
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"title":"Pulled story","content":"Body","status":"draft","categories":["News"]}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("pulled-story", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if pubDate := articlePubDate(t, conn, "pulled-story"); pubDate.Valid {
		t.Fatalf("pub_date = %s, want NULL after unpublishing", pubDate.Time.UTC())
	}

	rec = httptest.NewRecorder()
	body = `{"title":"Pulled story","content":"Body","status":"published","categories":["News"]}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("pulled-story", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("republish status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	pubDate := articlePubDate(t, conn, "pulled-story")
	if !pubDate.Valid {
		t.Fatal("republish left pub_date NULL")
	}
	if !pubDate.Time.UTC().Equal(original) {
		t.Fatalf("pub_date = %s, want the original %s", pubDate.Time.UTC(), original)
	}
}

// The canonical URL override and the noindex flag have to survive a PATCH, and a
// malformed canonical has to be refused rather than stored -- the public site
// emits whatever is in that column verbatim.
func TestArticlePatchHTTP_CanonicalURLAndNoIndexRoundTrip(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, UTC_TIMESTAMP())",
		"Syndicated story", "syndicated-story", "Body", "News",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"title":"Syndicated story","content":"Body","categories":["News"],` +
		`"canonical_url":"https://example.com/original","noindex":true}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("syndicated-story", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	var canonical sql.NullString
	var noIndex sql.NullBool
	if err := conn.QueryRowContext(context.Background(),
		"SELECT canonical_url, noindex FROM articles WHERE slug = ?", "syndicated-story",
	).Scan(&canonical, &noIndex); err != nil {
		t.Fatalf("read seo columns: %v", err)
	}
	if canonical.String != "https://example.com/original" {
		t.Fatalf("canonical_url = %q, want the supplied override", canonical.String)
	}
	if !noIndex.Bool {
		t.Fatal("noindex was not persisted")
	}

	// A relative canonical must be rejected outright, leaving the stored value be.
	rec = httptest.NewRecorder()
	body = `{"title":"Syndicated story","content":"Body","categories":["News"],"canonical_url":"/article/elsewhere"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("syndicated-story", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative canonical status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if err := conn.QueryRowContext(context.Background(),
		"SELECT canonical_url FROM articles WHERE slug = ?", "syndicated-story",
	).Scan(&canonical); err != nil {
		t.Fatalf("re-read canonical_url: %v", err)
	}
	if canonical.String != "https://example.com/original" {
		t.Fatalf("canonical_url = %q, want the rejected patch to have changed nothing", canonical.String)
	}
}

// The featured image's alt text is article-scoped, so it has to survive a PATCH
// on its own -- an author fixing only the description must not have to touch
// anything else to make it stick.
func TestArticlePatchHTTP_PhotoAltRoundTrip(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, photo_url, pub_date) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())",
		"Photo story", "photo-story", "Body", "News", "https://example.com/a.jpg",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"photo_alt":"  A protester holds a hand-painted sign  "}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("photo-story", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	var photoAlt sql.NullString
	if err := conn.QueryRowContext(context.Background(),
		"SELECT photo_alt FROM articles WHERE slug = ?", "photo-story",
	).Scan(&photoAlt); err != nil {
		t.Fatalf("read photo_alt: %v", err)
	}
	// Trimmed, because trailing whitespace in an alt attribute is invisible in
	// the CMS and meaningless to a screen reader.
	if photoAlt.String != "A protester holds a hand-painted sign" {
		t.Fatalf("photo_alt = %q, want the trimmed description", photoAlt.String)
	}

	// Clearing it is a legitimate edit, not a no-op to be ignored: an alt that
	// describes the wrong image is worse than none.
	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("photo-story", `{"photo_alt":""}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clearing patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if err := conn.QueryRowContext(context.Background(),
		"SELECT photo_alt FROM articles WHERE slug = ?", "photo-story",
	).Scan(&photoAlt); err != nil {
		t.Fatalf("re-read photo_alt: %v", err)
	}
	if photoAlt.String != "" {
		t.Fatalf("photo_alt = %q, want it cleared", photoAlt.String)
	}

	// A non-string is a client bug; storing its Go rendering would put junk in an
	// alt attribute the public site emits verbatim.
	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("photo-story", `{"photo_alt":42}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("numeric photo_alt status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// The excerpt box is optional, and the editor sends it on every save whether the
// author filled it in or not. Storing the blank verbatim published articles with
// no summary anywhere they are listed -- listings render `excerpt` and never the
// body -- so a blank means "derive one", the same fallback a POST applies.
func TestArticlePatchHTTP_BlankExcerptIsDerivedFromBody(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, excerpt, categories, pub_date) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())",
		"Phillies reinforce team", "phillies-reinforce-team",
		"<p>The Phillies added two arms before the deadline.</p>", "An older summary.", "Sports",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	// A second article, so the excerpt-only case below patches a row this test
	// has not already written: an UPDATE that sets every column to what it
	// already holds affects no rows, which the handler reports as a 404.
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, excerpt, categories, pub_date) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())",
		"FIFA experiences financial fiascos", "fifa-financial-fiascos",
		"<p>FIFA closed the year short of its own projections.</p>", "A stale summary.", "Sports",
	); err != nil {
		t.Fatalf("seed second article: %v", err)
	}

	readExcerpt := func(slug string) string {
		t.Helper()
		var excerpt sql.NullString
		if err := conn.QueryRowContext(context.Background(),
			"SELECT excerpt FROM articles WHERE slug = ?", slug,
		).Scan(&excerpt); err != nil {
			t.Fatalf("read excerpt: %v", err)
		}
		return excerpt.String
	}

	// A save that carries a new body derives from that body, not the stored one.
	rec := httptest.NewRecorder()
	body := `{"title":"Phillies reinforce team","excerpt":"","content":"<p>The Phillies added two arms and a bat.</p>"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("phillies-reinforce-team", body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if got := readExcerpt("phillies-reinforce-team"); got != "The Phillies added two arms and a bat." {
		t.Fatalf("excerpt = %q, want it derived from the patched body", got)
	}

	// An excerpt-only patch has no body of its own, so it derives from the stored
	// one rather than falling back to empty.
	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("fifa-financial-fiascos", `{"excerpt":"   "}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("excerpt-only patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if got := readExcerpt("fifa-financial-fiascos"); got != "FIFA closed the year short of its own projections." {
		t.Fatalf("excerpt = %q, want it derived from the stored body", got)
	}

	// A supplied excerpt still wins over anything derivable.
	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("phillies-reinforce-team", `{"excerpt":"  An editor's own summary.  "}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("supplied-excerpt patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if got := readExcerpt("phillies-reinforce-team"); got != "An editor's own summary." {
		t.Fatalf("excerpt = %q, want the supplied text", got)
	}

	// A non-string is a client bug, not an excerpt.
	rec = httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("phillies-reinforce-team", `{"excerpt":42}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("numeric excerpt status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// A noindexed article must drop out of the sitemap too: listing a URL whose page
// says noindex is a contradiction search engines report as an error.
func TestSitemapSlugsOmitsNoIndexedArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date, noindex) VALUES "+
			"(?, ?, ?, ?, UTC_TIMESTAMP(), 0), (?, ?, ?, ?, UTC_TIMESTAMP(), 1)",
		"Indexed", "indexed-story", "Body", "News",
		"Hidden", "hidden-story", "Body", "News",
	); err != nil {
		t.Fatalf("seed articles: %v", err)
	}

	rec := httptest.NewRecorder()
	GetSitemapSlugs(conn).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/sitemap/slugs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	payload := rec.Body.String()
	if !strings.Contains(payload, "indexed-story") {
		t.Fatalf("sitemap dropped an indexable article: %s", payload)
	}
	if strings.Contains(payload, "hidden-story") {
		t.Fatalf("sitemap still lists a noindexed article: %s", payload)
	}
}
