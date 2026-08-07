package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/middleware"
	"server/internal/models"
)

// seedDefaultOrderArticles reproduces the shape the live database is in after an
// ETL reseed: the legacy archive occupies high ids, and a row the CMS issued an
// id for before that load sits far below them while carrying a current pub_date.
func seedDefaultOrderArticles(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()

	rows := []struct {
		id   int64
		slug string
		pub  string
	}{
		{77, "drafted-before-the-reseed", "2026-08-07 13:00:00"},
		{10108, "todays-other-story", "2026-08-07 13:00:00"},
		{10110, "todays-first-story", "2026-08-07 13:00:00"},
		{10075, "last-issues-story", "2026-07-24 13:00:00"},
		{10020, "an-archive-story", "2011-04-08 15:04:51"},
	}
	for _, row := range rows {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO articles (id, title, slug, `text`, authors, categories, pub_date, creation_date) VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())",
			row.id, row.slug, row.slug, "Body", `["Rui Zhao"]`, "News", row.pub,
		); err != nil {
			t.Fatalf("seed %s: %v", row.slug, err)
		}
	}
}

func listArticlesAnonymously(t *testing.T, conn *sql.DB, query string) []models.ArticleListItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/articles"+query, nil)
	rec := httptest.NewRecorder()

	GetArticles(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload models.ArticlesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return payload.Articles
}

// A published article has to reach readers on its publish date whatever id it
// holds. Ordering the public listing on `id` kept a story that was drafted
// before an ETL reseed ten thousand rows down the list, so it never appeared on
// the homepage or its section page despite being published that morning.
func TestArticlesDefaultOrder_PublicSortsByPublishDateNotID(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedDefaultOrderArticles(t, conn)

	got := slugsOf(listArticlesAnonymously(t, conn, ""))
	want := []string{
		"todays-first-story",
		"todays-other-story",
		"drafted-before-the-reseed",
		"last-issues-story",
		"an-archive-story",
	}
	if len(got) != len(want) {
		t.Fatalf("listing returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listing order = %v, want %v", got, want)
		}
	}
}

// The homepage blocks read through the same default ordering, and a 13-slot news
// block is exactly where the low id used to cost the article its place.
func TestArticlesDefaultOrder_LowIDArticleFitsInATruncatedBlock(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedDefaultOrderArticles(t, conn)

	got := slugsOf(listArticlesAnonymously(t, conn, "?limit=3"))
	if len(got) != 3 {
		t.Fatalf("limit=3 returned %v, want three articles", got)
	}
	if got[2] != "drafted-before-the-reseed" {
		t.Fatalf("third article = %q, want drafted-before-the-reseed; full page = %v", got[2], got)
	}
}

// Editors keep id ordering by default: their listing carries drafts, whose
// pub_date is NULL, and sorting those last buries a new draft on the final page.
func TestArticlesDefaultOrder_EditorListingKeepsIDOrder(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedDefaultOrderArticles(t, conn)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (id, title, slug, `text`, authors, categories, pub_date, creation_date) VALUES (?, ?, ?, ?, ?, ?, NULL, UTC_TIMESTAMP())",
		10111, "a-brand-new-draft", "a-brand-new-draft", "Body", `["Rui Zhao"]`, "News",
	); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/articles", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Name: "Editor", Role: models.RoleEditor}))
	rec := httptest.NewRecorder()
	GetArticles(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload models.ArticlesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	got := slugsOf(payload.Articles)
	if len(got) == 0 || got[0] != "a-brand-new-draft" {
		t.Fatalf("editor listing = %v, want the new draft first", got)
	}
}
