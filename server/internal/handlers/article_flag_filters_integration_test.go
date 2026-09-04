package handlers

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
)

// The Articles screen offers Breaking and Featured as filters, so both have to
// narrow the listing on their own and in combination with the section filter
// the editor may already have set.
//
// The awkward part is the "off" half. Both columns are nullable and the
// WordPress archive leaves them NULL rather than 0, so `breaking_news` = 0
// matches none of it; a filter written that way would answer "not breaking"
// with only the handful of rows the CMS itself has written.
func seedFlagFilterArticles(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	past := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")

	rows := []struct {
		title      string
		slug       string
		categories string
		breaking   any
		priority   any
	}{
		{"Breaking campus story", "breaking-campus-story", `["News"]`, 1, 0},
		{"Breaking sports story", "breaking-sports-story", `["Sports"]`, 1, 1},
		{"Featured campus story", "featured-campus-story", `["News"]`, 0, 1},
		{"Ordinary campus story", "ordinary-campus-story", `["News"]`, 0, 0},
		// The archive shape: neither flag ever written.
		{"Imported campus story", "imported-campus-story", `["News"]`, nil, nil},
	}
	for _, row := range rows {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO articles (title, slug, `text`, authors, categories, pub_date, breaking_news, priority, creation_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())",
			row.title, row.slug, "Body", `["Rui Zhao"]`, row.categories, past, row.breaking, row.priority,
		); err != nil {
			t.Fatalf("seed %s: %v", row.slug, err)
		}
	}

	// One case stacks the flag filter on ?section_slug=, which needs a real
	// section row to validate against and the category index the listing
	// actually matches on. Raw INSERTs bypass both.
	//
	// The harness creates site_taxonomy but does not drop it, so rows survive
	// from whichever test ran before this one in the package. Clearing first is
	// what makes the tree here exactly the tree this test describes -- and a
	// literal id would otherwise collide with a leftover row, which passes when
	// this test runs alone and fails in the suite.
	if _, err := conn.ExecContext(ctx, "DELETE FROM site_taxonomy"); err != nil {
		t.Fatalf("clear site_taxonomy: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO site_taxonomy (id, kind, slug, canonical_title, parent_slug) VALUES (1, 'section', 'news', 'News', NULL)",
	); err != nil {
		t.Fatalf("seed the news section: %v", err)
	}
	// The alias cache is package-level state that outlives a single test, so it
	// has to be reloaded from the table this test just rewrote.
	if err := db.RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("refresh category aliases: %v", err)
	}
	if err := db.RebuildArticleCategories(ctx, conn); err != nil {
		t.Fatalf("rebuild article categories: %v", err)
	}
}

func TestArticleFlagFiltersHTTP(t *testing.T) {
	conn := articlePatchTestDB(t)
	seedFlagFilterArticles(t, conn)

	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "breaking only",
			query: "?breaking=true",
			want:  []string{"breaking-campus-story", "breaking-sports-story"},
		},
		{
			// The NULL row has to be here, or "not breaking" hides the archive.
			name:  "not breaking",
			query: "?breaking=false",
			want:  []string{"featured-campus-story", "imported-campus-story", "ordinary-campus-story"},
		},
		{
			name:  "featured only",
			query: "?featured=true",
			want:  []string{"breaking-sports-story", "featured-campus-story"},
		},
		{
			name:  "not featured",
			query: "?featured=false",
			want:  []string{"breaking-campus-story", "imported-campus-story", "ordinary-campus-story"},
		},
		{
			// Both at once: the article that is breaking AND pinned.
			name:  "breaking and featured",
			query: "?breaking=true&featured=true",
			want:  []string{"breaking-sports-story"},
		},
		{
			// And stacked on the section filter the editor already had set.
			name:  "breaking within a section",
			query: "?breaking=true&section_slug=news",
			want:  []string{"breaking-campus-story"},
		},
		{
			// An unparseable value does not filter, rather than guessing a half.
			name:  "unparseable value does not filter",
			query: "?breaking=maybe",
			want: []string{
				"breaking-campus-story", "breaking-sports-story", "featured-campus-story",
				"imported-campus-story", "ordinary-campus-story",
			},
		},
		{
			// Bare ?breaking is how a form spells true.
			name:  "bare param means true",
			query: "?breaking",
			want:  []string{"breaking-campus-story", "breaking-sports-story"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := slugsOf(listArticlesAsEditor(t, conn, tc.query))
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("%s returned %v, want %v", tc.query, got, tc.want)
			}
			for i, slug := range tc.want {
				if got[i] != slug {
					t.Fatalf("%s returned %v, want %v", tc.query, got, tc.want)
				}
			}
		})
	}
}
