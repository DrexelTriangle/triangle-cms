package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
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

// TestLegacySubsectionSeedRunsOnceAndStaysHidden covers the three properties
// the WordPress sub-category seed has to have.
//
// Hidden, because 48 more links across seven section pages is not navigation.
// Once, because these rows are editable and a deploy must not undo an editor's
// deletion. And skipping a row whose slug is taken, because an existing row is
// somebody's, not the seed's.
func TestLegacySubsectionSeedRunsOnceAndStaysHidden(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"DELETE FROM cms_settings WHERE key_name = 'legacy_subsections_seeded'",
	); err != nil {
		t.Fatalf("clear the seed flag: %v", err)
	}

	// An empty taxonomy means the sections have not been imported yet, not that
	// there is nothing to do. Seeding here would burn the one run and file 48
	// rows under parents that do not exist.
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	if count := countRows(t, conn, "SELECT COUNT(*) FROM site_taxonomy"); count != 0 {
		t.Fatalf("seeded %d rows into an empty taxonomy, want none until the sections exist", count)
	}

	insertTaxonomyRow(t, conn, 1, "section", "sports", "Sports")
	insertTaxonomyRow(t, conn, 2, "section", "entertainment", "Entertainment")
	// An existing row the seed also knows about, deliberately visible, to prove
	// the seed leaves it alone rather than hiding or rewriting it.
	insertTaxonomyRow(t, conn, 3, "subsection", "tennis", "Tennis")
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET parent_slug = 'sports' WHERE slug = 'tennis'",
	); err != nil {
		t.Fatalf("parent the existing row: %v", err)
	}

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	// Everything under the two sections that exist, and nothing under the ones
	// that do not.
	seeded := countRows(t, conn, "SELECT COUNT(*) FROM site_taxonomy WHERE kind = 'subsection'")
	var want int
	for _, item := range legacySubsections {
		if (item.Parent == "sports" || item.Parent == "entertainment") && item.Slug != "tennis" {
			want++
		}
	}
	if seeded != int64(want+1) { // +1 for the pre-existing tennis row
		t.Fatalf("seeded %d subsections, want %d", seeded, want+1)
	}
	if visible := countRows(t, conn,
		"SELECT COUNT(*) FROM site_taxonomy WHERE kind = 'subsection' AND is_visible = 1",
	); visible != 1 {
		t.Errorf("%d visible subsections, want only the pre-existing one -- the seed must arrive hidden", visible)
	}

	// Each seeded row carries its exact category title, so matching does not
	// depend on the slug happening to derive it.
	if got := CategoryMatchValues("street-style"); !containsValue(got, "street style") {
		t.Errorf("values = %v, want the seeded category title", got)
	}
	// A slug that does not derive its own category is exactly why the seed
	// writes the title as an alias -- but only for rows it actually created,
	// and this one's parent section is absent here.
	if got := CategoryMatchValues("aint-that-something-with-brandon-liz"); containsValue(got, "ain't that something with brandon & liz") {
		t.Errorf("values = %v, want no alias -- that row was skipped for a missing parent", got)
	}

	// The run-once guard: a deleted row stays deleted across a restart.
	if _, err := conn.ExecContext(ctx, "DELETE FROM site_taxonomy WHERE slug = 'crew'"); err != nil {
		t.Fatalf("delete a seeded row: %v", err)
	}
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("third ensure: %v", err)
	}
	if count := countRows(t, conn, "SELECT COUNT(*) FROM site_taxonomy WHERE slug = 'crew'"); count != 0 {
		t.Error("a deleted seed row came back on the next start")
	}
}

func countRows(t *testing.T, conn *sql.DB, query string) int64 {
	t.Helper()
	var count int64
	if err := conn.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return count
}

// TestFoodSubsectionSeedMovesTheReviewCategories covers the migration that the
// third level exists for: Food appears under A&E, and the three review
// categories move from beside it to under it.
//
// The run-once half matters as much as the move. These rows are editable, so an
// editor who pulls Restaurant Reviews back out to the section must not find it
// under Food again after the next deploy.
func TestFoodSubsectionSeedMovesTheReviewCategories(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	for _, key := range []string{"legacy_subsections_seeded", "food_subsection_seeded"} {
		if _, err := conn.ExecContext(ctx, "DELETE FROM cms_settings WHERE key_name = ?", key); err != nil {
			t.Fatalf("clear %s: %v", key, err)
		}
	}

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	insertTaxonomyRow(t, conn, 1, "section", "entertainment", "Entertainment")
	// Cooking is a current, visible subsection rather than a legacy WordPress
	// category, so nothing else in this boot would create it. It is here for
	// the same reason production has it: the seed has to move a live row, not
	// just the ones it seeded a moment earlier.
	insertTaxonomyRow(t, conn, 2, "subsection", "cooking", "Cooking")
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET parent_slug = 'entertainment', is_visible = 1 WHERE slug = 'cooking'",
	); err != nil {
		t.Fatalf("parent the cooking row: %v", err)
	}

	// One boot does both: the legacy seeding creates the review subsections,
	// and the Food seed moves them. Order is what makes that work.
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	parentOf := func(slug string) string {
		t.Helper()
		var parent sql.NullString
		if err := conn.QueryRowContext(ctx,
			"SELECT parent_slug FROM site_taxonomy WHERE slug = ?", slug,
		).Scan(&parent); err != nil {
			t.Fatalf("read parent of %s: %v", slug, err)
		}
		return parent.String
	}

	if got := parentOf("food"); got != "entertainment" {
		t.Errorf("food parent = %q, want entertainment", got)
	}
	for _, child := range foodSubsectionChildren {
		if got := parentOf(child); got != "food" {
			t.Errorf("%s parent = %q, want food", child, got)
		}
	}

	// Food is a heading the desk asked for, so unlike the legacy rows it
	// arrives with a link.
	var visible int
	if err := conn.QueryRowContext(ctx,
		"SELECT is_visible FROM site_taxonomy WHERE slug = 'food'",
	).Scan(&visible); err != nil {
		t.Fatalf("read food visibility: %v", err)
	}
	if visible != 1 {
		t.Error("food seeded hidden, want visible -- it is the entry A&E's strip is meant to gain")
	}

	// An editor moves one back, and a restart leaves it where they put it.
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET parent_slug = 'entertainment' WHERE slug = 'wine-reviews'",
	); err != nil {
		t.Fatalf("move a row back: %v", err)
	}
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("third ensure: %v", err)
	}
	if got := parentOf("wine-reviews"); got != "entertainment" {
		t.Errorf("wine-reviews parent = %q, want entertainment -- the seed re-ran over an editor's change", got)
	}
}

// TestEntertainmentVisibilitySeedRunsOnce covers the strip changes the desk
// asked for: Listicles and Books lose their link, TV gains one.
//
// The run-once half is the point. The strip is curated from the sections
// screen, so an editor who puts Books back must not find it hidden again on the
// next deploy.
func TestEntertainmentVisibilitySeedRunsOnce(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	for _, key := range []string{"legacy_subsections_seeded", "entertainment_visibility_seeded"} {
		if _, err := conn.ExecContext(ctx, "DELETE FROM cms_settings WHERE key_name = ?", key); err != nil {
			t.Fatalf("clear %s: %v", key, err)
		}
	}

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	insertTaxonomyRow(t, conn, 1, "section", "entertainment", "Entertainment")
	// Listicles and Books are current, visible subsections; TV arrives hidden
	// from the legacy seeding, which runs in the same boot.
	for id, slug := range map[int64]string{2: "listicles", 3: "books"} {
		insertTaxonomyRow(t, conn, id, "subsection", slug, strings.ToUpper(slug[:1])+slug[1:])
		if _, err := conn.ExecContext(ctx,
			"UPDATE site_taxonomy SET parent_slug = 'entertainment', is_visible = 1 WHERE slug = ?", slug,
		); err != nil {
			t.Fatalf("parent %s: %v", slug, err)
		}
	}

	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	visibilityOf := func(slug string) int {
		t.Helper()
		var visible int
		if err := conn.QueryRowContext(ctx,
			"SELECT is_visible FROM site_taxonomy WHERE slug = ?", slug,
		).Scan(&visible); err != nil {
			t.Fatalf("read visibility of %s: %v", slug, err)
		}
		return visible
	}

	for slug, want := range entertainmentVisibility {
		got := visibilityOf(slug)
		if (got == 1) != want {
			t.Errorf("%s is_visible = %d, want %v", slug, got, want)
		}
	}

	// Hiding is only ever about the link: TV keeps its articles either way, and
	// Books keeps its page. Prove the seed touched nothing else.
	if parent := func() string {
		var p sql.NullString
		if err := conn.QueryRowContext(ctx, "SELECT parent_slug FROM site_taxonomy WHERE slug = 'books'").Scan(&p); err != nil {
			t.Fatalf("read books parent: %v", err)
		}
		return p.String
	}(); parent != "entertainment" {
		t.Errorf("books parent = %q, want entertainment -- hiding must not re-file a row", parent)
	}

	// An editor puts Books back, and a restart leaves it visible.
	if _, err := conn.ExecContext(ctx,
		"UPDATE site_taxonomy SET is_visible = 1 WHERE slug = 'books'",
	); err != nil {
		t.Fatalf("unhide books: %v", err)
	}
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("third ensure: %v", err)
	}
	if got := visibilityOf("books"); got != 1 {
		t.Errorf("books is_visible = %d after a restart, want 1 -- the seed re-ran over an editor's change", got)
	}
}

// TestFooterDefaultFollowsTheTaxonomy is the point of generating the footer:
// the desk curates the section strip, and the footer follows without anyone
// editing a second list.
//
// It covers the three things that make it "one layer of depth": a section's
// direct children are listed, a hidden one is not, and a grandchild is not
// either -- the tree is three levels now, and a footer that recursed would
// print the archive.
func TestFooterDefaultFollowsTheTaxonomy(t *testing.T) {
	conn := taxonomyTestDB(t)
	ctx := context.Background()

	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	if err := EnsureTaxonomyTable(ctx, conn); err != nil {
		t.Fatalf("ensure taxonomy table: %v", err)
	}
	t.Cleanup(InvalidateGeneratedFooter)

	insertTaxonomyRow(t, conn, 1, "section", "entertainment", "Entertainment")
	// Movies is linked, Books is hidden, and Beer Reviews is a grandchild under
	// the visible Food row.
	for _, row := range []struct {
		id      int64
		slug    string
		title   string
		parent  string
		visible int
	}{
		{2, "movies", "Movies", "entertainment", 1},
		{3, "books", "Books", "entertainment", 0},
		{4, "food", "Food", "entertainment", 1},
		{5, "beer-reviews", "Beer Reviews", "food", 1},
	} {
		insertTaxonomyRow(t, conn, row.id, "subsection", row.slug, row.title)
		if _, err := conn.ExecContext(ctx,
			"UPDATE site_taxonomy SET parent_slug = ?, is_visible = ? WHERE slug = ?",
			row.parent, row.visible, row.slug,
		); err != nil {
			t.Fatalf("place %s: %v", row.slug, err)
		}
	}

	labels := func() []string {
		t.Helper()
		settings, err := GetFooterSettings(ctx, conn)
		if err != nil {
			t.Fatalf("get footer settings: %v", err)
		}
		var found []string
		for _, column := range settings.Columns {
			for _, entry := range column.Entries {
				found = append(found, entry.Label)
			}
		}
		return found
	}

	got := labels()
	has := func(label string) bool {
		for _, entry := range got {
			if entry == label {
				return true
			}
		}
		return false
	}

	if !has("Movies") || !has("Food") {
		t.Errorf("footer = %v, want the visible subsections listed", got)
	}
	if has("Books") {
		t.Error("a hidden subsection reached the footer; the strip toggle has to drive both")
	}
	if has("Beer Reviews") {
		t.Error("a grandchild reached the footer; the footer is one layer deep")
	}
	// The literal blocks survive alongside the generated ones.
	if !has("The Rectangle") || !has("Contact Us") {
		t.Errorf("footer = %v, want the non-taxonomy links kept", got)
	}

	// Hiding Movies removes it, once the cached columns are dropped the way a
	// taxonomy write does.
	if _, err := conn.ExecContext(ctx, "UPDATE site_taxonomy SET is_visible = 0 WHERE slug = 'movies'"); err != nil {
		t.Fatalf("hide movies: %v", err)
	}
	InvalidateGeneratedFooter()
	if got = labels(); has("Movies") {
		t.Errorf("footer = %v, want Movies gone after it was hidden", got)
	}

	// A stored menu still wins: generating is the DEFAULT, not an override of
	// whatever an editor saved in the settings screen.
	if err := SetFooterSettings(ctx, conn, models.FooterSettings{Columns: []models.FooterColumn{
		{Entries: []models.FooterEntry{{Kind: models.FooterEntryLink, Label: "Only This", Href: "/only"}}},
	}}); err != nil {
		t.Fatalf("store a custom footer: %v", err)
	}
	if got = labels(); len(got) != 1 || got[0] != "Only This" {
		t.Errorf("footer = %v, want the stored menu to win over the generated default", got)
	}
}
