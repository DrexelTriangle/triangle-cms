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

	if err = SeedDefaultCategoryAliases(ctx, conn); err != nil {
		return err
	}
	return RefreshCategoryAliases(ctx, conn)
}

// defaultCategoryAliases are the categories a section has to answer to beyond
// its own name, seeded so upgrading an existing database does not silently
// empty a section.
//
// Only applied where category_aliases IS NULL, so "never set" stays
// distinguishable from an empty array an editor deliberately saved. A row that
// already holds aliases is therefore never topped up from here: adding an entry
// for such a row also needs that row updated in the sections screen, or the
// addition applies to new databases only.
//
// Two kinds of entry live here. The first four are one section under two names.
// Everything after them is a category that never became a subsection: WordPress
// category archives roll a parent up over its child terms automatically, so
// /category/sports/ listed every article tagged "Men's Lacrosse" without anyone
// configuring it. Matching here is flat -- a section finds itself plus the
// subsections that have a row -- so a category with no row rolls up into
// nothing and its articles appear on no section page at all. That stranded 469
// published articles, 4.7% of the archive.
//
// It stayed invisible because most articles also carry their section's literal
// tag and were never affected, so the gaps looked arbitrary rather than
// systematic. ReportOrphanedArticles now names the remainder out loud.
//
// Aliases rather than subsection rows: these need to rejoin a section, not to
// acquire a nav entry and a page each. Adding a row is still the right move for
// a category the desk wants to feature.
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
	// podcasts are -- "Mark and Jair Explain Sports" is a show, not a sports
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

// TaxonomySlugForCategory resolves the category text an article carries to the
// slug of the page that lists it, or "" when nothing does.
//
// The category chip on an article card is a link, and its target used to be
// derived from the category NAME alone. That silently produced URLs no page
// answers: "Men's Basketball" canonicalizes to "men-s-basketball" while the
// subsection lives at "mens-basketball", so every chip on both basketballs and
// both soccers -- the four apostrophe subsections -- was a 404. The apostrophe
// is the same thing possessiveVariant exists to absorb on the matching side;
// this is that fix applied to the link.
//
// Resolving through the taxonomy also means a category that only reaches a
// section by alias links to that section rather than to nothing.
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
// or absent value is not an error -- most rows have no aliases.
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

// CategoryColumnExpr is the SQL expression every category match runs against.
//
// `articles`.`categories` is a JSON array of category titles, but it is written
// by two producers that escape it differently: the ETL emits a plain "&", while
// Go's encoding/json HTML-escapes it to a backslash-u escape (see FormatTags),
// so an article edited in the CMS stops matching the very section it is filed
// under. Folding that escape away here means callers only ever reason about one
// spelling. FormatTags no longer produces it, but rows written before that fix
// still carry it.
const CategoryColumnExpr = "REPLACE(LOWER(`categories`), '\\\\u0026', '&')"

// CategoryMatchPatterns returns the CategoryColumnExpr LIKE patterns that
// identify articles filed under a taxonomy slug.
//
// WordPress category text does not match our slugs literally, so a slug stands
// in for several spellings: "comics-puzzles" has to find "Comics & Puzzles".
// This is the single definition of "is this article in this section" -- both the
// article listing and the count rebuild call it, because when they disagreed a
// section could list 2545 articles while reporting 8.
//
// Every pattern is anchored on the JSON quotes around a member, so a slug
// matches a WHOLE category and never a fragment of a longer one. Unanchored
// patterns silently merged sibling taxonomies: `%puzzles%` matched the parent
// title "Comics & Puzzles" and so pulled all 219 comics into the Puzzles
// subsection, and `%men's basketball%` is a substring of "Women's Basketball",
// which folded the women's team into the men's. Both read as plausible-but-wrong
// pages rather than as errors, so anchoring is load-bearing, not cosmetic.
//
// Because the match is exact, a slug that is not the canonicalized category
// string resolves to nothing on its own -- it needs an alias row. Those come
// from the cache, so callers must have run RefreshCategoryAliases first;
// EnsureTaxonomyTable does it at startup.
func CategoryMatchPatterns(slug string) []string {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return nil
	}

	patterns := make([]string, 0, 4)
	add := func(value string) {
		// The JSON quotes are what make the match exact; without them a
		// pattern matches any category containing the phrase.
		pattern := `%"` + value + `"%`
		for _, existing := range patterns {
			if existing == pattern {
				return
			}
		}
		patterns = append(patterns, pattern)
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
	return patterns
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
// Without this the four apostrophe subsections -- both basketballs, both
// soccers -- matched nothing at all, so their pages were empty and their counts
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

// taxonomyMatchSlugs maps every section/subsection slug to the slugs whose
// articles belong to it: a subsection matches only itself, a section matches
// itself plus all of its children.
//
// The children matter because a section can be a pure container. Nothing is
// filed under the category "Special Editions" -- its articles live under
// "Welcome Week" and "100 Year Anniversary" -- so matching the section slug
// alone reports zero for a section that visibly has content.
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

	for parent, kids := range children {
		if _, ok := matches[parent]; !ok {
			continue
		}
		matches[parent] = append(matches[parent], kids...)
	}
	return matches, nil
}

// TaxonomyCountCondition builds the WHERE fragment matching articles in any of
// the given slugs, along with its arguments.
func TaxonomyCountCondition(slugs []string) (string, []any) {
	var clauses []string
	var args []any
	for _, slug := range slugs {
		for _, pattern := range CategoryMatchPatterns(slug) {
			clauses = append(clauses, CategoryColumnExpr+" LIKE ?")
			args = append(args, pattern)
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args
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

// ReportOrphanedArticles logs published articles that match no taxonomy row at
// all -- the inverse of the zero-match warning in countArticlesForSlugs.
//
// That warning catches a slug that finds no articles. This catches the
// direction that actually loses content: an article whose categories name
// nothing the taxonomy knows about is still published, still reachable by
// direct link and by search, and appears on no section page. Nothing in the CMS
// showed it was gone -- a section that quietly omits some of its articles reads
// as a normal section -- so it took an editor noticing that men's lacrosse had
// stopped appearing under Sports, months after the categories diverged.
//
// Diagnostic only: it names the gap and leaves the fix (an alias or a
// subsection row) to a person, because which section an orphan belongs to is an
// editorial call.
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

// RebuildTaxonomyArticleCountsFor recounts only the rows a single taxonomy edit
// can have changed, so a save in the sections screen can refresh the number it
// just invalidated without paying for a full rebuild.
//
// A full rebuild runs one scan of the articles table per taxonomy row -- fine as
// startup or maintenance work, far too slow to sit inside a save. The blast
// radius of one edit is small: the row itself, plus its parent section, because
// a section matches its own slug OR any of its children's. Nothing else moves.
//
// Callers pass the slugs they touched; parents are resolved here.
func RebuildTaxonomyArticleCountsFor(ctx context.Context, conn *sql.DB, slugs ...string) error {
	matchSlugs, err := taxonomyMatchSlugs(ctx, conn)
	if err != nil {
		return err
	}

	parents, err := taxonomyParentsBySlug(ctx, conn)
	if err != nil {
		return err
	}

	affected := make(map[string]struct{}, len(slugs)*2)
	for _, slug := range slugs {
		normalized := strings.TrimSpace(slug)
		if normalized == "" {
			continue
		}
		affected[normalized] = struct{}{}
		if parent := parents[normalized]; parent != "" {
			affected[parent] = struct{}{}
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
