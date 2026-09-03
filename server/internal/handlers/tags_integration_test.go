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
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run TagsHTTP -v
func seedTaggedArticles(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()

	rows := []struct {
		slug     string
		tags     string
		archived any
	}{
		{"tagged-one", `["triangle","drexel","Title IX"]`, nil},
		// A beat tag from years ago: used once, nowhere near the popular list,
		// and exactly the kind of spelling nobody retypes correctly.
		{"tagged-beat", `["Men's Lacrosse","lacrosse"]`, nil},
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

func getTags(t *testing.T, conn *sql.DB, query string) []db.PopularTag {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/tags"+query, nil)
	rec := httptest.NewRecorder()

	GetTags(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var payload []db.PopularTag
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestTagsHTTP_RanksTagsAcrossTheArchive(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	tags := getTags(t, conn, "")

	// One entry for drexel, not two: the seed spells it both ways, and the
	// suggestions fold case. The two spellings are equally common here, so the
	// displayed one is the tie-break: lexicographically first.
	want := []db.PopularTag{
		{Name: "triangle", Uses: 3},
		{Name: "Drexel", Uses: 2},
	}
	for i, expected := range want {
		if tags[i] != expected {
			t.Errorf("position %d: got %+v, want %+v", i, tags[i], expected)
		}
	}
}

// The point of the search: a tag used once years ago is unreachable from the
// popular list, and retyping it from memory is how a near-duplicate gets
// coined instead.
func TestTagsHTTP_FindsATagOutsideThePopularList(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	found := getTags(t, conn, "?q=lacrosse")

	names := make([]string, 0, len(found))
	for _, tag := range found {
		names = append(names, tag.Name)
	}
	// Exact match first, then the one that merely contains it, even though
	// both are used exactly as often here.
	want := []string{"lacrosse", "Men's Lacrosse"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i, expected := range want {
		if names[i] != expected {
			t.Errorf("position %d: got %q, want %q", i, names[i], expected)
		}
	}
}

// The editor sends the box's contents, so a query that matches nothing has to
// answer with an empty list rather than falling back to the popular tags --
// suggestions that ignore what was typed read as a broken search.
func TestTagsHTTP_ReturnsNothingForAQueryThatMatchesNothing(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	if found := getTags(t, conn, "?q=zzzznotatag"); len(found) != 0 {
		t.Fatalf("got %+v, want no matches", found)
	}
}

// An empty box means "what is popular", not "match everything against a blank
// string", and the two happen to agree, but only because a blank query short
// circuits before matching.
func TestTagsHTTP_TreatsABlankQueryAsThePopularList(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	found := getTags(t, conn, "?q=%20%20")

	if len(found) == 0 || found[0].Name != "triangle" {
		t.Fatalf("got %+v, want the popular list led by triangle", found)
	}
}

// Archived articles are excluded, so a retracted story cannot keep suggesting
// its tags to the next person writing one.
func TestTagsHTTP_ExcludesArchivedArticles(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	for _, tag := range getTags(t, conn, "") {
		if tag.Name == "retracted" {
			t.Fatalf("archived article's tag reached the suggestions: %+v", tag)
		}
	}
}

func TestTagsHTTP_HonoursTheLimit(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedTaggedArticles(t, conn)

	tags := getTags(t, conn, "?limit=2")

	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2: %+v", len(tags), tags)
	}
	if tags[0].Name != "triangle" {
		t.Errorf("got %q first, want the most-used tag %q", tags[0].Name, "triangle")
	}
}

func TestTagsHTTP_RejectsAnUnusableLimit(t *testing.T) {
	conn := articlePatchTestDB(t)

	for _, limit := range []string{"0", "-3", "many"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/tags?limit="+limit, nil)
		rec := httptest.NewRecorder()
		GetTags(conn).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: status = %d, want 400", limit, rec.Code)
		}
	}
}
