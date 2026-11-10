package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"
)

func insertPurgeArticle(t *testing.T, conn *sql.DB, slug, body string, live bool) int64 {
	t.Helper()
	var pub any
	if live {
		pub = time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	}
	res, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, ?)",
		slug, slug, body, `[]`, pub)
	if err != nil {
		t.Fatalf("insert %s: %v", slug, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// A quiz is rendered into every article that embeds it, and those pages are
// cached for 30 days; editing the quiz must purge exactly those.
func TestCDNPurge_QuizEditPurgesOnlyArticlesEmbeddingIt(t *testing.T) {
	conn := articlePatchTestDB(t)
	insertPurgeArticle(t, conn, "embeds-seven", `<p>Intro</p><p>[quiz id="7"]</p>`, true)
	insertPurgeArticle(t, conn, "embeds-eight", `<p>[quiz id="8"]</p>`, true)
	// Quoted in prose, so the site renders text, not the quiz.
	insertPurgeArticle(t, conn, "mentions-seven", `<p>Type [quiz id="7"] to embed one.</p>`, true)
	insertPurgeArticle(t, conn, "draft-seven", `<p>[quiz id="7"]</p>`, false)
	rec, waitFlush := installTestPurger(t)

	purgeQuizArticles(conn, 7)
	waitFlush()

	if got, want := rec.got(), []string{"https://www.thetriangle.org/article/embeds-seven"}; !slices.Equal(got, want) {
		t.Fatalf("purged %v, want %v", got, want)
	}
}

// The byline carries the display name and links to /author/<slug>, so a
// rename has to purge the author's pages under both slugs and their stories.
func TestCDNPurge_AuthorRenamePurgesBothAuthorPagesAndTheirArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	ctx := context.Background()
	live := insertPurgeArticle(t, conn, "by-jane", `<p>Body</p>`, true)
	draft := insertPurgeArticle(t, conn, "jane-draft", `<p>Body</p>`, false)
	insertPurgeArticle(t, conn, "by-someone-else", `<p>Body</p>`, true)
	res, err := conn.ExecContext(ctx, "INSERT INTO authors (login, display_name) VALUES ('jane-doe-renamed', 'Jane Q. Doe')")
	if err != nil {
		t.Fatal(err)
	}
	authorID, _ := res.LastInsertId()
	for _, id := range []int64{live, draft} {
		if _, err := conn.ExecContext(ctx, "INSERT INTO articles_authors (articles_id, author_id) VALUES (?, ?)", id, authorID); err != nil {
			t.Fatal(err)
		}
	}
	rec, waitFlush := installTestPurger(t)

	// The row already carries the new slug, as it does after the UPDATE.
	purgeAuthorPages(conn, "jane-doe", "jane-doe-renamed")
	waitFlush()

	want := []string{
		"https://www.thetriangle.org/",
		"https://www.thetriangle.org/article/by-jane",
		"https://www.thetriangle.org/author/jane-doe",
		"https://www.thetriangle.org/author/jane-doe-renamed",
	}
	if got := rec.got(); !slices.Equal(got, want) {
		t.Fatalf("purged\n  %v\nwant\n  %v", got, want)
	}
}

func TestCDNPurge_ProlificAuthorFallsBackToOnePrefixPurge(t *testing.T) {
	conn := articlePatchTestDB(t)
	ctx := context.Background()
	res, err := conn.ExecContext(ctx, "INSERT INTO authors (login, display_name) VALUES ('busy', 'Busy Writer')")
	if err != nil {
		t.Fatal(err)
	}
	authorID, _ := res.LastInsertId()
	for i := range maxAuthorArticlePurges + 1 {
		id := insertPurgeArticle(t, conn, fmt.Sprintf("story-%03d", i), `<p>Body</p>`, true)
		if _, err := conn.ExecContext(ctx, "INSERT INTO articles_authors (articles_id, author_id) VALUES (?, ?)", id, authorID); err != nil {
			t.Fatal(err)
		}
	}
	rec, waitFlush := installTestPurger(t)

	purgeAuthorPages(conn, "busy")
	waitFlush()

	want := []string{
		"https://www.thetriangle.org/",
		"https://www.thetriangle.org/article/",
		"https://www.thetriangle.org/author/busy",
	}
	if got := rec.got(); !slices.Equal(got, want) {
		t.Fatalf("purged %v, want %v", got, want)
	}
}
