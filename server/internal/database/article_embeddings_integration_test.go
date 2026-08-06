package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// The vector half is almost entirely database behaviour -- VECTOR columns,
// VEC_FromText round-tripping, and nearest-neighbour ordering -- so these need a
// real MariaDB 11.7+.
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run ArticleEmbeddings -v
func articleEmbeddingsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping article embeddings integration test")
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
		t.Fatal("timed out waiting for the article embeddings test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	for _, table := range []string{"article_embeddings", "articles"} {
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

	if err := EnsureArticlesSearchIndex(ctx, conn); err != nil {
		t.Fatalf("ensure article search index: %v", err)
	}
	if err := EnsureArticleEmbeddingsTable(ctx, conn); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "vector") {
			t.Skipf("this MariaDB has no VECTOR support: %v", err)
		}
		t.Fatalf("ensure article embeddings table: %v", err)
	}

	return conn
}

// testVector builds a unit-ish 384-dimensional vector whose direction is set by
// `lead`, so tests can control which article is nearest without a real model.
func testVector(lead int) []float32 {
	vector := make([]float32, EmbeddingDimensions)
	vector[lead] = 1
	return vector
}

func seedEmbeddingArticle(t *testing.T, conn *sql.DB, slug, title, body, pubDate, modDate string) int64 {
	t.Helper()
	var pub, mod any
	if pubDate != "" {
		pub = pubDate
	}
	if modDate != "" {
		mod = modDate
	}
	result, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, tags, `text`, pub_date, mod_date) VALUES (?, ?, '', ?, ?, ?)",
		title, slug, body, pub, mod)
	if err != nil {
		t.Fatalf("seed article %s: %v", slug, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// The round trip through VEC_FromText is where a formatting bug would show up as
// silently wrong distances rather than an error.
func TestArticleEmbeddings_SaveAndReadBack(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	id := seedEmbeddingArticle(t, conn, "story", "Story", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	if err := SaveArticleEmbedding(context.Background(), conn, id, testVector(0), "hash-a", "test-model"); err != nil {
		t.Fatalf("save embedding: %v", err)
	}

	var hash, model string
	if err := conn.QueryRowContext(context.Background(),
		"SELECT source_hash, model FROM article_embeddings WHERE article_id = ?", id).Scan(&hash, &model); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if hash != "hash-a" || model != "test-model" {
		t.Errorf("stored hash/model = %q/%q, want hash-a/test-model", hash, model)
	}

	// Upsert, not insert: the reconciler re-embeds the same article repeatedly.
	if err := SaveArticleEmbedding(context.Background(), conn, id, testVector(1), "hash-b", "test-model"); err != nil {
		t.Fatalf("re-save embedding: %v", err)
	}
	var count int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM article_embeddings WHERE article_id = ?", id).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("article has %d embedding rows after re-save, want 1", count)
	}
}

func TestArticleEmbeddings_RejectsWrongWidthVectors(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	id := seedEmbeddingArticle(t, conn, "story", "Story", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	if err := SaveArticleEmbedding(context.Background(), conn, id, []float32{1, 2, 3}, "hash", "wrong-model"); err == nil {
		t.Fatal("saved a 3-dimensional vector into a 384-dimensional column")
	}
}

// An article with no embedding is invisible to semantic search, so the
// reconciler has to find exactly the live ones that lack a current vector.
func TestArticleEmbeddings_FindsWorkForTheReconciler(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	missing := seedEmbeddingArticle(t, conn, "missing", "Missing", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	current := seedEmbeddingArticle(t, conn, "current", "Current", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	otherModel := seedEmbeddingArticle(t, conn, "other-model", "Other", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	seedEmbeddingArticle(t, conn, "draft", "Draft", "Body", "", "")
	seedEmbeddingArticle(t, conn, "scheduled", "Scheduled", "Body", "2099-01-01 12:00:00", "2099-01-01 12:00:00")

	if err := SaveArticleEmbedding(ctx, conn, current, testVector(0), "hash", "current-model"); err != nil {
		t.Fatalf("save embedding: %v", err)
	}
	if err := SaveArticleEmbedding(ctx, conn, otherModel, testVector(0), "hash", "old-model"); err != nil {
		t.Fatalf("save embedding: %v", err)
	}

	sources, err := ArticlesNeedingEmbeddings(ctx, conn, "current-model", 10)
	if err != nil {
		t.Fatalf("find work: %v", err)
	}

	got := make(map[int64]bool, len(sources))
	for _, source := range sources {
		got[source.ArticleID] = true
	}
	if !got[missing] {
		t.Error("an article with no embedding was not queued")
	}
	if !got[otherModel] {
		t.Error("an article embedded with a different model was not queued")
	}
	if got[current] {
		t.Error("an already-current article was queued for re-embedding")
	}
	if len(sources) != 2 {
		t.Errorf("queued %d articles, want 2: drafts and scheduled articles must be left alone", len(sources))
	}
}

// ETL-seeded rows predate hash bookkeeping in older databases. Re-embedding them
// on that basis alone would mean redoing the whole archive on every deploy.
func TestArticleEmbeddings_LeavesUnattributedRowsAlone(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	id := seedEmbeddingArticle(t, conn, "seeded", "Seeded", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO article_embeddings (article_id, embedding, source_hash, model) VALUES (?, VEC_FromText(?), '', '')",
		id, FormatVector(testVector(0))); err != nil {
		t.Fatalf("seed legacy embedding: %v", err)
	}

	sources, err := ArticlesNeedingEmbeddings(ctx, conn, "current-model", 10)
	if err != nil {
		t.Fatalf("find work: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("queued %d unattributed rows, want 0", len(sources))
	}
}

// mod_date only says the row was touched; the hash says the embedded text
// actually changed. Most saves change neither title, tags, nor body.
func TestArticleEmbeddings_StaleDetectionUsesTheHashNotJustModDate(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	edited := seedEmbeddingArticle(t, conn, "edited", "Headline", "<p>Original body</p>", "2026-01-01 12:00:00", "2026-06-01 12:00:00")
	touched := seedEmbeddingArticle(t, conn, "touched", "Headline", "<p>Original body</p>", "2026-01-01 12:00:00", "2026-06-01 12:00:00")

	// Both were embedded from the ORIGINAL text, and both have been touched since.
	original := BuildEmbeddingSource(edited, "Headline", "", "<p>Original body</p>")
	for _, id := range []int64{edited, touched} {
		if err := SaveArticleEmbedding(ctx, conn, id, testVector(0), original.Hash, "current-model"); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE article_embeddings SET updated_at = '2026-01-02 00:00:00'"); err != nil {
		t.Fatalf("age embeddings: %v", err)
	}
	// Only one of them actually had its text changed.
	if _, err := conn.ExecContext(ctx,
		"UPDATE articles SET `text` = '<p>Rewritten body</p>' WHERE id = ?", edited); err != nil {
		t.Fatalf("edit article: %v", err)
	}

	sources, err := StaleEmbeddingArticles(ctx, conn, 10)
	if err != nil {
		t.Fatalf("find stale: %v", err)
	}
	if len(sources) != 1 || sources[0].ArticleID != edited {
		ids := make([]int64, len(sources))
		for i, source := range sources {
			ids[i] = source.ArticleID
		}
		t.Errorf("stale articles = %v, want only the one whose text changed (%d)", ids, edited)
	}
}

// The nearest-neighbour scan runs before the visibility join, so a vector left
// behind for an unpublished article is a route for it into public search.
func TestArticleEmbeddings_DeletesVectorsForArticlesThatLeftPublic(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	live := seedEmbeddingArticle(t, conn, "live", "Live", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	archived := seedEmbeddingArticle(t, conn, "archived", "Archived", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	unpublished := seedEmbeddingArticle(t, conn, "unpublished", "Unpublished", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	deleted := seedEmbeddingArticle(t, conn, "deleted", "Deleted", "Body", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	for _, id := range []int64{live, archived, unpublished, deleted} {
		if err := SaveArticleEmbedding(ctx, conn, id, testVector(0), "hash", "current-model"); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
	}
	if _, err := conn.ExecContext(ctx, "UPDATE articles SET archived_at = UTC_TIMESTAMP() WHERE id = ?", archived); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE articles SET pub_date = NULL WHERE id = ?", unpublished); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM articles WHERE id = ?", deleted); err != nil {
		t.Fatalf("delete: %v", err)
	}

	removed, err := DeleteOrphanedEmbeddings(ctx, conn)
	if err != nil {
		t.Fatalf("delete orphans: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed %d embeddings, want 3", removed)
	}

	var remaining int64
	if err := conn.QueryRowContext(ctx, "SELECT article_id FROM article_embeddings").Scan(&remaining); err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if remaining != live {
		t.Errorf("remaining embedding is for article %d, want the live one (%d)", remaining, live)
	}
}

// The point of the hybrid: an article the lexical half cannot find at all --
// because it never uses the reader's words -- still surfaces on vector
// similarity alone.
func TestArticleSearchHybrid_SurfacesArticlesLexicalSearchMisses(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	lexical := seedEmbeddingArticle(t, conn, "lexical", "Parking permits repriced", "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	semantic := seedEmbeddingArticle(t, conn, "semantic", "Where to leave your car this fall", "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	// The semantic article sits right on the query direction; the lexical one is
	// orthogonal to it. Only the lexical one contains the query's words.
	if err := SaveArticleEmbedding(ctx, conn, semantic, testVector(0), "hash", "current-model"); err != nil {
		t.Fatalf("save embedding: %v", err)
	}
	if err := SaveArticleEmbedding(ctx, conn, lexical, testVector(5), "hash", "current-model"); err != nil {
		t.Fatalf("save embedding: %v", err)
	}

	articles, err := SearchArticlesHybrid(ctx, conn, "parking permits", testVector(0), 20, 0)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}

	slugs := make([]string, len(articles))
	for i, article := range articles {
		slugs[i] = article.Slug
	}
	if len(slugs) != 2 {
		t.Fatalf("hybrid search returned %v, want both articles", slugs)
	}
	// Lexical wins the tie: it is the only one both halves rank, and ties break
	// toward the words the reader actually typed.
	if slugs[0] != "lexical" {
		t.Errorf("hybrid search returned %v, want the lexical match first", slugs)
	}
}

// A nil or wrong-width query vector is the no-sidecar path, and it has to return
// the same results lexical search would.
func TestArticleSearchHybrid_FallsBackWithoutAQueryVector(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()
	seedEmbeddingArticle(t, conn, "story", "Tuition freeze extended", "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	for _, vector := range [][]float32{nil, {1, 2, 3}} {
		articles, err := SearchArticlesHybrid(ctx, conn, "tuition freeze", vector, 20, 0)
		if err != nil {
			t.Fatalf("hybrid search: %v", err)
		}
		if len(articles) != 1 || articles[0].Slug != "story" {
			t.Errorf("hybrid search with vector %v returned %d results, want the lexical match", vector, len(articles))
		}
	}
}

// Vectors for unpublished articles are deleted on an interval, so the search
// query cannot rely on that having happened yet.
func TestArticleSearchHybrid_NeverSurfacesNonLiveArticles(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	live := seedEmbeddingArticle(t, conn, "live", "Budget approved", "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
	archived := seedEmbeddingArticle(t, conn, "archived", "Budget approved earlier", "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")

	for _, id := range []int64{live, archived} {
		if err := SaveArticleEmbedding(ctx, conn, id, testVector(0), "hash", "current-model"); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
	}
	// Archived, but its vector is deliberately left in place.
	if _, err := conn.ExecContext(ctx, "UPDATE articles SET archived_at = UTC_TIMESTAMP() WHERE id = ?", archived); err != nil {
		t.Fatalf("archive: %v", err)
	}

	articles, err := SearchArticlesHybrid(ctx, conn, "budget approved", testVector(0), 20, 0)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(articles) != 1 || articles[0].Slug != "live" {
		t.Errorf("hybrid search returned %d results, want only the live article", len(articles))
	}
}

func TestArticleSearchHybrid_Paginates(t *testing.T) {
	conn := articleEmbeddingsTestDB(t)
	ctx := context.Background()

	for _, slug := range []string{"one", "two", "three"} {
		id := seedEmbeddingArticle(t, conn, slug, "Senate budget "+slug, "Body.", "2026-01-01 12:00:00", "2026-01-01 12:00:00")
		if err := SaveArticleEmbedding(ctx, conn, id, testVector(int(id)%EmbeddingDimensions), "hash", "current-model"); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
	}

	first, err := SearchArticlesHybrid(ctx, conn, "senate budget", testVector(0), 2, 0)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	second, err := SearchArticlesHybrid(ctx, conn, "senate budget", testVector(0), 2, 2)
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}

	if len(first) != 2 || len(second) != 1 {
		t.Fatalf("pages were %d and %d results, want 2 and 1", len(first), len(second))
	}
	if first[0].Slug == second[0].Slug || first[1].Slug == second[0].Slug {
		t.Error("the second page repeats a result from the first")
	}
}

