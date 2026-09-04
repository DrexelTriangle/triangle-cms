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
		parentSection, ok, err := rootSectionForSubsection(ctx, conn, params.Subsection)
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

		// A subsection can be a container too now, so its page needs its own
		// descendants for the same reason a section's does.
		matchSlugs, err := db.TaxonomyDescendants(ctx, conn, params.Subsection)
		if err != nil {
			return ArticleParams{}, err
		}
		if len(matchSlugs) == 0 {
			matchSlugs = []string{params.Subsection}
		}
		params.SubsectionMatchSlugs = matchSlugs
	}

	return params, nil
}

func normalizeSectionSlug(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// site_taxonomy is the only list of what a section is. Without a handle there
// is nothing to check against, so every slug is unknown: a hard-coded mirror of
// the tree here would answer for the seven sections it was written with and
// deny the ones an editor has added since.
func taxonomySectionExists(ctx context.Context, conn *sql.DB, section string) (bool, error) {
	if conn == nil {
		return false, nil
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

// sectionMatchSlugs returns the section plus every subsection below it, at any
// depth, which together define what "articles in this section" means. Without a
// database handle there are no subsections to find, so the section stands alone.
//
// Transitive because the tree is three levels: A&E holds Food, and Food holds
// Beer Reviews. One hop would have listed Food's own articles under A&E while
// dropping everything filed under the three review categories beneath it.
func sectionMatchSlugs(ctx context.Context, conn *sql.DB, section string) ([]string, error) {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return nil, nil
	}
	if conn == nil {
		return []string{trimmed}, nil
	}
	return db.TaxonomyDescendants(ctx, conn, trimmed)
}

// rootSectionForSubsection resolves the SECTION a subsection ultimately belongs
// to, walking up however many levels sit between them.
//
// The root rather than the immediate parent, because this backs the check that
// ?subsection_slug= agrees with ?section_slug=, and the caller names a section.
// Returning "food" for beer-reviews would fail that check against the only
// section a reader could have arrived from.
//
// Without a handle nothing resolves, for the reason taxonomySectionExists gives.
func rootSectionForSubsection(ctx context.Context, conn *sql.DB, subsection string) (string, bool, error) {
	if conn == nil {
		return "", false, nil
	}

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
	if !parent.Valid || strings.TrimSpace(parent.String) == "" {
		return "", false, nil
	}
	ancestors, err := db.TaxonomyAncestors(ctx, conn, subsection)
	if err != nil {
		return "", false, err
	}
	if len(ancestors) == 0 {
		return "", false, nil
	}
	// Nearest first, so the last entry is the top of the chain.
	return ancestors[len(ancestors)-1], true, nil
}
