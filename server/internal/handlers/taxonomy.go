package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

var validTaxonomyKinds = map[string]bool{
	string(models.TaxonomyKindSection):    true,
	string(models.TaxonomyKindSubsection): true,
	string(models.TaxonomyKindTag):        true,
}

func isValidTaxonomyKind(kind string) bool {
	return validTaxonomyKinds[strings.TrimSpace(kind)]
}

func scanTaxonomyRow(row interface{ Scan(...any) error }) (models.TaxonomyItem, error) {
	var item models.TaxonomyItem
	var parentSlug sql.NullString
	if err := row.Scan(&item.ID, &item.Kind, &item.Slug, &item.CanonicalTitle, &parentSlug); err != nil {
		return models.TaxonomyItem{}, err
	}
	if parentSlug.Valid && parentSlug.String != "" {
		s := parentSlug.String
		item.ParentSlug = &s
	}
	return item, nil
}

// GET /v1/taxonomy
func GetTaxonomy(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))

		var query string
		var args []any
		if kind != "" {
			if !isValidTaxonomyKind(kind) {
				writeError(w, http.StatusBadRequest, "kind must be one of: section, subsection, tag")
				return
			}
			query = "SELECT id, kind, slug, canonical_title, parent_slug FROM site_taxonomy WHERE kind = ? ORDER BY id ASC"
			args = []any{kind}
		} else {
			query = "SELECT id, kind, slug, canonical_title, parent_slug FROM site_taxonomy ORDER BY kind ASC, id ASC"
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

// GET /v1/taxonomy/{kind}/{slug}
func GetTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimSpace(r.PathValue("kind"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyKind(kind) {
			writeError(w, http.StatusBadRequest, "kind must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		row := conn.QueryRowContext(r.Context(),
			"SELECT id, kind, slug, canonical_title, parent_slug FROM site_taxonomy WHERE kind = ? AND slug = ?",
			kind, slug,
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

// POST /v1/taxonomy
func PostTaxonomy(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body models.TaxonomyInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		kind := strings.TrimSpace(body.Kind)
		slug := strings.TrimSpace(body.Slug)
		title := strings.TrimSpace(body.CanonicalTitle)

		if !isValidTaxonomyKind(kind) {
			writeError(w, http.StatusBadRequest, "kind must be one of: section, subsection, tag")
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
			[]string{"id", "kind", "slug", "canonical_title", "parent_slug"},
			nextID, kind, slug, title, parentSlug,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": nextID})
	}
}

// PUT /v1/taxonomy/{kind}/{slug}
func PutTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimSpace(r.PathValue("kind"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyKind(kind) {
			writeError(w, http.StatusBadRequest, "kind must be one of: section, subsection, tag")
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
			newSlug, title, parentSlug, kind, slug,
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
		w.WriteHeader(http.StatusNoContent)
	}
}

// DELETE /v1/taxonomy/{kind}/{slug}
func DeleteTaxonomyItem(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimSpace(r.PathValue("kind"))
		slug := strings.TrimSpace(r.PathValue("slug"))

		if !isValidTaxonomyKind(kind) {
			writeError(w, http.StatusBadRequest, "kind must be one of: section, subsection, tag")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		result, err := db.Delete(r.Context(), conn, "site_taxonomy", "kind = ? AND slug = ?", kind, slug)
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
		w.WriteHeader(http.StatusNoContent)
	}
}
