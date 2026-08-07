package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// Public search is the one query whose behaviour lives almost entirely in the
// database: FULLTEXT tokenizing, boolean-mode semantics, and relevance ordering
// are all server-side, so a fake connection would only assert the SQL string we
// already wrote. These need a real MariaDB.
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run ArticleSearch -v
func articleSearchTestDB(t *testing.T, withIndex bool) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping article search integration test")
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
		t.Fatal("timed out waiting for the article search test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles table: %v", err)
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
			archived_at DATETIME NULL,
			canonical_url LONGTEXT,
			noindex BOOL NOT NULL DEFAULT 0,
			photo_alt LONGTEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}

	if withIndex {
		if err := EnsureArticlesSearchIndex(ctx, conn); err != nil {
			t.Fatalf("ensure article search index: %v", err)
		}
	}

	return conn
}

func seedSearchArticle(t *testing.T, conn *sql.DB, slug, title, tags, body, pubDate string) {
	t.Helper()
	var pub any
	if pubDate != "" {
		pub = pubDate
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, tags, `text`, pub_date) VALUES (?, ?, ?, ?, ?)",
		title, slug, tags, body, pub,
	); err != nil {
		t.Fatalf("seed article %s: %v", slug, err)
	}
}

func searchSlugs(t *testing.T, conn *sql.DB, term string) []string {
	t.Helper()
	articles, err := SearchArticles(context.Background(), conn, term, 20, 0)
	if err != nil {
		t.Fatalf("search %q: %v", term, err)
	}
	slugs := make([]string, 0, len(articles))
	for _, article := range articles {
		slugs = append(slugs, article.Slug)
	}
	return slugs
}

// The old LIKE query encoded field priority in a CASE expression. That intent
// has to survive the move to FULLTEXT, or every "search for a headline you
// remember" turns up body mentions first.
func TestArticleSearch_TitleMatchOutranksBodyMention(t *testing.T) {
	conn := articleSearchTestDB(t, true)
	seedSearchArticle(t, conn, "body-mention", "Campus dining hours change", "",
		"The provost said the tuition freeze would not affect dining.", "2026-01-01 12:00:00")
	seedSearchArticle(t, conn, "headline", "Tuition freeze extended another year", "",
		"Students reacted to the announcement on Monday.", "2026-01-01 12:00:00")

	got := searchSlugs(t, conn, "tuition")
	if len(got) != 2 {
		t.Fatalf("search returned %v, want both articles", got)
	}
	if got[0] != "headline" {
		t.Errorf("search returned %v, want the headline match first", got)
	}
}

// A partially typed word is the common case for a search box, and boolean mode
// only prefix-matches when we ask it to.
func TestArticleSearch_PrefixMatchesTheLastTerm(t *testing.T) {
	conn := articleSearchTestDB(t, true)
	seedSearchArticle(t, conn, "commencement", "Commencement moved to the arena", "",
		"Body text.", "2026-01-01 12:00:00")

	if got := searchSlugs(t, conn, "commence"); len(got) != 1 || got[0] != "commencement" {
		t.Errorf("search returned %v, want the commencement article", got)
	}
}

// Every term is required, so a query naming two things only matches the article
// containing both.
func TestArticleSearch_RequiresEveryTerm(t *testing.T) {
	conn := articleSearchTestDB(t, true)
	seedSearchArticle(t, conn, "both", "Dragons basketball wins the opener", "",
		"Body text.", "2026-01-01 12:00:00")
	seedSearchArticle(t, conn, "one", "Dragons swimming takes second", "",
		"Body text.", "2026-01-01 12:00:00")

	if got := searchSlugs(t, conn, "dragons basketball"); len(got) != 1 || got[0] != "both" {
		t.Errorf("search returned %v, want only the article matching both terms", got)
	}
}

// Search is public, so it must never surface drafts, scheduled articles, or
// soft-deleted ones -- the property the FULLTEXT rewrite most easily loses.
func TestArticleSearch_ReturnsOnlyLiveArticles(t *testing.T) {
	conn := articleSearchTestDB(t, true)
	seedSearchArticle(t, conn, "live", "Senate approves budget", "", "Body.", "2026-01-01 12:00:00")
	seedSearchArticle(t, conn, "draft", "Senate approves budget draft", "", "Body.", "")
	seedSearchArticle(t, conn, "scheduled", "Senate approves budget later", "", "Body.", "2099-01-01 12:00:00")
	seedSearchArticle(t, conn, "archived", "Senate approves budget archived", "", "Body.", "2026-01-01 12:00:00")
	if _, err := conn.ExecContext(context.Background(),
		"UPDATE articles SET archived_at = UTC_TIMESTAMP() WHERE slug = 'archived'"); err != nil {
		t.Fatalf("archive article: %v", err)
	}

	if got := searchSlugs(t, conn, "senate budget"); len(got) != 1 || got[0] != "live" {
		t.Errorf("search returned %v, want only the live article", got)
	}
}

// Queries too short to tokenize fall back to LIKE rather than returning nothing,
// which is where FULLTEXT is weakest and substring matching is strongest.
func TestArticleSearch_ShortQueryFallsBackToLike(t *testing.T) {
	conn := articleSearchTestDB(t, true)
	seedSearchArticle(t, conn, "co-op", "Co-op placements rise", "", "Body.", "2026-01-01 12:00:00")

	if got := searchSlugs(t, conn, "op"); len(got) != 1 || got[0] != "co-op" {
		t.Errorf("search returned %v, want the co-op article via the LIKE fallback", got)
	}
}

// The index ALTER is non-fatal at boot, so search has to work on a database
// where it has not run yet.
func TestArticleSearch_WorksWithoutTheFulltextIndex(t *testing.T) {
	conn := articleSearchTestDB(t, false)
	seedSearchArticle(t, conn, "no-index", "Tuition freeze extended", "", "Body.", "2026-01-01 12:00:00")

	if got := searchSlugs(t, conn, "tuition freeze"); len(got) != 1 || got[0] != "no-index" {
		t.Errorf("search returned %v, want the article via the LIKE fallback", got)
	}
}

// An underscore is a LIKE single-char wildcard. On the fallback path an
// unescaped one turns a literal query into a wildcard search.
func TestArticleSearch_FallbackTreatsWildcardsAsLiterals(t *testing.T) {
	conn := articleSearchTestDB(t, false)
	seedSearchArticle(t, conn, "literal", "Report on a_b findings", "", "Body.", "2026-01-01 12:00:00")
	seedSearchArticle(t, conn, "lookalike", "Report on axb findings", "", "Body.", "2026-01-01 12:00:00")

	if got := searchSlugs(t, conn, "a_b"); len(got) != 1 || got[0] != "literal" {
		t.Errorf("search returned %v, want only the literal underscore match", got)
	}
}
