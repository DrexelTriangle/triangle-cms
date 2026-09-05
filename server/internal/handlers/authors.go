package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"strconv"
	"strings"
)

// authorArchiveCondition honours ?archived only for an identified editor.
// /v1/authors is public, and soft-deleted authors are not public information.
func authorArchiveCondition(q url.Values, isEditor bool) string {
	if _, archivedProvided := q["archived"]; archivedProvided && isEditor {
		archivedRaw := strings.ToLower(strings.TrimSpace(q.Get("archived")))
		switch archivedRaw {
		case "", "1", "true", "yes":
			return "a.`archived_at` IS NOT NULL"
		case "0", "false", "no":
			return "a.`archived_at` IS NULL"
		default:
			return "a.`archived_at` IS NOT NULL"
		}
	}
	return "a.`archived_at` IS NULL"
}

func authorIDsFromOverviews(authors []models.AuthorOverview) []int64 {
	ids := make([]int64, 0, len(authors))
	for _, author := range authors {
		ids = append(ids, author.ID)
	}
	return ids
}

func parseAuthorIDs(raw any) ([]int64, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, strconv.ErrSyntax
	}

	authorIDs := make([]int64, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case float64:
			if v != float64(int64(v)) {
				return nil, strconv.ErrSyntax
			}
			authorIDs = append(authorIDs, int64(v))
		case int64:
			authorIDs = append(authorIDs, v)
		default:
			return nil, strconv.ErrSyntax
		}
	}

	return authorIDs, nil
}

// @Summary List authors
// @Tags authors
// @Produce json
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param article_id query int false "Filter by article ID"
// @Param search query string false "Filter by display name, login, or email (substring match)"
// @Param archived query bool false "When true, return only soft-deleted authors. Ignored for unauthenticated callers."
// @Param sort_by query string false "Sort field (id sorts by creation order)" Enums(display_name,id)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} models.AuthorsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors [get]
func GetAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)
		page := intParam(r, "page", 0)
		if page <= 0 {
			page = 1
			if limit > 0 {
				page = (offset / limit) + 1
			}
		}
		articleID := intParam(r, "article_id", 0)
		sortBy := q.Get("sort_by")
		if _, ok := db.AuthorSortByColumn[sortBy]; !ok {
			sortBy = ""
		}

		var conditions []string
		var args []any

		if articleID > 0 {
			conditions = append(conditions, "a.`id` IN (SELECT `author_id` FROM `articles_authors` WHERE `articles_id` = ?)")
			args = append(args, articleID)
		}

		if search := strings.TrimSpace(q.Get("search")); search != "" {
			like := "%" + search + "%"
			conditions = append(conditions, "(a.`display_name` LIKE ? OR a.`login` LIKE ? OR a.`email` LIKE ?)")
			args = append(args, like, like, like)
		}

		_, isEditor := middleware.UserFromContext(r.Context())
		conditions = append(conditions, authorArchiveCondition(q, isEditor))

		countQuery := "SELECT COUNT(*) FROM `authors` a"
		if len(conditions) > 0 {
			countQuery += " WHERE " + strings.Join(conditions, " AND ")
		}
		var totalCount int
		if err := conn.QueryRowContext(r.Context(), countQuery, args...).Scan(&totalCount); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		query := "SELECT a.`id`, a.`display_name`, a.`login`, a.`email`, COUNT(aa.`articles_id`) AS `article_count`, a.`archived_at` FROM `authors` a LEFT JOIN `articles_authors` aa ON a.`id` = aa.`author_id`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY a.`id`, a.`display_name`, a.`login`, a.`email`, a.`archived_at`"
		if sortBy == "" {
			query += " ORDER BY a.`id` DESC"
		}
		query = db.BuildOrderLimit(query, sortBy, q.Get("sort_direction"), db.AuthorSortByColumn, limit, offset)

		rows, err := conn.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		authors := make([]models.AuthorOverview, 0)
		for rows.Next() {
			a, err := db.ScanAuthorOverview(rows)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authors = append(authors, a)
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := offset+len(authors) < totalCount
		writeJSON(w, http.StatusOK, models.AuthorsResponse{
			Authors:    authors,
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary Create an author
// @Tags authors
// @Accept json
// @Param body body models.AuthorInput true "Author"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors [post]
func PostAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body models.AuthorInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		slug := strings.TrimSpace(body.Slug)
		if slug == "" {
			writeError(w, http.StatusBadRequest, "slug is required")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		_, err := db.Insert(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			body.DisplayName, body.FirstName, body.LastName, body.Email, slug,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "author_created", body.DisplayName, "slug", slug)
		w.WriteHeader(http.StatusCreated)
	}
}

// @Summary Get an author by slug
// @Tags authors
// @Produce json
// @Param slug path string true "Author slug"
// @Success 200 {object} models.Author
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors/{slug} [get]
func GetAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		rows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		a, err := db.ScanAuthor(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// @Summary Replace an author
// @Tags authors
// @Accept json
// @Param slug path string true "Author slug"
// @Param body body models.AuthorInput true "Author"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [put]
func PutAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		var body models.AuthorInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		nextSlug := slug
		if body.Slug != "" {
			nextSlug = strings.TrimSpace(body.Slug)
			if !isValidCanonicalSlug(nextSlug) {
				writeError(w, http.StatusBadRequest, "slug must be canonical")
				return
			}
		}
		result, err := db.Update(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			"`login` = ? AND `archived_at` IS NULL",
			body.DisplayName, body.FirstName, body.LastName, body.Email, nextSlug, slug,
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
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_updated", body.DisplayName, "old_slug", slug, "new_slug", nextSlug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Partially update an author
// @Tags authors
// @Accept json
// @Param slug path string true "Author slug"
// @Param body body models.AuthorPatch true "Fields to update"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [patch]
func PatchAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var setCols []string
		var setArgs []any
		for _, col := range []string{"display_name", "first_name", "last_name", "email"} {
			if v, ok := body[col]; ok {
				setCols = append(setCols, col)
				setArgs = append(setArgs, v)
			}
		}
		if v, ok := body["slug"]; ok {
			rawSlug, ok := v.(string)
			if !ok {
				writeError(w, http.StatusBadRequest, "slug must be a string")
				return
			}
			trimmed := strings.TrimSpace(rawSlug)
			if !isValidCanonicalSlug(trimmed) {
				writeError(w, http.StatusBadRequest, "slug must be canonical")
				return
			}
			setCols = append(setCols, "login")
			setArgs = append(setArgs, trimmed)
		}
		if len(setCols) == 0 {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		result, err := db.Update(r.Context(), conn, "authors", setCols, "`login` = ? AND `archived_at` IS NULL", append(setArgs, slug)...)
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
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		target := slug
		if rawSlug, ok := body["slug"].(string); ok && strings.TrimSpace(rawSlug) != "" {
			target = strings.TrimSpace(rawSlug)
		}
		if displayName, ok := body["display_name"].(string); ok && strings.TrimSpace(displayName) != "" {
			target = strings.TrimSpace(displayName)
		}
		activity.LogRequest(r, "author_updated", target, "slug", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Delete an author
// @Tags authors
// @Param slug path string true "Author slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [delete]
func DeleteAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `authors` SET `archived_at` = NOW() WHERE `login` = ? AND `archived_at` IS NULL", slug)
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
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_deleted", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Restore an archived author
// @Tags authors
// @Param slug path string true "Author slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug}/restore [patch]
func RestoreAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `authors` SET `archived_at` = NULL WHERE `login` = ? AND `archived_at` IS NOT NULL", slug)
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
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_restored", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary List articles by author
// @Tags authors
// @Produce json
// @Param slug path string true "Author slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Param section_slug query string false "Filter by section"
// @Param subsection_slug query string false "Filter by subsection"
// @Success 200 {object} models.AuthorArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors/{slug}/articles [get]
func GetAuthorArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		authorRows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer authorRows.Close()
		if !authorRows.Next() {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		author, err := db.ScanAuthor(authorRows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		params, err := normalizeAndValidateArticleParams(r.Context(), conn, ArticleParams{
			AuthorSlug: slug,
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		page, limit, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		totalCount, err := countArticles(r, conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := db.CollectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(articles) > limit
		if hasMore {
			articles = articles[:limit]
		}

		setPublicReadCache(w, r)
		writeJSON(w, http.StatusOK, models.AuthorArticlesResponse{
			Author: models.AuthorArticlesAuthor{
				ID:          author.ID,
				Slug:        author.Slug,
				DisplayName: author.DisplayName,
			},
			Articles:   articleListItems(articles, excerptWords),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}
