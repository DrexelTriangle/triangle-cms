package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// A slug that is well formed but absent from site_taxonomy is a missing
// resource, not a malformed request. Handlers that read the slug from the
// request path answer 404 for these; the query-string forms on
// GET /v1/articles keep answering 400, since there the slug is a filter the
// caller chose rather than the resource being addressed.
var (
	errSectionNotFound    = errors.New("invalid section_slug")
	errSubsectionNotFound = errors.New("invalid subsection_slug")
)

// articleParamsStatus picks the status code for a validation failure.
// pathParamErr is the sentinel whose slug arrived in the request path, so only
// that one degrades to 404.
func articleParamsStatus(err, pathParamErr error) int {
	if errors.Is(err, pathParamErr) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

var allowedSubsectionsBySection = map[string]map[string]struct{}{
	"news": {
		"academic-transformation": {},
		"transit":                 {},
		"public-safety":           {},
		"campus":                  {},
		"city":                    {},
		"national":                {},
		"world":                   {},
	},
	"sports": {
		"mens-basketball":   {},
		"womens-basketball": {},
		"big-5":             {},
		"philly-sports":     {},
		"field-hockey":      {},
		"mens-soccer":       {},
		"womens-soccer":     {},
		"nil":               {},
		"squash":            {},
	},
	"opinion": {
		"science-tech":    {},
		"from-the-editor": {},
		"politics":        {},
		"lifestyle":       {},
	},
	"columns": {
		"the-love-triangle":    {},
		"tri-this-sweet-treat": {},
		"from-the-playbook":    {},
		"jack-of-all-takes":    {},
		"the-green-angle":      {},
		"the-overall-score":    {},
	},
	"entertainment": {
		"movies":              {},
		"music":               {},
		"happening-in-philly": {},
		"cooking":             {},
		"books":               {},
		"gaming":              {},
		"listicles":           {},
		"performing-arts":     {},
		"the-drawing-board":   {},
	},
	"comics-puzzles": {
		"political-cartoons": {},
		"crossword":          {},
		"sudoku":             {},
		"comics":             {},
		"puzzles":            {},
		"satire":             {},
	},
	"graduation": {},
}

func normalizeAndValidateArticleParams(ctx context.Context, conn *sql.DB, params ArticleParams) (ArticleParams, error) {
	params.AuthorSlug = strings.TrimSpace(params.AuthorSlug)
	params.AuthorSearch = strings.TrimSpace(params.AuthorSearch)
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
			return ArticleParams{}, errSectionNotFound
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
			return ArticleParams{}, errSubsectionNotFound
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
