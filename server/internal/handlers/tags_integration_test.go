package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
)

// The suggestions are aggregated out of the articles table itself, so the only
// way to know the ranking is right is to put articles in a real MariaDB and
// read the endpoint back. Skips unless CMS_TEST_DSN is set.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run PopularTagsHTTP -v
func seedTaggedArticles(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()

	rows := []struct {
		slug     string
		tags     string
		archived any
	}{
		{"tagged-one", `["triangle","drexel","Title IX"]`, nil},
		{"tagged-two", `["triangle","Drexel"]`, nil},
		// A draft: unpublished, but its tags are still tags the desk uses.
		{"tagged-draft", `["triangle"]`, nil},
		// Archived, so its tags must not reach the suggestion list at all.
		{"tagged-archived", `["retracted"]`, "2026-01-01 00:00:00"},
		{"untagged", "", nil},
	}
	for _, row := range rows {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO articles (title, slug, `text`, authors, tags, archived_at, creation_date) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())",
			row.slug, row.slug, "Body", `["Rui Zhao"]`, row.tags, row.archived,
		); err != nil {
			t.Fatalf("seed %s: %v", row.slug, err)
		}
	}
	// The ranking is cached for minutes at a time; a test that seeds rows has
	// to clear it or it reads whatever an earlier test left behind.
	db.InvalidatePopularTags()
	t.Cleanup(db.InvalidatePopularTags)
}

func getPopularTags(t *testing.T, conn *sql.DB, query string) []db.PopularTag {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/tags/popular"+query, nil)
	rec := httptest.NewRecorder()

	GetPopularTags(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload []db.PopularTag
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestPopularTagsHTTP_RanksTagsAcrossTheArchive(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	tags := getPopularTags(t, conn, "")

	// One entry for drexel, not two: the seed spells it both ways, and the
	// suggestions fold case. The two spellings are equally common here, so the
	// displayed one is the tie-break -- lexicographically first.
	want := []db.PopularTag{
		{Name: "triangle", Uses: 3},
		{Name: "Drexel", Uses: 2},
		{Name: "Title IX", Uses: 1},
	}
	if len(tags) != len(want) {
		t.Fatalf("got %+v, want %+v", tags, want)
	}
	for i, expected := range want {
		if tags[i] != expected {
			t.Errorf("position %d: got %+v, want %+v", i, tags[i], expected)
		}
	}
}

// Archived articles are excluded, so a retracted story cannot keep suggesting
// its tags to the next person writing one.
func TestPopularTagsHTTP_ExcludesArchivedArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	for _, tag := range getPopularTags(t, conn, "") {
		if tag.Name == "retracted" {
			t.Fatalf("archived article's tag reached the suggestions: %+v", tag)
		}
	}
}

func TestPopularTagsHTTP_HonoursTheLimit(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	tags := getPopularTags(t, conn, "?limit=2")

	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2: %+v", len(tags), tags)
	}
	if tags[0].Name != "triangle" {
		t.Errorf("got %q first, want the most-used tag %q", tags[0].Name, "triangle")
	}
}

func TestPopularTagsHTTP_RejectsAnUnusableLimit(t *testing.T) {
	conn := articlePatchTestDB(t)

	for _, limit := range []string{"0", "-3", "many"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/tags/popular?limit="+limit, nil)
		rec := httptest.NewRecorder()
		GetPopularTags(conn).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: status = %d, want 400", limit, rec.Code)
		}
	}
}
