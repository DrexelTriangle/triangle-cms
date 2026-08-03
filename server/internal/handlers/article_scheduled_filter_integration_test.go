package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/middleware"
	"server/internal/models"
)

// The Articles search screen sends status=scheduled alongside whatever else the
// editor has typed, so the filter has to hold up on its own and in combination
// with the title search and the section filter.
func seedScheduledFilterArticles(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	future := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02 15:04:05")
	past := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")

	rows := []struct {
		title      string
		slug       string
		categories string
		pub        any
		scheduled  any
	}{
		{"Scheduled campus story", "scheduled-campus-story", "News", nil, future},
		{"Scheduled sports story", "scheduled-sports-story", "Sports", nil, future},
		{"Published campus story", "published-campus-story", "News", past, nil},
		{"Draft campus story", "draft-campus-story", "News", nil, nil},
	}
	for _, row := range rows {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO articles (title, slug, `text`, authors, categories, pub_date, scheduled_pub_date, creation_date) VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())",
			row.title, row.slug, "Body", `["Rui Zhao"]`, row.categories, row.pub, row.scheduled,
		); err != nil {
			t.Fatalf("seed %s: %v", row.slug, err)
		}
	}
}

func listArticlesAsEditor(t *testing.T, conn *sql.DB, query string) []models.ArticleListItem {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/articles"+query, nil)
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
	return payload.Articles
}

func slugsOf(items []models.ArticleListItem) []string {
	slugs := make([]string, 0, len(items))
	for _, item := range items {
		slugs = append(slugs, item.Slug)
	}
	return slugs
}

func TestArticlePatchHTTP_ScheduledFilterReturnsOnlyScheduledArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedScheduledFilterArticles(t, conn)

	items := listArticlesAsEditor(t, conn, "?status=scheduled&sort_by=published_date&sort_direction=desc")

	got := slugsOf(items)
	if len(got) != 2 {
		t.Fatalf("scheduled filter returned %v, want the two scheduled articles", got)
	}
	for _, item := range items {
		if item.Status != models.ArticleStatusScheduled {
			t.Fatalf("article %s has status %q, want scheduled", item.Slug, item.Status)
		}
		if item.PublishedDate == nil {
			t.Fatalf("article %s has no date; the listing would show a blank date cell", item.Slug)
		}
	}
}

func TestArticlePatchHTTP_ScheduledFilterCombinesWithTitleSearch(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedScheduledFilterArticles(t, conn)

	items := listArticlesAsEditor(t, conn, "?status=scheduled&title=campus&sort_by=published_date&sort_direction=desc")

	got := slugsOf(items)
	if len(got) != 1 || got[0] != "scheduled-campus-story" {
		t.Fatalf("scheduled + title search returned %v, want only scheduled-campus-story", got)
	}
}

func TestArticlePatchHTTP_ScheduledFilterHiddenFromAnonymousCallers(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedScheduledFilterArticles(t, conn)

	req := httptest.NewRequest(http.MethodGet, "/v1/articles?status=scheduled", nil)
	rec := httptest.NewRecorder()
	GetArticles(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload models.ArticlesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, item := range payload.Articles {
		if item.Slug != "published-campus-story" {
			t.Fatalf("anonymous caller saw %q; unpublished headlines must not leak", item.Slug)
		}
	}
}
