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
			archived_at DATETIME NULL
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
