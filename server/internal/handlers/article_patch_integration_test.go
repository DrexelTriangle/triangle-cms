package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
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
	// The server connects with clientFoundRows, and the handlers' 404-on-zero-rows
	// checks only behave correctly under it. A test DSN without the flag would
	// exercise semantics production never runs with, so add it rather than asking
	// whoever sets the variable to remember.
	if !strings.Contains(dsn, "clientFoundRows") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		dsn += separator + "clientFoundRows=true"
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
			slug VARCHAR(255) NOT NULL,
			description LONGTEXT,
			excerpt LONGTEXT,
			tags LONGTEXT,
			metadata LONGTEXT,
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

func patchArticleRequestWithID(slug string, id int64, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/v1/articles/"+slug+"?id="+strconv.FormatInt(id, 10), strings.NewReader(body))
	req.SetPathValue("slug", slug)
	return req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Name: "Editor", Role: models.RoleEditor}))
}

func postArticleRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/articles", strings.NewReader(body))
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

func TestPostArticleHTTP_DuplicateTitleGetsUniqueSlug(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories) VALUES (?, ?, ?, ?)",
		"Letter from the editor", "letter-from-the-editor", "Old body", "News",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"title":"Letter from the editor","content":"New body","status":"draft",` +
		`"comment_status":"open","photo_url":"","categories":["News"],"authors":[]}`
	PostArticles(conn).ServeHTTP(rec, postArticleRequest(body))

	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created models.ArticleCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created article: %v", err)
	}
	if created.Slug != "letter-from-the-editor-2" {
		t.Fatalf("created slug = %q, want letter-from-the-editor-2", created.Slug)
	}

	var count int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM articles WHERE title = ?",
		"Letter from the editor",
	).Scan(&count); err != nil {
		t.Fatalf("count articles: %v", err)
	}
	if count != 2 {
		t.Fatalf("matching title count = %d, want 2", count)
	}
}

func TestArticlePatchHTTP_IDQualifiedPatchOnlyUpdatesThatRow(t *testing.T) {
	conn := articlePatchTestDB(t)
	result, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"Original", "letter-from-the-editor", "Old body", "News",
		"Duplicate", "letter-from-the-editor", "New draft", "News",
	)
	if err != nil {
		t.Fatalf("seed duplicated slug articles: %v", err)
	}
	firstID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("first insert id: %v", err)
	}
	secondID := firstID + 1

	rec := httptest.NewRecorder()
	body := `{"title":"Edited duplicate","excerpt":"","content":"Edited body",` +
		`"comment_status":"open","photo_url":"","breaking_news":false,"categories":["News"],` +
		`"tags":[],"authors":[],"focus_keyword":"","meta_description":"","seo_title":""}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequestWithID("letter-from-the-editor", secondID, body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	var firstTitle, firstText, secondTitle, secondText string
	if err := conn.QueryRowContext(context.Background(), "SELECT title, `text` FROM articles WHERE id = ?", firstID).Scan(&firstTitle, &firstText); err != nil {
		t.Fatalf("read first article: %v", err)
	}
	if err := conn.QueryRowContext(context.Background(), "SELECT title, `text` FROM articles WHERE id = ?", secondID).Scan(&secondTitle, &secondText); err != nil {
		t.Fatalf("read second article: %v", err)
	}
	if firstTitle != "Original" || firstText != "Old body" {
		t.Fatalf("first article changed to title=%q text=%q", firstTitle, firstText)
	}
	if secondTitle != "Edited duplicate" || secondText != "Edited body" {
		t.Fatalf("second article = title %q text %q, want edited row", secondTitle, secondText)
	}
}

// The newsroom's report, end to end: an editor opens New Article, types a title
// another article already carries, and leaves the slug field blank. Before the
// id-qualified routes this filed a second row on the same slug, and every
// screen that addressed the article by slug then showed the FIRST one, so the
// new article "became" the old one.
func TestArticleFlowHTTP_SecondArticleWithTheSameTitleStaysItsOwnArticle(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, ?)",
		"Letter from the editor", "letter-from-the-editor", "Last term's letter.", "News", "2025-01-05 12:00:00",
	); err != nil {
		t.Fatalf("seed the existing letter: %v", err)
	}

	// The editor's New Article form sends an empty slug; the server derives it.
	rec := httptest.NewRecorder()
	body := `{"title":"Letter from the editor","slug":"","content":"This term's letter.",` +
		`"status":"draft","comment_status":"open","photo_url":"","categories":["News"],"authors":[]}`
	PostArticles(conn).ServeHTTP(rec, postArticleRequest(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var created models.ArticleCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created article: %v", err)
	}
	if created.Slug != "letter-from-the-editor-2" {
		t.Fatalf("created slug = %q, want letter-from-the-editor-2", created.Slug)
	}

	// Opening it in the CMS must load the new letter, not the old one.
	rec = httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/v1/articles/"+created.Slug+"?id="+strconv.FormatInt(created.ID, 10), nil)
	getReq.SetPathValue("slug", created.Slug)
	getReq = getReq.WithContext(middleware.ContextWithUser(getReq.Context(), &models.User{ID: 1, Name: "Editor", Role: models.RoleEditor}))
	GetArticle(conn).ServeHTTP(rec, getReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "This term's letter.") {
		t.Fatalf("editor opened the wrong article: %s", rec.Body.String())
	}

	// And saving it must not write over the old one.
	rec = httptest.NewRecorder()
	patch := `{"title":"Letter from the editor","content":"This term's letter, edited.","comment_status":"open"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequestWithID(created.Slug, created.ID, patch))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	var oldText string
	if err := conn.QueryRowContext(context.Background(),
		"SELECT `text` FROM articles WHERE slug = ?", "letter-from-the-editor",
	).Scan(&oldText); err != nil {
		t.Fatalf("read the existing letter: %v", err)
	}
	if oldText != "Last term's letter." {
		t.Fatalf("the existing letter was overwritten: %q", oldText)
	}
}

// The duplicates already in production keep their shared slug until someone
// renames one, so a slug-only read has to answer with the same row every time
// rather than whichever the scan reached first.
func TestGetArticleHTTP_DuplicateSlugResolvesToTheSameRowEveryTime(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)",
		"Original", "letter-from-the-editor", "The first letter.", "News", "2025-01-05 12:00:00",
		"Duplicate", "letter-from-the-editor", "The second letter.", "News", "2025-02-05 12:00:00",
	); err != nil {
		t.Fatalf("seed duplicated slug articles: %v", err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/articles/letter-from-the-editor", nil)
		req.SetPathValue("slug", "letter-from-the-editor")
		GetArticle(conn).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "The first letter.") {
			t.Fatalf("attempt %d resolved to a different row: %s", attempt, rec.Body.String())
		}
	}
}

func TestArticlePatchHTTP_ExcerptDerivesFromThePatchedRow(t *testing.T) {
	conn := articlePatchTestDB(t)
	result, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"Original", "letter-from-the-editor", "The body of the first letter.", "News",
		"Duplicate", "letter-from-the-editor", "The body of the second letter.", "News",
	)
	if err != nil {
		t.Fatalf("seed duplicated slug articles: %v", err)
	}
	firstID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("first insert id: %v", err)
	}
	secondID := firstID + 1

	// A blank excerpt with no content in the patch is the editor's ordinary
	// save, and the derived summary has to come from the row being written.
	rec := httptest.NewRecorder()
	body := `{"excerpt":"","comment_status":"open"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequestWithID("letter-from-the-editor", secondID, body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}

	var excerpt string
	if err := conn.QueryRowContext(context.Background(), "SELECT excerpt FROM articles WHERE id = ?", secondID).Scan(&excerpt); err != nil {
		t.Fatalf("read second article: %v", err)
	}
	if !strings.Contains(excerpt, "second letter") {
		t.Fatalf("excerpt = %q, want it derived from the second article's body", excerpt)
	}
}

func TestArticlePatchHTTP_RenameOntoTakenSlugConflicts(t *testing.T) {
	conn := articlePatchTestDB(t)
	result, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories) VALUES (?, ?, ?, ?), (?, ?, ?, ?)",
		"Taken", "letter-from-the-editor", "Old body", "News",
		"Renaming", "a-second-letter", "New body", "News",
	)
	if err != nil {
		t.Fatalf("seed articles: %v", err)
	}
	firstID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("first insert id: %v", err)
	}
	secondID := firstID + 1

	rec := httptest.NewRecorder()
	body := `{"slug":"letter-from-the-editor","comment_status":"open"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequestWithID("a-second-letter", secondID, body))

	if rec.Code != http.StatusConflict {
		t.Fatalf("patch status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	var slug string
	if err := conn.QueryRowContext(context.Background(), "SELECT slug FROM articles WHERE id = ?", secondID).Scan(&slug); err != nil {
		t.Fatalf("read renamed article: %v", err)
	}
	if slug != "a-second-letter" {
		t.Fatalf("slug = %q, want the rename rejected", slug)
	}
}

// A save that leaves the slug alone still sends it, so the collision check has
// to ignore the row doing the saving.
func TestArticlePatchHTTP_ResavingItsOwnSlugIsNotAConflict(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories) VALUES (?, ?, ?, ?)",
		"Only", "letter-from-the-editor", "Old body", "News",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	body := `{"slug":"letter-from-the-editor","title":"Edited","comment_status":"open"}`
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("letter-from-the-editor", body))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

// A create that reuses a title also has to survive concurrently, which is the
// case the per-candidate slug lock exists for.
func TestPostArticleHTTP_ConcurrentDuplicateTitlesGetDistinctSlugs(t *testing.T) {
	conn := articlePatchTestDB(t)
	// The shared test pool is a single connection; the create path needs one
	// for its slug lock and the pool for everything after it.
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(4)

	body := `{"title":"Letter from the editor","content":"Body","status":"draft",` +
		`"comment_status":"open","photo_url":"","categories":["News"],"authors":[]}`

	const creates = 4
	slugs := make(chan string, creates)
	var wg sync.WaitGroup
	for i := 0; i < creates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			PostArticles(conn).ServeHTTP(rec, postArticleRequest(body))
			if rec.Code != http.StatusCreated {
				t.Errorf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
				return
			}
			var created models.ArticleCreateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Errorf("decode created article: %v", err)
				return
			}
			slugs <- created.Slug
		}()
	}
	wg.Wait()
	close(slugs)

	seen := map[string]bool{}
	for slug := range slugs {
		if seen[slug] {
			t.Fatalf("slug %q was handed out twice", slug)
		}
		seen[slug] = true
	}
	if len(seen) != creates {
		t.Fatalf("got %d distinct slugs, want %d", len(seen), creates)
	}
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
// malformed canonical has to be refused rather than stored: the public site
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
// on its own, since an author fixing only the description must not have to touch
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
// no summary anywhere they are listed (listings render `excerpt` and never the
// body) so a blank means "derive one", the same fallback a POST applies.
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

// A save that changes nothing still matched its row, so it is a success. It used
// to be answered 404: the UPDATE changed no values, MySQL reported zero rows
// affected, and the handler reads zero as "no such article". The editor sends the
// whole form on every save and autosaves on a timer, so two saves inside the same
// second are enough to hit it: mod_date is stamped to the second, so the second
// save rewrites every column to what it already holds. To an author, an article
// they are editing reports that it does not exist.
func TestArticlePatchHTTP_NoOpSaveIsNotAMissingArticle(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, excerpt, categories, pub_date) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())",
		"Unchanged story", "unchanged-story", "<p>Body.</p>", "A summary.", "News",
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	body := `{"title":"Unchanged story","excerpt":"A summary.","content":"<p>Body.</p>","comment_status":"open"}`
	for attempt := 1; attempt <= 2; attempt++ {
		rec := httptest.NewRecorder()
		PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("unchanged-story", body))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("save %d status = %d, want 204; body = %s", attempt, rec.Code, rec.Body.String())
		}
	}

	// A slug that genuinely is not there must still be a 404; the fix must not
	// turn "no such article" into a silent success.
	rec := httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(rec, patchArticleRequest("no-such-story", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing-article status = %d, want 404; body = %s", rec.Code, rec.Body.String())
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
