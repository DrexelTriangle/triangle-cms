package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

var validTaxonomyTypes = map[string]bool{
	string(models.TaxonomyTypeSection):    true,
	string(models.TaxonomyTypeSubsection): true,
	string(models.TaxonomyTypeTag):        true,
}

func isValidTaxonomyType(taxType string) bool {
	return validTaxonomyTypes[strings.TrimSpace(taxType)]
}

// taxonomySelectColumns is the column list every taxonomy read shares, kept in
// one place so a new column cannot be added to some queries and missed by
// others -- scanTaxonomyRow depends on this exact order.
const taxonomySelectColumns = "id, kind, slug, canonical_title, parent_slug, article_count, category_aliases"

func scanTaxonomyRow(row interface{ Scan(...any) error }) (models.TaxonomyItem, error) {
	var item models.TaxonomyItem
	var parentSlug sql.NullString
	var aliases sql.NullString
	if err := row.Scan(&item.ID, &item.Type, &item.Slug, &item.CanonicalTitle, &parentSlug, &item.ArticleCount, &aliases); err != nil {
		return models.TaxonomyItem{}, err
	}
	if parentSlug.Valid && parentSlug.String != "" {
		s := parentSlug.String
		item.ParentSlug = &s
	}
	parsed, err := db.ParseCategoryAliases(aliases.String)
	if err != nil {
		return models.TaxonomyItem{}, err
	}
	// Always a list, never null, so the editor can bind to it directly.
	item.CategoryAliases = parsed
	if item.CategoryAliases == nil {
		item.CategoryAliases = []string{}
	}
	return item, nil
}

// normalizeCategoryAliases trims and de-duplicates, dropping blanks. Returns
// the JSON to store; an empty list is stored as [] rather than NULL so it stays
// distinguishable from "never set", which is what the defaults seed on.
func normalizeCategoryAliases(aliases []string) (string, error) {
	cleaned := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		trimmed := strings.TrimSpace(alias)
		if trimmed == "" {
			continue
		}
		duplicate := false
		for _, existing := range cleaned {
			if strings.EqualFold(existing, trimmed) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			cleaned = append(cleaned, trimmed)
		}
	}
	return db.MarshalCategoryJSON(cleaned)
}

// refreshTaxonomyDerivedState brings everything downstream of a taxonomy write
// back in line: the in-memory aliases that article matching reads, and the
// stored article_count the sections screen shows.
//
// Both matter for the same reason. Matching updates the moment the cache
// reloads, but article_count is a stored number, so without recounting an
// editor who fixes a section sees it start working on the site while the screen
// still shows 0 -- a successful fix that looks like a failed one. Only the
// touched rows are recounted; see RebuildTaxonomyArticleCountsFor.
//
// Neither failure fails the request: the row is already saved, and both are
// recoverable at the next write or restart. They are logged loudly instead,
// because the symptom otherwise is an edit that appears to do nothing.
//
// Callers pass every slug the write touched, INCLUDING a parent whose row is
// about to disappear -- parents cannot be resolved once the child is deleted.
func refreshTaxonomyDerivedState(r *http.Request, conn *sql.DB, slugs ...string) {
	if conn == nil {
		return
	}
	if err := db.RefreshCategoryAliases(r.Context(), conn); err != nil {
		slog.Error("failed to refresh category alias cache", "error", err)
	}
	if err := db.RebuildTaxonomyArticleCountsFor(r.Context(), conn, slugs...); err != nil {
		slog.Error("failed to recount taxonomy articles after a write", "slugs", slugs, "error", err)
	}
}

// parentSlugForRecount narrows validateTaxonomyParent's any-typed result to the
// slug string, or "" for a section.
func parentSlugForRecount(parentSlug any) string {
	if parent, ok := parentSlug.(string); ok {
		return parent
	}
	return ""
}

func taxonomyItemArticleCount(ctx context.Context, conn *sql.DB, taxType, slug string) (int64, error) {
	var count int64
	err := conn.QueryRowContext(ctx,
		"SELECT article_count FROM site_taxonomy WHERE kind = ? AND slug = ?",
		taxType, slug,
	).Scan(&count)
	return count, err
}

// liveTaxonomyArticleCount counts what the item matches RIGHT NOW, rather than
// trusting the stored article_count.
//
// The stored count is only as fresh as the last rebuild, so it reads 0 both for
// an item that is genuinely empty and for one whose articles simply have not
// been counted yet -- after a reseed, or after an alias was just added. Deletion
// is guarded on "nothing uses this", so it has to ask the articles table, not a
// cached number. Everything else can keep using the cheap stored value.
func liveTaxonomyArticleCount(ctx context.Context, conn *sql.DB, slug string) (int64, error) {
	condition, args := db.TaxonomyCountCondition([]string{slug})
	if condition == "" {
		return 0, nil
	}
	var count int64
	query := "SELECT COUNT(*) FROM `articles` WHERE `archived_at` IS NULL AND " + condition
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func taxonomyItemExists(ctx context.Context, conn *sql.DB, taxType, slug string) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
		taxType, slug,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func taxonomySectionHasChildren(ctx context.Context, conn *sql.DB, sectionSlug string) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM site_taxonomy WHERE kind = ? AND parent_slug = ? LIMIT 1",
		string(models.TaxonomyTypeSubsection), sectionSlug,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func validateTaxonomyParent(ctx context.Context, conn *sql.DB, taxType string, parentSlug *string) (any, error) {
	if parentSlug == nil || strings.TrimSpace(*parentSlug) == "" {
		if taxType == string(models.TaxonomyTypeSubsection) {
			return nil, fmt.Errorf("parent_slug is required for subsections")
		}
		return nil, nil
	}

	parent := strings.TrimSpace(*parentSlug)
	if !isValidCanonicalSlug(parent) {
		return nil, fmt.Errorf("parent_slug must be canonical")
	}
	if taxType != string(models.TaxonomyTypeSubsection) {
		return nil, fmt.Errorf("parent_slug is only allowed for subsections")
	}
	if conn == nil {
		return parent, nil
	}

	exists, err := taxonomyItemExists(ctx, conn, string(models.TaxonomyTypeSection), parent)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("parent_slug must reference an existing section")
	}
	return parent, nil
}

// @Summary List taxonomy items
// @Tags taxonomy
// @Produce json
// @Param type query string false "Filter by type" Enums(section,subsection,tag)
// @Success 200 {array} models.TaxonomyItem
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /v1/taxonomy [get]
func GetTaxonomy(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taxType := strings.TrimSpace(r.URL.Query().Get("type"))

		var query string
		var args []any
		if taxType != "" {
			if !isValidTaxonomyType(taxType) {
				writeError(w, http.StatusBadRequest, "type must be one of: section, subsection, tag")
				return
			}
			query = "SELECT " + taxonomySelectColumns + " FROM site_taxonomy WHERE kind = ? ORDER BY id ASC"
			args = []any{taxType}
		} else {
			query = "SELECT " + taxonomySelectColumns + " FROM site_taxonomy ORDER BY kind ASC, id ASC"
		}

		rows, err := conn.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		items := make([]models.TaxonomyItem, 0)
		for rows.Next() {
			item, err := scanTaxonomyRow(rows)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

// @Summary Get a taxonomy item
// @Tags taxonomy
// @Produce json
// @Param type path string true "Type" Enums(section,subsection,tag)
// @Param slug path string true "Slug"
// @Success 200 {object} models.TaxonomyItem
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /v1/taxonomy/{type}/{slug} [get]
func GetTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taxType := strings.TrimSpace(r.PathValue("type"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyType(taxType) {
			writeError(w, http.StatusBadRequest, "type must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		row := conn.QueryRowContext(r.Context(),
			"SELECT "+taxonomySelectColumns+" FROM site_taxonomy WHERE kind = ? AND slug = ?",
			taxType, slug,
		)
		item, err := scanTaxonomyRow(row)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "taxonomy item not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// @Summary Create a taxonomy item
// @Tags taxonomy
// @Accept json
// @Produce json
// @Param body body models.TaxonomyInput true "Taxonomy item"
// @Success 201 {object} map[string]int64
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /v1/taxonomy [post]
func PostTaxonomy(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body models.TaxonomyInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		taxType := strings.TrimSpace(body.Type)
		slug := strings.TrimSpace(body.Slug)
		title := strings.TrimSpace(body.CanonicalTitle)

		if !isValidTaxonomyType(taxType) {
			writeError(w, http.StatusBadRequest, "type must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if title == "" {
			writeError(w, http.StatusBadRequest, "canonical_title is required")
			return
		}
		parentSlug, err := validateTaxonomyParent(r.Context(), conn, taxType, body.ParentSlug)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		var nextID int64
		if err := conn.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(id), 0) + 1 FROM site_taxonomy").Scan(&nextID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		aliases, err := normalizeCategoryAliases(body.CategoryAliases)
		if err != nil {
			writeError(w, http.StatusBadRequest, "category_aliases must be a list of strings")
			return
		}

		_, err = db.Insert(r.Context(), conn, "site_taxonomy",
			[]string{"id", "kind", "slug", "canonical_title", "parent_slug", "article_count", "category_aliases"},
			nextID, taxType, slug, title, parentSlug, 0, aliases,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		refreshTaxonomyDerivedState(r, conn, slug, parentSlugForRecount(parentSlug))
		action := "taxonomy_created"
		if taxType == string(models.TaxonomyTypeTag) {
			action = "tag_created"
		}
		activity.LogRequest(r, action, fmt.Sprintf("%s: %s", taxType, title), "slug", slug)
		writeJSON(w, http.StatusCreated, map[string]any{"id": nextID})
	}
}

// @Summary Update a taxonomy item
// @Tags taxonomy
// @Accept json
// @Param type path string true "Type" Enums(section,subsection,tag)
// @Param slug path string true "Slug"
// @Param body body models.TaxonomyPut true "Updated fields"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /v1/taxonomy/{type}/{slug} [put]
func PutTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taxType := strings.TrimSpace(r.PathValue("type"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyType(taxType) {
			writeError(w, http.StatusBadRequest, "type must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		var body models.TaxonomyPut
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		title := strings.TrimSpace(body.CanonicalTitle)
		if title == "" {
			writeError(w, http.StatusBadRequest, "canonical_title is required")
			return
		}

		newSlug := slug
		if body.Slug != "" {
			newSlug = strings.TrimSpace(body.Slug)
			if !isValidCanonicalSlug(newSlug) {
				writeError(w, http.StatusBadRequest, "slug must be canonical")
				return
			}
		}

		parentSlug, err := validateTaxonomyParent(r.Context(), conn, taxType, body.ParentSlug)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if newSlug != slug && conn != nil {
			count, err := taxonomyItemArticleCount(r.Context(), conn, taxType, slug)
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "taxonomy item not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if count > 0 {
				writeError(w, http.StatusBadRequest, "slug cannot be changed while articles use this taxonomy item")
				return
			}
			if taxType == string(models.TaxonomyTypeSection) {
				hasChildren, err := taxonomySectionHasChildren(r.Context(), conn, slug)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if hasChildren {
					writeError(w, http.StatusBadRequest, "section slug cannot be changed while subsections use it")
					return
				}
			}
		}

		// Omitting category_aliases leaves the stored value alone; sending []
		// clears it. Without that distinction, every edit made from a client
		// that does not know about aliases would silently wipe them.
		columns := []string{"slug", "canonical_title", "parent_slug"}
		values := []any{newSlug, title, parentSlug}
		if body.CategoryAliases != nil {
			aliases, err := normalizeCategoryAliases(*body.CategoryAliases)
			if err != nil {
				writeError(w, http.StatusBadRequest, "category_aliases must be a list of strings")
				return
			}
			columns = append(columns, "category_aliases")
			values = append(values, aliases)
		}
		values = append(values, taxType, slug)

		result, err := db.Update(r.Context(), conn, "site_taxonomy",
			columns,
			"kind = ? AND slug = ?",
			values...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "taxonomy item not found")
			return
		}
		action := "taxonomy_updated"
		if taxType == string(models.TaxonomyTypeTag) {
			action = "tag_updated"
		}
		refreshTaxonomyDerivedState(r, conn, slug, newSlug, parentSlugForRecount(parentSlug))
		activity.LogRequest(r, action, fmt.Sprintf("%s: %s", taxType, title), "old_slug", slug, "new_slug", newSlug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Delete a taxonomy item
// @Tags taxonomy
// @Param type path string true "Type" Enums(section,subsection,tag)
// @Param slug path string true "Slug"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /v1/taxonomy/{type}/{slug} [delete]
func DeleteTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taxType := strings.TrimSpace(r.PathValue("type"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyType(taxType) {
			writeError(w, http.StatusBadRequest, "type must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		// Captured before the row goes away: once it is deleted its parent can
		// no longer be looked up, and the parent's count changes because a
		// section matches its own slug OR any of its children's.
		var deletedParentSlug string
		if conn != nil {
			var parent sql.NullString
			if err := conn.QueryRowContext(r.Context(),
				"SELECT parent_slug FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
				taxType, slug,
			).Scan(&parent); err == nil && parent.Valid {
				deletedParentSlug = strings.TrimSpace(parent.String)
			}
		}

		if conn != nil {
			// Existence check only -- the count it returns is the cached one.
			if _, err := taxonomyItemArticleCount(r.Context(), conn, taxType, slug); err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "taxonomy item not found")
				return
			} else if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			// Children first: it is an indexed lookup on one small table, and
			// it is the more specific reason to refuse, so it should not be
			// preempted by a scan of every article.
			if taxType == string(models.TaxonomyTypeSection) {
				hasChildren, err := taxonomySectionHasChildren(r.Context(), conn, slug)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if hasChildren {
					writeError(w, http.StatusBadRequest, "section cannot be deleted while subsections use it")
					return
				}
			}
			// Deletion is irreversible and editors can do it, so ask the
			// articles table rather than a number that may predate the last
			// reseed or alias change.
			count, err := liveTaxonomyArticleCount(r.Context(), conn, slug)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if count > 0 {
				writeError(w, http.StatusBadRequest, "taxonomy item cannot be deleted while articles use it")
				return
			}
		}

		result, err := db.Delete(r.Context(), conn, "site_taxonomy", "kind = ? AND slug = ?", taxType, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "taxonomy item not found")
			return
		}
		refreshTaxonomyDerivedState(r, conn, slug, deletedParentSlug)
		action := "taxonomy_deleted"
		if taxType == string(models.TaxonomyTypeTag) {
			action = "tag_deleted"
		}
		activity.LogRequest(r, action, fmt.Sprintf("%s: %s", taxType, slug))
		w.WriteHeader(http.StatusNoContent)
	}
}
