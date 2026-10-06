package database

import (
	"context"
	"testing"
)

// newsletterArticlesTestDB recreates a minimal articles table in the test
// database (the pattern every articles-touching suite here follows) with only
// the columns the newsletter lookup reads.
func newsletterArticlesTestDB(t *testing.T) *NewsletterArticleLookup {
	t.Helper()
	conn := newsletterTestDB(t)
	mustExec(t, conn, "DROP TABLE IF EXISTS articles")
	mustExec(t, conn, `CREATE TABLE articles (
		id BIGINT NOT NULL PRIMARY KEY,
		slug VARCHAR(255) NOT NULL,
		title TEXT NOT NULL,
		excerpt TEXT NULL,
		description TEXT NULL,
		photo_url TEXT NULL,
		photo_alt TEXT NULL,
		pub_date DATETIME NULL,
		archived_at DATETIME NULL
	) DEFAULT CHARSET=utf8mb4`)
	mustExec(t, conn, `INSERT INTO articles (id, slug, title, excerpt, description, photo_url, photo_alt, pub_date, archived_at) VALUES
		(1, 'live', 'Live story', 'The excerpt', 'The description', 'https://cdn.example/live.jpg', 'Alt text', UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL),
		(2, 'no-excerpt', 'No excerpt', '', 'Falls back to description', NULL, NULL, UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL),
		(3, 'future', 'Scheduled', NULL, NULL, NULL, NULL, UTC_TIMESTAMP() + INTERVAL 1 DAY, NULL),
		(4, 'draft', 'Draft', NULL, NULL, NULL, NULL, NULL, NULL),
		(5, 'archived', 'Archived', NULL, NULL, NULL, NULL, UTC_TIMESTAMP() - INTERVAL 1 DAY, UTC_TIMESTAMP())`)
	t.Cleanup(func() { conn.Exec("DROP TABLE IF EXISTS articles") })
	return &NewsletterArticleLookup{Conn: conn}
}

func TestNewsletterArticleLookup_PublishedAndFallbacks(t *testing.T) {
	lk := newsletterArticlesTestDB(t)
	refs, err := lk.ArticlesByID(context.Background(), []int64{1, 2, 3, 4, 5, 99})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := refs[99]; ok {
		t.Error("missing article 99 was returned")
	}
	live := refs[1]
	if !live.Published || live.Slug != "live" || live.Title != "Live story" || live.Excerpt != "The excerpt" || live.ImageURL != "https://cdn.example/live.jpg" || live.ImageAlt != "Alt text" {
		t.Errorf("live = %+v", live)
	}
	if refs[2].Excerpt != "Falls back to description" {
		t.Errorf("empty excerpt did not fall back to description: %+v", refs[2])
	}
	for _, id := range []int64{3, 4, 5} {
		ref, ok := refs[id]
		if !ok {
			t.Errorf("article %d not returned; the renderer needs it to warn by title", id)
			continue
		}
		if ref.Published {
			t.Errorf("article %d (%s) reported as published", id, ref.Title)
		}
	}
}

func TestNewsletterArticleLookup_SlugsSkipArchived(t *testing.T) {
	lk := newsletterArticlesTestDB(t)
	ids, err := lk.ArticleIDsBySlug(context.Background(), []string{"live", "future", "archived", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if ids["live"] != 1 || ids["future"] != 3 {
		t.Errorf("ids = %v; live and scheduled articles must resolve so editors can link ahead", ids)
	}
	if _, ok := ids["archived"]; ok {
		t.Error("an archived article's slug resolved")
	}
	if _, ok := ids["nope"]; ok {
		t.Error("an unknown slug resolved")
	}
}
