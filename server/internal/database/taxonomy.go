package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

func EnsureTaxonomyTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema("site_taxonomy"))
	if err != nil {
		return err
	}

	if _, err = conn.ExecContext(ctx, `
		ALTER TABLE site_taxonomy
		ADD COLUMN IF NOT EXISTS article_count BIGINT UNSIGNED NOT NULL DEFAULT 0
	`); err != nil {
		return err
	}

	if _, err = conn.ExecContext(ctx, `
		ALTER TABLE site_taxonomy
		ADD COLUMN IF NOT EXISTS category_aliases JSON NULL
	`); err != nil {
		return err
	}

	// Defaults to visible, so every row that predates the column keeps its link
	// in the subsection strip. That is the freeze: the strip on each section
	// page stays exactly what it is today, and everything seeded below arrives
	// hidden.
	if _, err = conn.ExecContext(ctx, `
		ALTER TABLE site_taxonomy
		ADD COLUMN IF NOT EXISTS is_visible TINYINT(1) NOT NULL DEFAULT 1
	`); err != nil {
		return err
	}

	if err = SeedDefaultCategoryAliases(ctx, conn); err != nil {
		return err
	}
	seeded, err := SeedLegacySubsections(ctx, conn)
	if err != nil {
		return err
	}
	// After the legacy seeding, which is what creates the review subsections
	// this moves. On a fresh database both run in the same boot, and the order
	// is the difference between re-parenting three rows and finding none.
	moved, err := SeedFoodSubsection(ctx, conn)
	if err != nil {
		return err
	}
	seeded = append(seeded, moved...)
	// After the Food move as well as the legacy seeding: TV is unhidden here
	// and the legacy seeding is what creates it, while Listicles and Books are
	// left where the move put them. The two seeds touch disjoint rows, so the
	// order between them is about legibility rather than correctness.
	if err = SeedEntertainmentVisibility(ctx, conn); err != nil {
		return err
	}
	if err = RefreshCategoryAliases(ctx, conn); err != nil {
		return err
	}

	if len(seeded) > 0 {
		// After the refresh, because counting reads the alias cache the seed
		// just added to. Not fatal: this runs before the article_categories
		// index is built on a fresh database, and a wrong count is a number on
		// a screen, recoverable by the next rebuild or by any edit to the row.
		if err := RebuildTaxonomyArticleCountsFor(ctx, conn, seeded...); err != nil {
			slog.Warn("failed to count articles for the newly seeded subsections; they will read 0 until the next rebuild", "error", err)
		}
	}
	return nil
}

// Default aliases apply only where category_aliases IS NULL, preserving
// explicitly saved empty arrays. Parent aliases cover deleted legacy subsections.
var defaultCategoryAliases = map[string][]string{
	"from-the-editor":     {"From the Editor's Desk"},
	"happening-in-philly": {"What's Happening in Philly"},

	// Most sports articles also carry a literal "Sports" tag and were never
	// affected, which is why this surfaced as a handful of arbitrary-looking
	// gaps rather than as a whole missing sport. This is the report that
	// exposed the rest.
	"sports": {
		"Men's Lacrosse",
		"Women's Lacrosse",
		"Tennis",
		"Crew",
		"Golf",
		"Softball",
		"Swimming & Diving",
		"Running",
		"Athlete of the Week",
	},

	// The rest of the orphans, filed by what the articles actually are. The
	// same WordPress hierarchy that carried the sports carried these, and
	// losing it stranded 436 published articles on no section page at all.
	//
	// Style is the large one: 306 articles across the beat and its recurring
	// features. It sits under Entertainment rather than Opinion's Lifestyle
	// because it is culture coverage, not opinion writing.
	"entertainment": {
		"Arts & Entertainment",
		"Style", "Street Style", "DIY", "Inside Her Bag", "Store Profile",
		"Beauty Guide", "Style Guide", "Designer Profile", "Fashion Week",
		"TV", "Exhibits", "Reel2Reel", "Features",
		"Restaurant Reviews", "Beer Reviews", "Last Call",
	},

	// Editorials and letters are the section's own voice, so they belong to
	// Opinion even though neither is filed under it.
	"opinion": {"Editorial", "Letters to the Editor", "Commentary"},

	// Columns is where recurring bylined series live, which is what the
	// podcasts are: "Mark and Jair Explain Sports" is a show, not a sports
	// article. Anchored matching keeps it out of Sports despite the name.
	"columns": {
		"Podcasts",
		"Mark and Jair Explain Sports",
		"Ain't That Something With Brandon & Liz",
		"You, Me, Buscemi",
		"Polkadot Tea.Pot",
		"Where's Mario",
		"Student Snapshot",
	},

	"comics-puzzles": {"Word Search"},
	"science-tech":   {"Science & Technology", "Technically Speaking"},

	// Sponsored content, and the weakest of these calls: it is not news, but
	// an advertiser paid for it to be visible and no section fits it better.
	"news": {"Paid Post"},
}

// legacySubsection is a WordPress sub-category that never got a row of its own.
type legacySubsection struct {
	Slug   string
	Title  string
	Parent string
}

// Legacy WordPress categories get hidden subsection pages with exact title
// aliases. Hidden rows still contribute articles to their parent sections.
var legacySubsections = []legacySubsection{
	// Sports. WordPress rolled a parent category over its children, so
	// /category/sports/ listed these without anyone configuring it; flat
	// matching here does not, which is what stranded them.
	{"tennis", "Tennis", "sports"},
	{"crew", "Crew", "sports"},
	{"mens-lacrosse", "Men's Lacrosse", "sports"},
	{"womens-lacrosse", "Women's Lacrosse", "sports"},
	{"golf", "Golf", "sports"},
	{"softball", "Softball", "sports"},
	{"swimming-diving", "Swimming & Diving", "sports"},
	{"running", "Running", "sports"},
	{"athlete-of-the-week", "Athlete of the Week", "sports"},
	{"wrestling", "Wrestling", "sports"},

	// Entertainment. Style is the large one (the beat plus its recurring
	// features) and sits here rather than under Opinion's Lifestyle because
	// it is culture coverage, not opinion writing.
	{"features", "Features", "entertainment"},
	{"style", "Style", "entertainment"},
	{"tv", "TV", "entertainment"},
	{"theater", "Theater", "entertainment"},
	{"beer-reviews", "Beer Reviews", "entertainment"},
	{"wine-reviews", "Wine Reviews", "entertainment"},
	{"restaurant-reviews", "Restaurant Reviews", "entertainment"},
	{"last-call", "Last Call", "entertainment"},
	{"exhibits", "Exhibits", "entertainment"},
	{"reel2reel", "Reel2Reel", "entertainment"},
	{"street-style", "Street Style", "entertainment"},
	{"style-guide", "Style Guide", "entertainment"},
	{"beauty-guide", "Beauty Guide", "entertainment"},
	{"designer-profile", "Designer Profile", "entertainment"},
	{"store-profile", "Store Profile", "entertainment"},
	{"inside-her-bag", "Inside Her Bag", "entertainment"},
	{"fashion-week", "Fashion Week", "entertainment"},
	{"diy", "DIY", "entertainment"},

	// Opinion's own voice: neither editorials nor letters are filed under it.
	{"editorial", "Editorial", "opinion"},
	{"letters-to-the-editor", "Letters to the Editor", "opinion"},
	{"commentary", "Commentary", "opinion"},

	// Columns is where recurring bylined series live, which is what the
	// podcasts are: "Mark and Jair Explain Sports" is a show, not a sports
	// article, and a row keeps it out of Sports despite the name.
	{"podcasts", "Podcasts", "columns"},
	{"mark-and-jair-explain-sports", "Mark and Jair Explain Sports", "columns"},
	{"aint-that-something-with-brandon-liz", "Ain't That Something With Brandon & Liz", "columns"},
	{"you-me-buscemi", "You, Me, Buscemi", "columns"},
	{"polkadot-tea-pot", "Polkadot Tea.Pot", "columns"},
	{"wheres-mario", "Where's Mario", "columns"},
	{"student-snapshot", "Student Snapshot", "columns"},
	{"triangle-talks", "Triangle Talks", "columns"},
	{"triangle-sports-talk", "Triangle Sports Talk", "columns"},
	{"sadie-says", "Sadie Says", "columns"},
	{"tech-tuesday", "Tech Tuesday", "columns"},
	{"dear-granny-and-eloise", "Dear Granny and Eloise", "columns"},
	{"movies-ive-seen", "Movies I've Seen", "columns"},
	{"humans-of-drexel", "Humans of Drexel", "columns"},

	{"word-search", "Word Search", "comics-puzzles"},

	{"administration", "Administration", "news"},
	{"sjn-grant", "SJN Grant", "news"},
}

// SeedLegacySubsections inserts missing legacy rows once and returns their slugs.
// Skip existing slugs and missing parents to preserve editorial changes.
// An empty taxonomy defers the seed; otherwise record completion even if no rows changed.
func SeedLegacySubsections(ctx context.Context, conn *sql.DB) ([]string, error) {
	if conn == nil {
		return nil, nil
	}

	// The run-once flag lives in cms_settings, so without that table there is no
	// way to seed exactly once, and seeding repeatedly would resurrect rows an
	// editor deleted. Production creates it well before this runs; a database
	// without it is a test fixture or a half-built schema, and skipping is the
	// safe answer for both.
	var settingsTableExists int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'cms_settings'",
	).Scan(&settingsTableExists); err != nil {
		return nil, err
	}
	if settingsTableExists == 0 {
		return nil, nil
	}

	var done string
	switch err := conn.QueryRowContext(ctx,
		"SELECT value_text FROM cms_settings WHERE key_name = 'legacy_subsections_seeded' LIMIT 1",
	).Scan(&done); err {
	case nil:
		if strings.TrimSpace(done) == "1" {
			return nil, nil
		}
	case sql.ErrNoRows:
		// Not yet seeded.
	default:
		return nil, err
	}

	sections, err := existingSlugsByKind(ctx, conn, "section")
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return nil, nil
	}
	subsections, err := existingSlugsByKind(ctx, conn, "subsection")
	if err != nil {
		return nil, err
	}

	var nextID int64
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM site_taxonomy").Scan(&nextID); err != nil {
		return nil, err
	}

	inserted := make([]string, 0, len(legacySubsections))
	for _, item := range legacySubsections {
		if _, taken := subsections[item.Slug]; taken {
			continue
		}
		if _, ok := sections[item.Parent]; !ok {
			slog.Warn("skipping legacy subsection whose parent section is missing",
				"slug", item.Slug, "parent_slug", item.Parent)
			continue
		}
		aliases, err := MarshalCategoryJSON([]string{item.Title})
		if err != nil {
			return nil, err
		}
		nextID++
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO site_taxonomy
				(id, kind, slug, canonical_title, parent_slug, article_count, category_aliases, is_visible)
			VALUES (?, 'subsection', ?, ?, ?, 0, ?, 0)
		`, nextID, item.Slug, item.Title, item.Parent, aliases); err != nil {
			return nil, err
		}
		inserted = append(inserted, item.Slug)
	}

	if len(inserted) > 0 {
		slog.Info("seeded legacy WordPress sub-categories as hidden subsections", "count", len(inserted))
	}
	return inserted, writeSettingRaw(ctx, conn, "legacy_subsections_seeded", "1")
}

// Food groups A&E review categories. Reparenting Cooking also moves its
// visible navigation link from Entertainment to Food.
var (
	foodSubsectionSlug   = "food"
	foodSubsectionTitle  = "Food"
	foodSubsectionParent = "entertainment"
	// No aliases: "food" already matches the category "Food" through
	// CategoryMatchValues, and everything else Food should list arrives through
	// its children. Inventing spellings here would be guessing at categories
	// that may mean something else to the desk.
	foodSubsectionAliases  = []string{"Food"}
	foodSubsectionChildren = []string{"beer-reviews", "wine-reviews", "restaurant-reviews", "cooking"}
)

// SeedFoodSubsection creates visible Food and reparents children without changing
// their visibility. Its separate completion flag preserves later editorial changes
// and covers databases where legacy seeding already ran.
func SeedFoodSubsection(ctx context.Context, conn *sql.DB) ([]string, error) {
	if conn == nil {
		return nil, nil
	}

	var settingsTableExists int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'cms_settings'",
	).Scan(&settingsTableExists); err != nil {
		return nil, err
	}
	if settingsTableExists == 0 {
		return nil, nil
	}

	var done string
	switch err := conn.QueryRowContext(ctx,
		"SELECT value_text FROM cms_settings WHERE key_name = 'food_subsection_seeded' LIMIT 1",
	).Scan(&done); err {
	case nil:
		if strings.TrimSpace(done) == "1" {
			return nil, nil
		}
	case sql.ErrNoRows:
		// Not yet seeded.
	default:
		return nil, err
	}

	sections, err := existingSlugsByKind(ctx, conn, "section")
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		// The taxonomy has not been imported yet. Returning without recording
		// the flag leaves the one run intact for a later boot; see
		// SeedLegacySubsections.
		return nil, nil
	}
	if _, ok := sections[foodSubsectionParent]; !ok {
		slog.Warn("skipping the Food subsection seed: its parent section is missing",
			"parent_slug", foodSubsectionParent)
		return nil, writeSettingRaw(ctx, conn, "food_subsection_seeded", "1")
	}

	subsections, err := existingSlugsByKind(ctx, conn, "subsection")
	if err != nil {
		return nil, err
	}

	touched := make([]string, 0, len(foodSubsectionChildren)+1)
	if _, taken := subsections[foodSubsectionSlug]; !taken {
		aliases, err := MarshalCategoryJSON(foodSubsectionAliases)
		if err != nil {
			return nil, err
		}
		var nextID int64
		if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) + 1 FROM site_taxonomy").Scan(&nextID); err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO site_taxonomy
				(id, kind, slug, canonical_title, parent_slug, article_count, category_aliases, is_visible)
			VALUES (?, 'subsection', ?, ?, ?, 0, ?, 1)
		`, nextID, foodSubsectionSlug, foodSubsectionTitle, foodSubsectionParent, aliases); err != nil {
			return nil, err
		}
		touched = append(touched, foodSubsectionSlug)
	}

	for _, child := range foodSubsectionChildren {
		if _, exists := subsections[child]; !exists {
			continue
		}
		// Guarded on the CURRENT parent, so a row an editor has already moved
		// somewhere deliberate is left where they put it.
		result, err := conn.ExecContext(ctx, `
			UPDATE site_taxonomy
			SET parent_slug = ?
			WHERE kind = 'subsection' AND slug = ? AND parent_slug = ?
		`, foodSubsectionSlug, child, foodSubsectionParent)
		if err != nil {
			return nil, err
		}
		// RowsAffected is not trusted as proof here, since through MaxScale it
		// can report 0 for a write that landed, so the slug is reported as touched
		// either way and the recount that follows settles the numbers.
		if _, err := result.RowsAffected(); err != nil {
			return nil, err
		}
		touched = append(touched, child)
	}

	if len(touched) > 0 {
		slog.Info("seeded the Food subsection under A&E", "slugs", touched)
	}
	return touched, writeSettingRaw(ctx, conn, "food_subsection_seeded", "1")
}

// Initial A&E navigation choices: hide Listicles and Books, show TV.
// Visibility changes navigation only; pages and articles remain accessible.
var entertainmentVisibility = map[string]bool{
	"listicles": false,
	"books":     false,
	"tv":        true,
}

// SeedEntertainmentVisibility applies initial navigation choices once.
// An empty taxonomy defers the seed; otherwise record completion even if no rows
// changed, preserving subsequent editorial edits.
func SeedEntertainmentVisibility(ctx context.Context, conn *sql.DB) error {
	if conn == nil {
		return nil
	}

	var settingsTableExists int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'cms_settings'",
	).Scan(&settingsTableExists); err != nil {
		return err
	}
	if settingsTableExists == 0 {
		return nil
	}

	var done string
	switch err := conn.QueryRowContext(ctx,
		"SELECT value_text FROM cms_settings WHERE key_name = 'entertainment_visibility_seeded' LIMIT 1",
	).Scan(&done); err {
	case nil:
		if strings.TrimSpace(done) == "1" {
			return nil
		}
	case sql.ErrNoRows:
		// Not yet seeded.
	default:
		return err
	}

	subsections, err := existingSlugsByKind(ctx, conn, "subsection")
	if err != nil {
		return err
	}
	if len(subsections) == 0 {
		return nil
	}

	changed := make([]string, 0, len(entertainmentVisibility))
	for slug, visible := range entertainmentVisibility {
		if _, exists := subsections[slug]; !exists {
			slog.Warn("skipping a visibility change for a subsection that does not exist", "slug", slug)
			continue
		}
		result, err := conn.ExecContext(ctx, `
			UPDATE site_taxonomy
			SET is_visible = ?
			WHERE kind = 'subsection' AND slug = ? AND is_visible = ?
		`, visible, slug, !visible)
		if err != nil {
			return err
		}
		// RowsAffected is not proof of anything through MaxScale, which can
		// report 0 for a write that landed. It is only used to keep the log
		// honest about what moved, so a wrong answer costs a log line.
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			changed = append(changed, slug)
		}
	}

	if len(changed) > 0 {
		slog.Info("applied the A&E subsection strip changes", "slugs", changed)
	}
	return writeSettingRaw(ctx, conn, "entertainment_visibility_seeded", "1")
}

func existingSlugsByKind(ctx context.Context, conn *sql.DB, kind string) (map[string]struct{}, error) {
	rows, err := conn.QueryContext(ctx, "SELECT slug FROM site_taxonomy WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	slugs := make(map[string]struct{})
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		slugs[strings.TrimSpace(slug)] = struct{}{}
	}
	return slugs, rows.Err()
}

func SeedDefaultCategoryAliases(ctx context.Context, conn *sql.DB) error {
	for slug, aliases := range defaultCategoryAliases {
		payload, err := MarshalCategoryJSON(aliases)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE site_taxonomy
			SET category_aliases = ?
			WHERE slug = ? AND category_aliases IS NULL
		`, payload, slug); err != nil {
			return err
		}
	}
	return nil
}

// categoryAliasCache holds site_taxonomy's aliases in memory so
// CategoryMatchPatterns can stay a pure function of a slug. Reloading it is
// cheap (the table is ~50 rows) and only happens at startup and after a
// taxonomy write, which keeps the article-listing path free of an extra query.
var (
	categoryAliasMu     sync.RWMutex
	categoryAliasBySlug = map[string][]string{}

	// categorySlugByTitle is the reverse direction: the category text an
	// article carries -> the taxonomy slug whose page lists it. It backs
	// TaxonomySlugForCategory and is loaded by the same refresh.
	categorySlugByTitle = map[string]string{}
)

// RefreshCategoryAliases reloads the alias cache from site_taxonomy. Call it
// after any write to the table, or matching will keep using the old aliases
// until the process restarts.
func RefreshCategoryAliases(ctx context.Context, conn *sql.DB) error {
	// Every section and subsection, not just the rows carrying aliases: the
	// title -> slug direction needs the rows that have no alias at all, which
	// is most of them.
	rows, err := conn.QueryContext(ctx, `
		SELECT slug, canonical_title, category_aliases
		FROM site_taxonomy
		WHERE kind IN ('section', 'subsection')
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	loaded := make(map[string][]string)
	titles := make(map[string]string)
	aliasTitles := make(map[string]string)
	for rows.Next() {
		var slug, canonicalTitle string
		var raw sql.NullString
		if err := rows.Scan(&slug, &canonicalTitle, &raw); err != nil {
			return err
		}
		normalizedSlug := strings.ToLower(strings.TrimSpace(slug))
		if normalizedSlug == "" {
			continue
		}
		if title := strings.ToLower(strings.TrimSpace(canonicalTitle)); title != "" {
			titles[title] = normalizedSlug
		}

		if !raw.Valid {
			continue
		}
		aliases, err := ParseCategoryAliases(raw.String)
		if err != nil {
			// One malformed row must not blank every other section's
			// aliases, so skip it and keep going.
			slog.Warn("ignoring malformed taxonomy category_aliases", "slug", slug, "error", err)
			continue
		}
		if len(aliases) == 0 {
			continue
		}
		loaded[normalizedSlug] = aliases
		for _, alias := range aliases {
			if normalized := strings.ToLower(strings.TrimSpace(alias)); normalized != "" {
				// First writer wins, so a duplicated alias cannot make the
				// resolved slug depend on row order.
				if _, taken := aliasTitles[normalized]; !taken {
					aliasTitles[normalized] = normalizedSlug
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// A row's own title outranks any alias: an alias is how a section absorbs
	// somebody else's category, so it must never displace that category's own
	// page when one exists.
	for title, slug := range aliasTitles {
		if _, exists := titles[title]; !exists {
			titles[title] = slug
		}
	}

	categoryAliasMu.Lock()
	categoryAliasBySlug = loaded
	categorySlugByTitle = titles
	categoryAliasMu.Unlock()
	return nil
}

// TaxonomySlugForCategory resolves category text through taxonomy aliases,
// returning an empty string if unmatched. Deriving URLs from names alone breaks
// categories such as "Men's Basketball", whose slug is "mens-basketball".
func TaxonomySlugForCategory(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return ""
	}
	categoryAliasMu.RLock()
	defer categoryAliasMu.RUnlock()
	return categorySlugByTitle[normalized]
}

// CategoryLinkSlug is what an article's category chip should point at: the
// taxonomy slug when a page exists, and the name-derived slug otherwise so the
// value is never empty.
func CategoryLinkSlug(name string) string {
	if slug := TaxonomySlugForCategory(name); slug != "" {
		return slug
	}
	return CanonicalizeSlug(name)
}

// ParseCategoryAliases decodes the stored JSON array, dropping blanks. An empty
// or absent value is not an error; most rows have no aliases.
func ParseCategoryAliases(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, err
	}
	aliases := make([]string, 0, len(decoded))
	for _, alias := range decoded {
		if cleaned := strings.TrimSpace(alias); cleaned != "" {
			aliases = append(aliases, cleaned)
		}
	}
	return aliases, nil
}

func categoryAliasesFor(slug string) []string {
	categoryAliasMu.RLock()
	defer categoryAliasMu.RUnlock()
	return categoryAliasBySlug[slug]
}

// CategoryMatchValues returns lowercased, trimmed whole category titles for a slug.
// Listings and counts share exact matches: substrings would merge men's and women's
// basketball. Call RefreshCategoryAliases first (done at startup) to resolve aliases.
// JSON decoding in article_categories handles escaped ampersands.
func CategoryMatchValues(slug string) []string {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return nil
	}

	values := make([]string, 0, 4)
	add := func(value string) {
		for _, existing := range values {
			if existing == value {
				return
			}
		}
		values = append(values, value)
	}

	add(normalized)
	if strings.Contains(normalized, "-") {
		spaced := strings.ReplaceAll(normalized, "-", " ")
		add(spaced)
		add(strings.ReplaceAll(normalized, "-", " & "))
		if possessive := possessiveVariant(spaced); possessive != "" {
			add(possessive)
		}
	}
	for _, alias := range categoryAliasesFor(normalized) {
		add(strings.ToLower(strings.TrimSpace(alias)))
	}
	return values
}

// possessiveStems are the words a slug can only have lost an apostrophe from.
// "mens" is not a plural of anything, so it can only be "men's"; "comics" is a
// plural, so "comic's" would be a guess. Restricting the rewrite to these keeps
// it from inventing patterns.
var possessiveStems = map[string]string{
	"mens":      "men's",
	"womens":    "women's",
	"childrens": "children's",
	"peoples":   "people's",
}

// possessiveVariant restores the apostrophe a slug had to drop, so
// "mens-basketball" can still find the category "Men's Basketball". Returns ""
// when the phrase has no possessive to restore.
//
// Without this the four apostrophe subsections (both basketballs, both
// soccers) matched nothing at all, so their pages were empty and their counts
// read 0 while the categories they name were on hundreds of articles.
func possessiveVariant(phrase string) string {
	words := strings.Split(phrase, " ")
	changed := false
	for i, word := range words {
		if replacement, ok := possessiveStems[word]; ok {
			words[i] = replacement
			changed = true
		}
	}
	if !changed {
		return ""
	}
	return strings.Join(words, " ")
}

// MaxTaxonomyDepth is how many levels the section tree may hold, counting the
// section itself: section -> subsection -> subsection. A&E needed the third for
// Food, which holds Beer Reviews, Wine Reviews and Restaurant Reviews.
//
// Bounded because the walks below are recursive and the tree is editor-editable.
// The bound is also what makes a cycle survivable rather than a hang, though
// validateTaxonomyParent refuses to create one in the first place.
const MaxTaxonomyDepth = 3

// TaxonomyChildren returns the slugs filed directly under a slug.
func TaxonomyChildren(ctx context.Context, conn *sql.DB, slug string) ([]string, error) {
	if conn == nil || strings.TrimSpace(slug) == "" {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT slug FROM site_taxonomy WHERE kind = 'subsection' AND parent_slug = ?",
		strings.TrimSpace(slug),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var children []string
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			return nil, err
		}
		if trimmed := strings.TrimSpace(child); trimmed != "" {
			children = append(children, trimmed)
		}
	}
	return children, rows.Err()
}

// TaxonomyDescendants returns a slug plus every slug below it, at any depth,
// with the slug itself first. That set is what "articles filed under this" means
// once the tree has three levels.
func TaxonomyDescendants(ctx context.Context, conn *sql.DB, slug string) ([]string, error) {
	trimmed := strings.TrimSpace(slug)
	if conn == nil || trimmed == "" {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT slug, COALESCE(parent_slug, '')
		FROM site_taxonomy
		WHERE kind = 'subsection' AND parent_slug IS NOT NULL AND parent_slug <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	children := make(map[string][]string)
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if strings.TrimSpace(child) == "" {
			continue
		}
		children[strings.TrimSpace(parent)] = append(children[strings.TrimSpace(parent)], strings.TrimSpace(child))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return append([]string{trimmed}, descendantsOf(children, trimmed)...), nil
}

// TaxonomyAncestors returns a slug's parent chain, nearest first, stopping at
// the root section.
//
// It stops after MaxTaxonomyDepth hops regardless: this walks editor-owned data
// that a bad write could have made circular, and a traversal that cannot
// terminate would take the request thread with it.
func TaxonomyAncestors(ctx context.Context, conn *sql.DB, slug string) ([]string, error) {
	if conn == nil {
		return nil, nil
	}
	parents, err := taxonomyParentsBySlug(ctx, conn)
	if err != nil {
		return nil, err
	}
	return ancestorChain(parents, slug), nil
}

// ancestorChain walks a parent map upward, nearest ancestor first. Bounded by
// MaxTaxonomyDepth and by a seen set, so a cycle yields a truncated chain rather
// than an infinite loop.
func ancestorChain(parents map[string]string, slug string) []string {
	var chain []string
	seen := map[string]struct{}{}
	current := strings.TrimSpace(slug)
	for range MaxTaxonomyDepth {
		parent := strings.TrimSpace(parents[current])
		if parent == "" {
			break
		}
		if _, looped := seen[parent]; looped {
			break
		}
		seen[parent] = struct{}{}
		chain = append(chain, parent)
		current = parent
	}
	return chain
}

// taxonomyMatchSlugs maps each slug to itself and all descendants.
// Container sections may have no directly filed articles.
func taxonomyMatchSlugs(ctx context.Context, conn *sql.DB) (map[string][]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT slug, kind, COALESCE(parent_slug, '')
		FROM site_taxonomy
		WHERE kind IN ('section', 'subsection')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make(map[string][]string)
	children := make(map[string][]string)
	for rows.Next() {
		var slug, kind, parent string
		if err := rows.Scan(&slug, &kind, &parent); err != nil {
			return nil, err
		}
		if strings.TrimSpace(slug) == "" {
			continue
		}
		matches[slug] = []string{slug}
		if kind == "subsection" && strings.TrimSpace(parent) != "" {
			children[parent] = append(children[parent], slug)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for slug := range matches {
		matches[slug] = append(matches[slug], descendantsOf(children, slug)...)
	}
	return matches, nil
}

// descendantsOf collects every slug below one, breadth-first. The seen set is
// what makes it safe on data it does not control: a cycle visits each member
// once and stops.
func descendantsOf(children map[string][]string, slug string) []string {
	var found []string
	seen := map[string]struct{}{slug: {}}
	queue := append([]string(nil), children[slug]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, visited := seen[current]; visited {
			continue
		}
		seen[current] = struct{}{}
		found = append(found, current)
		queue = append(queue, children[current]...)
	}
	return found
}

// TaxonomyCountCondition builds a category-index predicate and its arguments.
// Use EXISTS because callers negate the result and NOT IN is unsafe with NULLs.
// Callers must select from the unaliased articles table used by the correlation.
func TaxonomyCountCondition(slugs []string) (string, []any) {
	var values []string
	seen := make(map[string]bool)
	for _, slug := range slugs {
		for _, value := range CategoryMatchValues(slug) {
			if seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return "", nil
	}

	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		args[i] = value
	}

	condition := "EXISTS (SELECT 1 FROM `article_categories` `ac` WHERE `ac`.`article_id` = `articles`.`id` " +
		"AND `ac`.`category` IN (" + strings.Join(placeholders, ", ") + "))"
	return condition, args
}

// countArticlesForSlugs counts the articles a taxonomy row matches, over the
// same population the public listing shows, so article_count equals the total a
// reader actually pages through.
func countArticlesForSlugs(ctx context.Context, conn *sql.DB, slug string, matched []string) (int64, error) {
	condition, args := TaxonomyCountCondition(matched)
	if condition == "" {
		return 0, nil
	}
	var count int64
	query := "SELECT COUNT(*) FROM `articles` WHERE `archived_at` IS NULL AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND " + condition
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		// The failure mode this catches: matching is exact, so a slug that is
		// not the canonicalized category string resolves to nothing and its
		// page renders empty. That looks like a section with no content rather
		// than a misconfiguration, which is exactly how four sections stayed
		// broken until someone noticed the comics were in the wrong place.
		// Name it out loud.
		slog.Warn("taxonomy slug matches no articles; it likely needs a category alias",
			"slug", slug, "matched_slugs", matched)
	}
	return count, nil
}

// ReportOrphanedArticles logs published articles that match no taxonomy row.
// It does not assign categories; that requires an editorial decision.
func ReportOrphanedArticles(ctx context.Context, conn *sql.DB) error {
	matchSlugs, err := taxonomyMatchSlugs(ctx, conn)
	if err != nil {
		return err
	}

	slugs := make([]string, 0, len(matchSlugs))
	for slug := range matchSlugs {
		slugs = append(slugs, slug)
	}
	condition, args := TaxonomyCountCondition(slugs)
	if condition == "" {
		return nil
	}

	const published = "`archived_at` IS NULL AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP()"

	var orphaned int64
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `articles` WHERE "+published+" AND NOT "+condition, args...,
	).Scan(&orphaned); err != nil {
		return err
	}
	if orphaned == 0 {
		return nil
	}

	// The category sets are what a person needs to act: they say which
	// spellings to alias, which is not recoverable from a bare count.
	rows, err := conn.QueryContext(ctx,
		"SELECT `categories`, COUNT(*) AS c FROM `articles` WHERE "+published+
			" AND NOT "+condition+" GROUP BY `categories` ORDER BY c DESC LIMIT 10", args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	samples := make([]string, 0, 10)
	for rows.Next() {
		var categories sql.NullString
		var count int64
		if err := rows.Scan(&categories, &count); err != nil {
			return err
		}
		samples = append(samples, fmt.Sprintf("%s (%d)", strings.TrimSpace(categories.String), count))
	}
	if err := rows.Err(); err != nil {
		return err
	}

	slog.Warn("published articles match no section or subsection and appear on no section page; they likely need a category alias",
		"count", orphaned, "top_category_sets", strings.Join(samples, "; "))
	return nil
}

func RebuildTaxonomyArticleCounts(ctx context.Context, conn *sql.DB) error {
	matchSlugs, err := taxonomyMatchSlugs(ctx, conn)
	if err != nil {
		return err
	}

	counts := make(map[string]int64, len(matchSlugs))
	for slug, slugs := range matchSlugs {
		count, err := countArticlesForSlugs(ctx, conn, slug, slugs)
		if err != nil {
			return err
		}
		counts[slug] = count
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE site_taxonomy
		SET article_count = 0
		WHERE kind IN ('section', 'subsection')
	`); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE site_taxonomy
		SET article_count = ?
		WHERE kind IN ('section', 'subsection') AND slug = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for slug, count := range counts {
		if _, err := stmt.ExecContext(ctx, count, slug); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RebuildTaxonomyArticleCountsFor recounts touched slugs and every ancestor,
// keeping nested counts current without a full recount on each edit.
func RebuildTaxonomyArticleCountsFor(ctx context.Context, conn *sql.DB, slugs ...string) error {
	matchSlugs, err := taxonomyMatchSlugs(ctx, conn)
	if err != nil {
		return err
	}

	parents, err := taxonomyParentsBySlug(ctx, conn)
	if err != nil {
		return err
	}

	affected := make(map[string]struct{}, len(slugs)*MaxTaxonomyDepth)
	for _, slug := range slugs {
		normalized := strings.TrimSpace(slug)
		if normalized == "" {
			continue
		}
		affected[normalized] = struct{}{}
		for _, ancestor := range ancestorChain(parents, normalized) {
			affected[ancestor] = struct{}{}
		}
	}
	if len(affected) == 0 {
		return nil
	}

	stmt, err := conn.PrepareContext(ctx, `
		UPDATE site_taxonomy
		SET article_count = ?
		WHERE kind IN ('section', 'subsection') AND slug = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for slug := range affected {
		matched, ok := matchSlugs[slug]
		if !ok {
			// Deleted by the edit that triggered this; nothing to update.
			continue
		}
		count, err := countArticlesForSlugs(ctx, conn, slug, matched)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, count, slug); err != nil {
			return err
		}
	}
	return nil
}

func taxonomyParentsBySlug(ctx context.Context, conn *sql.DB) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT slug, COALESCE(parent_slug, '')
		FROM site_taxonomy
		WHERE kind = 'subsection'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	parents := make(map[string]string)
	for rows.Next() {
		var slug, parent string
		if err := rows.Scan(&slug, &parent); err != nil {
			return nil, err
		}
		if strings.TrimSpace(parent) != "" {
			parents[slug] = parent
		}
	}
	return parents, rows.Err()
}
