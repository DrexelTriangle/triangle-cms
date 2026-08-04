package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/models"
)

// Exercises the sections management screen end to end against a real MariaDB:
// the alias a section is saved with has to reach the article matcher, or the
// screen looks like it works while the section stays empty.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run TaxonomyHTTP -v
func taxonomyHTTPTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping taxonomy handler integration test")
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
		t.Fatal("timed out waiting for the taxonomy test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		_ = conn.Close()
	})

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS site_taxonomy"); err != nil {
		t.Fatalf("drop site_taxonomy: %v", err)
	}
	if err := db.EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	// Deletion asks the articles table for a live count, so every test needs
	// one even when it files no articles.
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT PRIMARY KEY,
			categories JSON NULL,
			archived_at DATETIME NULL,
			pub_date DATETIME NULL
		)`); err != nil {
		t.Fatalf("create articles: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS articles") })
	return conn
}

func taxonomyRequest(t *testing.T, handler http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = strings.NewReader(string(payload))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	req.SetPathValue("type", pathSegment(target, 2))
	req.SetPathValue("slug", pathSegment(target, 3))
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}

func pathSegment(target string, index int) string {
	parts := strings.Split(strings.TrimPrefix(target, "/"), "/")
	if index < len(parts) {
		return parts[index]
	}
	return ""
}

func getTaxonomyItems(t *testing.T, conn *sql.DB) []models.TaxonomyItem {
	t.Helper()
	recorder := taxonomyRequest(t, GetTaxonomy(conn), http.MethodGet, "/v1/taxonomy", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /v1/taxonomy = %d: %s", recorder.Code, recorder.Body.String())
	}
	var items []models.TaxonomyItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode taxonomy list: %v", err)
	}
	return items
}

func findItem(t *testing.T, items []models.TaxonomyItem, slug string) models.TaxonomyItem {
	t.Helper()
	for _, item := range items {
		if item.Slug == slug {
			return item
		}
	}
	t.Fatalf("no taxonomy item with slug %q in %v", slug, items)
	return models.TaxonomyItem{}
}

// TestTaxonomyHTTPAliasFixesAnEmptySection is the whole point of the sections
// screen: an editor sees a section matching nothing, types the real category
// name, saves, and the section starts matching. If any link in that chain is
// broken the screen still "works" while the section stays empty.
func TestTaxonomyHTTPAliasFixesAnEmptySection(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)

	// A section whose slug is not the category its articles carry -- the
	// Entertainment situation, which is the case that motivated aliases.
	created := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":            "section",
		"slug":            "food",
		"canonical_title": "Food",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /v1/taxonomy = %d: %s", created.Code, created.Body.String())
	}

	// Nothing matches "Food" yet, because the articles say "Restaurant Reviews".
	if got := db.CategoryMatchPatterns("food"); len(got) != 1 {
		t.Fatalf("patterns = %v, want only the slug's own", got)
	}

	// The editor saves the real category name. This is the exact payload the
	// sections screen sends.
	updated := taxonomyRequest(t, PutTaxonomyItem(conn), http.MethodPut, "/v1/taxonomy/section/food", map[string]any{
		"slug":             "food",
		"canonical_title":  "Food",
		"parent_slug":      nil,
		"category_aliases": []string{"Restaurant Reviews", "Beer Reviews"},
	})
	if updated.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d: %s", updated.Code, updated.Body.String())
	}

	// The matcher must see it immediately -- no restart. This is what the
	// cache refresh on write buys.
	patterns := db.CategoryMatchPatterns("food")
	for _, want := range []string{`%"restaurant reviews"%`, `%"beer reviews"%`} {
		found := false
		for _, pattern := range patterns {
			if pattern == want {
				found = true
			}
		}
		if !found {
			t.Errorf("patterns %v missing %q right after the save", patterns, want)
		}
	}

	// And the screen must read them back, or the editor's next save wipes them.
	item := findItem(t, getTaxonomyItems(t, conn), "food")
	if len(item.CategoryAliases) != 2 || item.CategoryAliases[0] != "Restaurant Reviews" {
		t.Errorf("GET returned aliases %v, want the two just saved", item.CategoryAliases)
	}
}

// TestTaxonomyHTTPOmittedAliasesSurviveAnEdit guards the compatibility rule: a
// client that predates this field (or any edit that does not touch aliases)
// must not silently clear them.
func TestTaxonomyHTTPOmittedAliasesSurviveAnEdit(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)

	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":             "section",
		"slug":             "food",
		"canonical_title":  "Food",
		"category_aliases": []string{"Restaurant Reviews"},
	}).Code; code != http.StatusCreated {
		t.Fatalf("POST = %d", code)
	}

	// Rename only -- no category_aliases key at all.
	updated := taxonomyRequest(t, PutTaxonomyItem(conn), http.MethodPut, "/v1/taxonomy/section/food", map[string]any{
		"slug":            "food",
		"canonical_title": "Food & Drink",
		"parent_slug":     nil,
	})
	if updated.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d: %s", updated.Code, updated.Body.String())
	}

	item := findItem(t, getTaxonomyItems(t, conn), "food")
	if len(item.CategoryAliases) != 1 || item.CategoryAliases[0] != "Restaurant Reviews" {
		t.Errorf("aliases = %v, want them untouched by an unrelated edit", item.CategoryAliases)
	}
}

// TestTaxonomyHTTPExplicitEmptyClearsAliases is the other half of that rule:
// an editor who empties the field must actually get an empty field.
func TestTaxonomyHTTPExplicitEmptyClearsAliases(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)

	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":             "section",
		"slug":             "food",
		"canonical_title":  "Food",
		"category_aliases": []string{"Restaurant Reviews"},
	}).Code; code != http.StatusCreated {
		t.Fatalf("POST = %d", code)
	}

	updated := taxonomyRequest(t, PutTaxonomyItem(conn), http.MethodPut, "/v1/taxonomy/section/food", map[string]any{
		"slug":             "food",
		"canonical_title":  "Food",
		"parent_slug":      nil,
		"category_aliases": []string{},
	})
	if updated.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d: %s", updated.Code, updated.Body.String())
	}

	item := findItem(t, getTaxonomyItems(t, conn), "food")
	if len(item.CategoryAliases) != 0 {
		t.Errorf("aliases = %v, want cleared", item.CategoryAliases)
	}
	if got := db.CategoryMatchPatterns("food"); len(got) != 1 {
		t.Errorf("patterns = %v, want the alias gone from matching too", got)
	}
}

// TestTaxonomyHTTPAliasesAreNotHTMLEscaped pins the escaping rule at the layer
// editors actually touch: an ampersand typed into the sections screen must be
// stored as an ampersand, or the alias silently matches nothing.
func TestTaxonomyHTTPAliasesAreNotHTMLEscaped(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)

	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":             "section",
		"slug":             "entertainment",
		"canonical_title":  "Entertainment",
		"category_aliases": []string{"Arts & Entertainment"},
	}).Code; code != http.StatusCreated {
		t.Fatalf("POST = %d", code)
	}

	var stored string
	if err := conn.QueryRowContext(context.Background(),
		"SELECT category_aliases FROM site_taxonomy WHERE slug = 'entertainment'",
	).Scan(&stored); err != nil {
		t.Fatalf("read stored aliases: %v", err)
	}
	if want := `["Arts & Entertainment"]`; stored != want {
		t.Fatalf("stored %s, want %s -- an escaped ampersand matches no article", stored, want)
	}
}

// TestTaxonomyHTTPDeleteRefusesWhenArticlesExist covers the guard that keeps
// deletion safe now that editors can do it.
func TestTaxonomyHTTPDeleteRefusesWhenArticlesExist(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)
	ctx := context.Background()

	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":            "section",
		"slug":            "sports",
		"canonical_title": "Sports",
	}).Code; code != http.StatusCreated {
		t.Fatalf("POST = %d", code)
	}

	// An empty section deletes fine.
	empty := taxonomyRequest(t, DeleteTaxonomyItem(conn), http.MethodDelete, "/v1/taxonomy/section/sports", nil)
	if empty.Code != http.StatusNoContent {
		t.Fatalf("delete of an empty section = %d: %s", empty.Code, empty.Body.String())
	}

	// Recreate it, this time with an article filed under it. site_taxonomy's
	// article_count is still 0 -- nothing has rebuilt it -- so this is exactly
	// the stale-count case the live count exists to catch.
	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":            "section",
		"slug":            "sports",
		"canonical_title": "Sports",
	}).Code; code != http.StatusCreated {
		t.Fatalf("re-POST = %d", code)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO articles (id, categories, pub_date) VALUES (1, '["Sports"]', UTC_TIMESTAMP())`,
	); err != nil {
		t.Fatalf("insert article: %v", err)
	}

	var storedCount int64
	if err := conn.QueryRowContext(ctx,
		"SELECT article_count FROM site_taxonomy WHERE slug = 'sports'",
	).Scan(&storedCount); err != nil {
		t.Fatalf("read stored count: %v", err)
	}
	if storedCount != 0 {
		t.Fatalf("stored count = %d, want a stale 0 for this test to mean anything", storedCount)
	}

	refused := taxonomyRequest(t, DeleteTaxonomyItem(conn), http.MethodDelete, "/v1/taxonomy/section/sports", nil)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("delete of a section with articles = %d, want 400: %s", refused.Code, refused.Body.String())
	}

	var remaining int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM site_taxonomy WHERE slug = 'sports'",
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Error("the section was deleted despite having articles")
	}
}

// TestTaxonomyHTTPDeleteRefusesSectionWithSubsections keeps the other half of
// "empty" honest: a section that still parents subsections is not empty.
func TestTaxonomyHTTPDeleteRefusesSectionWithSubsections(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)

	for _, body := range []map[string]any{
		{"type": "section", "slug": "sports", "canonical_title": "Sports"},
		{"type": "subsection", "slug": "squash", "canonical_title": "Squash", "parent_slug": "sports"},
	} {
		if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", body).Code; code != http.StatusCreated {
			t.Fatalf("POST %v = %d", body, code)
		}
	}

	refused := taxonomyRequest(t, DeleteTaxonomyItem(conn), http.MethodDelete, "/v1/taxonomy/section/sports", nil)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("delete of a parent section = %d, want 400: %s", refused.Code, refused.Body.String())
	}
}

// TestTaxonomyHTTPAliasSaveUpdatesTheDisplayedCount closes the loop the sections
// screen actually shows. Matching goes live the moment the alias cache reloads,
// but article_count is stored, so without a recount the editor's fix works on
// the site while the screen still reads 0 -- a successful fix that looks failed.
func TestTaxonomyHTTPAliasSaveUpdatesTheDisplayedCount(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx,
		`INSERT INTO articles (id, categories, pub_date) VALUES
			(1, '["Restaurant Reviews"]', UTC_TIMESTAMP()),
			(2, '["Restaurant Reviews"]', UTC_TIMESTAMP()),
			(3, '["News"]', UTC_TIMESTAMP())`,
	); err != nil {
		t.Fatalf("insert articles: %v", err)
	}

	if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", map[string]any{
		"type":            "section",
		"slug":            "food",
		"canonical_title": "Food",
	}).Code; code != http.StatusCreated {
		t.Fatalf("POST = %d", code)
	}

	// Nothing is filed under "Food", so the screen shows 0 and flags it.
	if got := findItem(t, getTaxonomyItems(t, conn), "food").ArticleCount; got != 0 {
		t.Fatalf("count before the fix = %d, want 0", got)
	}

	// The editor supplies the real category name.
	if code := taxonomyRequest(t, PutTaxonomyItem(conn), http.MethodPut, "/v1/taxonomy/section/food", map[string]any{
		"slug":             "food",
		"canonical_title":  "Food",
		"parent_slug":      nil,
		"category_aliases": []string{"Restaurant Reviews"},
	}).Code; code != http.StatusNoContent {
		t.Fatalf("PUT = %d", code)
	}

	// ...and the number must be right on the very next load, with no rebuild
	// and no restart.
	if got := findItem(t, getTaxonomyItems(t, conn), "food").ArticleCount; got != 2 {
		t.Errorf("count after the fix = %d, want 2 -- the save did not recount", got)
	}
}

// TestTaxonomyHTTPSubsectionSaveRecountsItsParent covers the blast radius: a
// section matches its own slug OR any child's, so fixing a child moves the
// parent's number too.
func TestTaxonomyHTTPSubsectionSaveRecountsItsParent(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)
	ctx := context.Background()

	if _, err := conn.ExecContext(ctx,
		`INSERT INTO articles (id, categories, pub_date) VALUES
			(1, '["Beer Reviews"]', UTC_TIMESTAMP())`,
	); err != nil {
		t.Fatalf("insert articles: %v", err)
	}

	for _, body := range []map[string]any{
		{"type": "section", "slug": "food", "canonical_title": "Food"},
		{"type": "subsection", "slug": "drinks", "canonical_title": "Drinks", "parent_slug": "food"},
	} {
		if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", body).Code; code != http.StatusCreated {
			t.Fatalf("POST %v = %d", body, code)
		}
	}

	if code := taxonomyRequest(t, PutTaxonomyItem(conn), http.MethodPut, "/v1/taxonomy/subsection/drinks", map[string]any{
		"slug":             "drinks",
		"canonical_title":  "Drinks",
		"parent_slug":      "food",
		"category_aliases": []string{"Beer Reviews"},
	}).Code; code != http.StatusNoContent {
		t.Fatalf("PUT = %d", code)
	}

	items := getTaxonomyItems(t, conn)
	if got := findItem(t, items, "drinks").ArticleCount; got != 1 {
		t.Errorf("subsection count = %d, want 1", got)
	}
	if got := findItem(t, items, "food").ArticleCount; got != 1 {
		t.Errorf("parent section count = %d, want 1 -- the parent was not recounted", got)
	}
}

// TestTaxonomyHTTPDeleteRecountsTheParent covers the case where the parent
// cannot be resolved after the fact, because the child row is gone by then.
//
// Deleting an EMPTY child cannot move a correct parent count -- an empty child
// contributes nothing -- so the parent's stored count is staled first. If the
// delete recounts it, the stale number is corrected; if it does not, the stale
// number survives.
func TestTaxonomyHTTPDeleteRecountsTheParent(t *testing.T) {
	conn := taxonomyHTTPTestDB(t)
	ctx := context.Background()

	for _, body := range []map[string]any{
		{"type": "section", "slug": "food", "canonical_title": "Food"},
		{"type": "subsection", "slug": "drinks", "canonical_title": "Drinks", "parent_slug": "food"},
	} {
		if code := taxonomyRequest(t, PostTaxonomy(conn), http.MethodPost, "/v1/taxonomy", body).Code; code != http.StatusCreated {
			t.Fatalf("POST %v = %d", body, code)
		}
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO articles (id, categories, pub_date) VALUES (1, '["Food"]', UTC_TIMESTAMP())`,
	); err != nil {
		t.Fatalf("insert article: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET article_count = 99 WHERE slug = 'food'",
	); err != nil {
		t.Fatalf("stale the parent count: %v", err)
	}

	if code := taxonomyRequest(t, DeleteTaxonomyItem(conn), http.MethodDelete, "/v1/taxonomy/subsection/drinks", nil).Code; code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}

	if got := findItem(t, getTaxonomyItems(t, conn), "food").ArticleCount; got != 1 {
		t.Errorf("parent count after deleting a child = %d, want 1 -- the parent was not recounted", got)
	}
}
