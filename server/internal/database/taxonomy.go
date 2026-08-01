package database

import (
	"context"
	"database/sql"
	"strings"
)

func EnsureTaxonomyTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema("site_taxonomy"))
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, `
		ALTER TABLE site_taxonomy
		ADD COLUMN IF NOT EXISTS article_count BIGINT UNSIGNED NOT NULL DEFAULT 0
	`)
	return err
}

// CategoryMatchPatterns returns the LOWER(`categories`) LIKE patterns that
// identify articles filed under a taxonomy slug.
//
// WordPress category text does not match our slugs literally, so a slug stands
// in for several spellings: "comics-puzzles" has to find "Comics & Puzzles".
// This is the single definition of "is this article in this section" -- both the
// article listing and the count rebuild call it, because when they disagreed a
// section could list 2545 articles while reporting 8.
func CategoryMatchPatterns(slug string) []string {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return nil
	}

	patterns := make([]string, 0, 3)
	add := func(value string) {
		for _, existing := range patterns {
			if existing == value {
				return
			}
		}
		patterns = append(patterns, value)
	}

	add("%" + normalized + "%")
	if strings.Contains(normalized, "-") {
		add("%" + strings.ReplaceAll(normalized, "-", " ") + "%")
		add("%" + strings.ReplaceAll(normalized, "-", " & ") + "%")
	}
	return patterns
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
			clauses = append(clauses, "LOWER(`categories`) LIKE ?")
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
		query := "SELECT COUNT(*) FROM `articles` WHERE `archived_at` IS NULL AND `pub_date` IS NOT NULL AND " + condition
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
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
