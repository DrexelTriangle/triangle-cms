package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

func scanTaxonomyRow(row interface{ Scan(...any) error }) (models.TaxonomyItem, error) {
	var item models.TaxonomyItem
	var parentSlug sql.NullString
	if err := row.Scan(&item.ID, &item.Type, &item.Slug, &item.CanonicalTitle, &parentSlug, &item.ArticleCount); err != nil {
		return models.TaxonomyItem{}, err
	}
	if parentSlug.Valid && parentSlug.String != "" {
		s := parentSlug.String
		item.ParentSlug = &s
	}
	return item, nil
}

// @Summary List taxonomy items
// @Tags taxonomy
// @Produce json
// @Param type query string false "Filter by type" Enums(section,subsection,tag)
// @Success 200 {array} models.TaxonomyItem
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
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
			query = "SELECT id, kind, slug, canonical_title, parent_slug, article_count FROM site_taxonomy WHERE kind = ? ORDER BY id ASC"
			args = []any{taxType}
		} else {
			query = "SELECT id, kind, slug, canonical_title, parent_slug, article_count FROM site_taxonomy ORDER BY kind ASC, id ASC"
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
// @Security BearerAuth
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
			"SELECT id, kind, slug, canonical_title, parent_slug, article_count FROM site_taxonomy WHERE kind = ? AND slug = ?",
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
		if body.ParentSlug != nil {
			ps := strings.TrimSpace(*body.ParentSlug)
			if ps != "" && !isValidCanonicalSlug(ps) {
				writeError(w, http.StatusBadRequest, "parent_slug must be canonical")
				return
			}
		}

		var nextID int64
		if err := conn.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(id), 0) + 1 FROM site_taxonomy").Scan(&nextID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		var parentSlug any
		if body.ParentSlug != nil && strings.TrimSpace(*body.ParentSlug) != "" {
			parentSlug = strings.TrimSpace(*body.ParentSlug)
		}

		_, err := db.Insert(r.Context(), conn, "site_taxonomy",
			[]string{"id", "kind", "slug", "canonical_title", "parent_slug", "article_count"},
			nextID, taxType, slug, title, parentSlug, 0,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
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

		if body.ParentSlug != nil {
			ps := strings.TrimSpace(*body.ParentSlug)
			if ps != "" && !isValidCanonicalSlug(ps) {
				writeError(w, http.StatusBadRequest, "parent_slug must be canonical")
				return
			}
		}

		var parentSlug any
		if body.ParentSlug != nil && strings.TrimSpace(*body.ParentSlug) != "" {
			parentSlug = strings.TrimSpace(*body.ParentSlug)
		}

		result, err := db.Update(r.Context(), conn, "site_taxonomy",
			[]string{"slug", "canonical_title", "parent_slug"},
			"kind = ? AND slug = ?",
			newSlug, title, parentSlug, taxType, slug,
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
		action := "taxonomy_deleted"
		if taxType == string(models.TaxonomyTypeTag) {
			action = "tag_deleted"
		}
		activity.LogRequest(r, action, fmt.Sprintf("%s: %s", taxType, slug))
		w.WriteHeader(http.StatusNoContent)
	}
}
