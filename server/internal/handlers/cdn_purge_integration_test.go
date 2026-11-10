package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"server/internal/cdnpurge"
	db "server/internal/database"
)

// purgeRecorder is a fake Cloudflare zone that records every URL purged.
type purgeRecorder struct {
	mu   sync.Mutex
	urls []string
}

func (p *purgeRecorder) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.urls)
	slices.Sort(out)
	return slices.Compact(out)
}

// installTestPurger points the handlers at a running purger backed by a fake
// Cloudflare, and returns the recorder plus a func that waits out one flush.
func installTestPurger(t *testing.T) (*purgeRecorder, func()) {
	t.Helper()
	rec := &purgeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Files []string `json:"files"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		rec.mu.Lock()
		rec.urls = append(rec.urls, payload.Files...)
		rec.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	const delay = 20 * time.Millisecond
	p, err := cdnpurge.New(cdnpurge.Config{
		ZoneID: "z", APIToken: "t", SiteURL: "https://www.thetriangle.org",
		Endpoint: srv.URL, Delay: delay,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	SetCDNPurger(p)
	t.Cleanup(func() {
		SetCDNPurger(nil)
		cancel()
		<-done
		srv.Close()
	})
	return rec, func() { time.Sleep(10 * delay) }
}

func seedPurgeTaxonomy(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DELETE FROM site_taxonomy"); err != nil {
		t.Fatalf("clear taxonomy: %v", err)
	}
	for i, row := range []struct{ kind, slug, title, parent string }{
		{"section", "news", "News", ""},
		{"subsection", "campus", "Campus", "news"},
		{"section", "sports", "Sports", ""},
		{"section", "opinion", "Opinion", ""},
	} {
		var parent any
		if row.parent != "" {
			parent = row.parent
		}
		if _, err := conn.ExecContext(ctx,
			// article_count 1: the seeded article is counted, and the handlers
			// decrement on refile/delete.
			"INSERT INTO site_taxonomy (id, kind, slug, canonical_title, parent_slug, article_count) VALUES (?, ?, ?, ?, ?, 1)",
			i+1, row.kind, row.slug, row.title, parent,
		); err != nil {
			t.Fatalf("insert %s: %v", row.slug, err)
		}
	}
	if err := db.RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("refresh aliases: %v", err)
	}
}

func seedPurgeArticle(t *testing.T, conn *sql.DB, slug string, live bool) int64 {
	t.Helper()
	ctx := context.Background()
	var pub any
	if live {
		pub = time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	}
	res, err := conn.ExecContext(ctx,
		"INSERT INTO articles (title, slug, `text`, categories, pub_date, photo_url) VALUES (?, ?, ?, ?, ?, ?)",
		"Title", slug, "Body", `["Campus"]`, pub, "https://www.thetriangle.org/wp-content/uploads/2026/11/lead.jpg",
	)
	if err != nil {
		t.Fatalf("seed article: %v", err)
	}
	id, _ := res.LastInsertId()
	if err := db.ReplaceArticleCategories(ctx, conn, id, []string{"Campus"}); err != nil {
		t.Fatalf("index categories: %v", err)
	}
	authorRes, err := conn.ExecContext(ctx, "INSERT INTO authors (login, display_name) VALUES (?, ?)", "jane-doe", "Jane Doe")
	if err != nil {
		t.Fatalf("seed author: %v", err)
	}
	authorID, _ := authorRes.LastInsertId()
	if _, err := conn.ExecContext(ctx, "INSERT INTO articles_authors (articles_id, author_id) VALUES (?, ?)", id, authorID); err != nil {
		t.Fatalf("link author: %v", err)
	}
	return id
}

// A retitled, refiled article must leave no stale page behind: the old slug,
// the old section and its parent, the new section, the byline and the homepage.
func TestCDNPurge_PatchPurgesOldAndNewSurface(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedPurgeTaxonomy(t, conn)
	id := seedPurgeArticle(t, conn, "old-slug", true)
	rec, waitFlush := installTestPurger(t)

	w := httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(w, patchArticleRequestWithID("old-slug", id, `{"slug":"new-slug","categories":["Sports"]}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d: %s", w.Code, w.Body.String())
	}
	waitFlush()

	want := []string{
		"https://www.thetriangle.org/",
		"https://www.thetriangle.org/article/new-slug",
		"https://www.thetriangle.org/article/old-slug",
		"https://www.thetriangle.org/author/jane-doe",
		"https://www.thetriangle.org/campus",
		"https://www.thetriangle.org/news",
		"https://www.thetriangle.org/sports",
		"https://www.thetriangle.org/wp-content/uploads/2026/11/lead.jpg",
	}
	if got := rec.got(); !slices.Equal(got, want) {
		t.Fatalf("purged\n  %v\nwant\n  %v", got, want)
	}
}

func TestCDNPurge_DraftEditPurgesNothing(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedPurgeTaxonomy(t, conn)
	id := seedPurgeArticle(t, conn, "a-draft", false)
	rec, waitFlush := installTestPurger(t)

	w := httptest.NewRecorder()
	PatchArticle(conn).ServeHTTP(w, patchArticleRequestWithID("a-draft", id, `{"content":"New body"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("patch status = %d: %s", w.Code, w.Body.String())
	}
	waitFlush()
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("editing a draft purged %v", got)
	}
}

// Unpublishing is the case that matters most: a retraction has to come off
// the public site now, not when the 30-minute s-maxage runs out.
func TestCDNPurge_DeletePurgesTheArticle(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedPurgeTaxonomy(t, conn)
	id := seedPurgeArticle(t, conn, "retracted", true)
	rec, waitFlush := installTestPurger(t)

	req := patchArticleRequestWithID("retracted", id, "")
	req.Method = http.MethodDelete
	w := httptest.NewRecorder()
	DeleteArticle(conn).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}
	waitFlush()
	if got := rec.got(); !slices.Contains(got, "https://www.thetriangle.org/article/retracted") ||
		!slices.Contains(got, "https://www.thetriangle.org/") {
		t.Fatalf("delete purged %v; want the article and the homepage", got)
	}
}

func TestCDNPurge_SchedulerReportsAndPurgesDueArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedPurgeTaxonomy(t, conn)
	id := seedPurgeArticle(t, conn, "goes-live", false)
	past := time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")
	if _, err := conn.ExecContext(context.Background(), "UPDATE articles SET scheduled_pub_date = ? WHERE id = ?", past, id); err != nil {
		t.Fatal(err)
	}
	future := seedPurgeArticle(t, conn, "not-yet", false)
	if _, err := conn.ExecContext(context.Background(), "UPDATE articles SET scheduled_pub_date = ? WHERE id = ?",
		time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05"), future); err != nil {
		t.Fatal(err)
	}
	rec, waitFlush := installTestPurger(t)

	n, ids, err := db.PublishDueArticles(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !slices.Equal(ids, []int64{id}) {
		t.Fatalf("PublishDueArticles = %d, %v; want 1, [%d]", n, ids, id)
	}
	if pub := articlePubDate(t, conn, "goes-live"); !pub.Valid {
		t.Fatal("due article was not published")
	}
	if pub := articlePubDate(t, conn, "not-yet"); pub.Valid {
		t.Fatal("future article was published early")
	}

	PurgePublishedArticles(conn, ids)
	waitFlush()
	got := rec.got()
	if !slices.Contains(got, "https://www.thetriangle.org/article/goes-live") || slices.Contains(got, "https://www.thetriangle.org/article/not-yet") {
		t.Fatalf("scheduler purge = %v", got)
	}
}

func TestSectionPagesForCategories_IncludesAncestorsAndIgnoresUnrelated(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedPurgeTaxonomy(t, conn)

	pages, err := db.SectionPagesForCategories(context.Background(), conn, []string{" CAMPUS ", "Nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(pages)
	if want := []string{"campus", "news"}; !slices.Equal(pages, want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
}
