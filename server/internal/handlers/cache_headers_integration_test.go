package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/middleware"
	"server/internal/models"
)

// The unit tests around setPublicReadCache prove the rule; these prove the
// handlers actually call it. The distinction matters because the failure is
// silent in both directions: a missing call just means no caching, and a call
// on the wrong branch means a shared cache can serve an editor's drafts to a
// reader.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run CacheHeaders -v
func TestCacheHeadersPublicListingIsCacheable(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, UTC_TIMESTAMP())",
		"Cacheable story", "cacheable-story", "Body", `["News"]`,
	); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	rec := httptest.NewRecorder()
	GetArticles(conn).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/articles?limit=5", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != publicReadCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, publicReadCacheControl)
	}
}

// The one that matters: the editor's copy of the same URL carries drafts and
// soft-deleted rows, so it must never be marked public.
func TestCacheHeadersEditorListingIsNotCacheable(t *testing.T) {
	conn := articlePatchTestDB(t)
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, `text`, categories, pub_date) VALUES (?, ?, ?, ?, NULL)",
		"Unpublished draft", "unpublished-draft", "Body", `["News"]`,
	); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/articles?limit=5", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Name: "Editor", Role: models.RoleEditor}))
	rec := httptest.NewRecorder()
	GetArticles(conn).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// The draft has to actually be in the body, or this asserts nothing.
	if !strings.Contains(rec.Body.String(), "unpublished-draft") {
		t.Fatal("the editor listing did not include the draft; this test would pass vacuously")
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "public") {
		t.Errorf("Cache-Control = %q on a response carrying an unpublished headline", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != uncacheableCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, uncacheableCacheControl)
	}
}

// A 404 must not be cached: a slug that is wrong for sixty seconds because a
// reader hit it before the article published is a support ticket.
func TestCacheHeadersNotFoundIsNotCacheable(t *testing.T) {
	conn := articlePatchTestDB(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/articles/no-such-article", nil)
	req.SetPathValue("slug", "no-such-article")
	rec := httptest.NewRecorder()
	GetArticle(conn).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q on a 404, want none", got)
	}
}
