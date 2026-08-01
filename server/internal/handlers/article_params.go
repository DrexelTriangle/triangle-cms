package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

var allowedSubsectionsBySection = map[string]map[string]struct{}{
	"news": {
		"academic-transformation": {},
		"politics":                {},
		"transit":                 {},
		"crime-policy-violations": {},
	},
	"sports": {
		"mens-basketball":   {},
		"womens-basketball": {},
		"big-5":             {},
		"philly-sports":     {},
		"field-hockey":      {},
		"mens-soccer":       {},
		"womens-soccer":     {},
	},
	"opinion": {
		"science-tech":    {},
		"from-the-editor": {},
	},
	"columns": {
		"the-love-triangle":    {},
		"tri-this-sweet-treat": {},
	},
	"entertainment": {
		"movies":              {},
		"music":               {},
		"happening-in-philly": {},
		"cooking":             {},
		"books":               {},
		"gaming":              {},
		"listicles":           {},
	},
	"comics-puzzles": {
		"political-cartoons": {},
		"crossword":          {},
		"sudoku":             {},
	},
}

func normalizeAndValidateArticleParams(ctx context.Context, conn *sql.DB, params ArticleParams) (ArticleParams, error) {
	params.AuthorSlug = strings.TrimSpace(params.AuthorSlug)
	params.Section = normalizeSectionSlug(params.Section)
	params.Subsection = strings.ToLower(strings.TrimSpace(params.Subsection))

	if params.AuthorSlug != "" && !db.IsCanonicalSlug(params.AuthorSlug) {
		return ArticleParams{}, fmt.Errorf("invalid author_slug")
	}

	if params.Section != "" {
		ok, err := taxonomySectionExists(ctx, conn, params.Section)
		if err != nil {
			return ArticleParams{}, err
		}
		if !ok {
			return ArticleParams{}, fmt.Errorf("invalid section_slug")
		}
	}

	if params.Section != "" {
		matchSlugs, err := sectionMatchSlugs(ctx, conn, params.Section)
		if err != nil {
			return ArticleParams{}, err
		}
		params.SectionMatchSlugs = matchSlugs
	}

	if params.Subsection != "" {
		parentSection, ok, err := parentSectionForSubsection(ctx, conn, params.Subsection)
		if err != nil {
			return ArticleParams{}, err
		}
		if !ok {
			return ArticleParams{}, fmt.Errorf("invalid subsection_slug")
		}
		if params.Section == "" {
			params.Section = parentSection
		} else if params.Section != parentSection {
			return ArticleParams{}, fmt.Errorf("subsection_slug does not belong to section_slug")
		}
	}

	return params, nil
}

func normalizeSectionSlug(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func taxonomySectionExists(ctx context.Context, conn *sql.DB, section string) (bool, error) {
	if conn == nil {
		_, ok := allowedSubsectionsBySection[section]
		return ok, nil
	}

	var exists int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
		string(models.TaxonomyTypeSection), section,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// sectionMatchSlugs returns the section plus every subsection filed under it,
// which together define what "articles in this section" means. Falls back to
// the section alone when there is no database handle, matching the behaviour of
// the other taxonomy lookups here.
func sectionMatchSlugs(ctx context.Context, conn *sql.DB, section string) ([]string, error) {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return nil, nil
	}
	if conn == nil {
		slugs := []string{trimmed}
		for subsection := range allowedSubsectionsBySection[trimmed] {
			slugs = append(slugs, subsection)
		}
		return slugs, nil
	}

	rows, err := conn.QueryContext(ctx,
		"SELECT slug FROM site_taxonomy WHERE kind = ? AND parent_slug = ?",
		string(models.TaxonomyTypeSubsection), trimmed,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	slugs := []string{trimmed}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		if strings.TrimSpace(slug) != "" {
			slugs = append(slugs, slug)
		}
	}
	return slugs, rows.Err()
}

func parentSectionForSubsection(ctx context.Context, conn *sql.DB, subsection string) (string, bool, error) {
	if conn != nil {
		var parent sql.NullString
		err := conn.QueryRowContext(ctx,
			"SELECT parent_slug FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
			string(models.TaxonomyTypeSubsection), subsection,
		).Scan(&parent)
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return parent.String, parent.Valid && parent.String != "", nil
	}

	for section, subsections := range allowedSubsectionsBySection {
		if _, ok := subsections[subsection]; ok {
			return section, true, nil
		}
	}
	return "", false, nil
}
