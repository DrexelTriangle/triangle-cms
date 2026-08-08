package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// A section the seed has no defaults for, for the "keeps no aliases" case.
// Which slugs are unseeded changes as orphaned categories get filed -- news
// gained "Paid Post" -- so the choice is guarded by requireUnseeded rather than
// assumed, or the assertion would quietly start testing nothing.
const unseededSlug = "photo"

func requireUnseeded(t *testing.T, slug string) {
	t.Helper()
	if _, seeded := defaultCategoryAliases[slug]; seeded {
		t.Fatalf("%q now has seeded aliases; pick another slug for the no-defaults case", slug)
	}
}

// These need a real MariaDB, because the whole point is the column, the seed
// and the cache reload agreeing with each other. They skip unless CMS_TEST_DSN
// is set, so CI without a database stays green.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/tax_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run TaxonomyAlias -v
func taxonomyTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping taxonomy database integration test")
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
	acquireMediaTestLock(ctx, t, conn)

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS site_taxonomy"); err != nil {
		t.Fatalf("drop site_taxonomy: %v", err)
	}
	t.Cleanup(func() {
		categoryAliasMu.Lock()
		categoryAliasBySlug = map[string][]string{}
		categorySlugByTitle = map[string]string{}
		categoryAliasMu.Unlock()
	})
	return conn
}

func insertTaxonomyRow(t *testing.T, conn *sql.DB, id int64, kind, slug, title string) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO site_taxonomy (id, kind, slug, canonical_title) VALUES (?, ?, ?, ?)",
		id, kind, slug, title,
	); err != nil {
		t.Fatalf("insert %s: %v", slug, err)
	}
}

func TestTaxonomyAliasSeedAndCacheRoundTrip(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	// A first EnsureTaxonomyTable creates the table; the rows land after, the
	// way an existing database looks when this version first starts.
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	requireUnseeded(t, unseededSlug)
	insertTaxonomyRow(t, conn, 1, "section", "entertainment", "Entertainment")
	insertTaxonomyRow(t, conn, 2, "section", unseededSlug, "Photo")

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	// entertainment must have picked up its seeded defaults. Asserted against
	// defaultCategoryAliases rather than a literal list: the map grows every
	// time another orphaned category is filed, and a test that pins the
	// contents fails on the filing rather than on anything being broken.
	values := CategoryMatchValues("entertainment")
	for _, alias := range defaultCategoryAliases["entertainment"] {
		want := strings.ToLower(alias)
		if !containsValue(values, want) {
			t.Errorf("entertainment values %v missing the seeded alias %s", values, want)
		}
	}
	// The aliases must not be HTML-escaped in the column, or one holding an
	// ampersand would never match the articles it names.
	var stored string
	if err := conn.QueryRowContext(ctx,
		"SELECT category_aliases FROM site_taxonomy WHERE slug = 'entertainment'",
	).Scan(&stored); err != nil {
		t.Fatalf("read stored aliases: %v", err)
	}
	want, err := MarshalCategoryJSON(defaultCategoryAliases["entertainment"])
	if err != nil {
		t.Fatalf("marshal expected aliases: %v", err)
	}
	if stored != want {
		t.Errorf("stored aliases = %s, want %s", stored, want)
	}
	if strings.Contains(stored, "\\u0026") {
		t.Errorf("stored aliases are HTML-escaped: %s", stored)
	}

	// A slug with no default keeps no aliases.
	if got := CategoryMatchValues(unseededSlug); len(got) != 1 {
		t.Errorf("%s values = %v, want just its own", unseededSlug, got)
	}
}

func TestTaxonomyAliasSeedDoesNotResurrectClearedAliases(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	insertTaxonomyRow(t, conn, 1, "section", "entertainment", "Entertainment")
	if err := SeedDefaultCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// An editor deliberately clears the aliases. That is [] , not NULL, and a
	// later startup must leave it alone -- otherwise the defaults come back
	// every restart and the edit looks like it never saved.
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET category_aliases = '[]' WHERE slug = 'entertainment'",
	); err != nil {
		t.Fatalf("clear aliases: %v", err)
	}
	if err := SeedDefaultCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	var stored string
	if err := conn.QueryRowContext(ctx,
		"SELECT category_aliases FROM site_taxonomy WHERE slug = 'entertainment'",
	).Scan(&stored); err != nil {
		t.Fatalf("read stored aliases: %v", err)
	}
	if stored != "[]" {
		t.Errorf("stored aliases = %s, want [] -- the seed overwrote a deliberate clear", stored)
	}
}

func TestTaxonomyAliasRefreshPicksUpWrites(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	insertTaxonomyRow(t, conn, 1, "subsection", "movies", "Movies")
	if err := RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := CategoryMatchValues("movies"); len(got) != 1 {
		t.Fatalf("movies values = %v, want just its own", got)
	}

	if _, err := conn.ExecContext(ctx,
		`UPDATE site_taxonomy SET category_aliases = '["Movies I''ve Seen"]' WHERE slug = 'movies'`,
	); err != nil {
		t.Fatalf("write alias: %v", err)
	}
	// Still stale until the cache is told, which is why every write path calls
	// RefreshCategoryAliases.
	if got := CategoryMatchValues("movies"); len(got) != 1 {
		t.Errorf("values changed before a refresh: %v", got)
	}
	if err := RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("refresh after write: %v", err)
	}
	if got := CategoryMatchValues("movies"); !containsValue(got, "movies i've seen") {
		t.Errorf("movies values %v missing the written alias", got)
	}
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRefreshLoadsCategoryTitlesAndPrefersARealPage(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	insertTaxonomyRow(t, conn, 1, "section", "sports", "Sports")
	insertTaxonomyRow(t, conn, 2, "subsection", "mens-basketball", "Men's Basketball")
	if err := RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// A row with no alias at all still has to resolve, which is why the
	// refresh reads every section and subsection rather than only the rows
	// carrying aliases.
	if got, want := CategoryLinkSlug("Men's Basketball"), "mens-basketball"; got != want {
		t.Errorf("CategoryLinkSlug = %q, want %q", got, want)
	}

	// Now let Sports absorb the subsection's own category by alias. The
	// subsection has a page of its own, so the alias must not take the link
	// away from it -- a chip should reach the most specific page that lists
	// the article.
	if _, err := conn.ExecContext(ctx,
		`UPDATE site_taxonomy SET category_aliases = ? WHERE slug = 'sports'`,
		`["Men's Basketball","Men's Lacrosse"]`,
	); err != nil {
		t.Fatalf("write aliases: %v", err)
	}
	if err := RefreshCategoryAliases(ctx, conn); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	if got, want := CategoryLinkSlug("Men's Basketball"), "mens-basketball"; got != want {
		t.Errorf("an alias must not displace a real page: got %q, want %q", got, want)
	}
	// A category with no page of its own does reach the section that absorbed it.
	if got, want := CategoryLinkSlug("Men's Lacrosse"), "sports"; got != want {
		t.Errorf("aliased-only category = %q, want %q", got, want)
	}
}
