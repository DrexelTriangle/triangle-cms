package database

import (
	"context"
	"database/sql"
	"encoding/json"
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

// defaultCategoryAliases are the slug -> category-title mismatches that existed
// before aliases were editable data, seeded so upgrading an existing database
// does not silently empty four sections.
//
// Only applied where category_aliases IS NULL, so "never set" stays
// distinguishable from an empty array an editor deliberately saved.
var defaultCategoryAliases = map[string][]string{
	"entertainment":       {"Arts & Entertainment"},
	"science-tech":        {"Science & Technology"},
	"from-the-editor":     {"From the Editor's Desk"},
	"happening-in-philly": {"What's Happening in Philly"},
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
)

// RefreshCategoryAliases reloads the alias cache from site_taxonomy. Call it
// after any write to the table, or matching will keep using the old aliases
// until the process restarts.
func RefreshCategoryAliases(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT slug, category_aliases
		FROM site_taxonomy
		WHERE category_aliases IS NOT NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	loaded := make(map[string][]string)
	for rows.Next() {
		var slug string
		var raw sql.NullString
		if err := rows.Scan(&slug, &raw); err != nil {
			return err
		}
		aliases, err := ParseCategoryAliases(raw.String)
		if err != nil {
			// One malformed row must not blank every other section's
			// aliases, so skip it and keep going.
			slog.Warn("ignoring malformed taxonomy category_aliases", "slug", slug, "error", err)
			continue
		}
		if len(aliases) > 0 {
			loaded[strings.ToLower(strings.TrimSpace(slug))] = aliases
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	categoryAliasMu.Lock()
	categoryAliasBySlug = loaded
	categoryAliasMu.Unlock()
	return nil
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

func RebuildTaxonomyArticleCounts(ctx context.Context, conn *sql.DB) error {
	matchSlugs, err := taxonomyMatchSlugs(ctx, conn)
	if err != nil {
		return err
	}

	// Counted over the same population the public listing shows, so
	// article_count equals the total a reader actually pages through.
	counts := make(map[string]int64, len(matchSlugs))
	for slug, slugs := range matchSlugs {
		condition, args := TaxonomyCountCondition(slugs)
		if condition == "" {
			continue
		}
		var count int64
		query := "SELECT COUNT(*) FROM `articles` WHERE `archived_at` IS NULL AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND " + condition
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
			return err
		}
		counts[slug] = count
		if count == 0 {
			// The failure mode this catches: matching is exact, so a slug
			// that is not the canonicalized category string resolves to
			// nothing and its page renders empty. That looks like a section
			// with no content rather than a misconfiguration, which is
			// exactly how four sections stayed broken until someone noticed
			// the comics were in the wrong place. Name it out loud.
			slog.Warn("taxonomy slug matches no articles; it likely needs a category alias",
				"slug", slug, "matched_slugs", slugs)
		}
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
