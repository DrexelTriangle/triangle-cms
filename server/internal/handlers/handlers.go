package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

// @Summary Liveness check
// @Tags health
// @Produce json
// @Success 200 {object} models.HealthResponse
// @Router /v1/health [get]
func HealthCheck(w http.ResponseWriter, r *http.Request) {

	writeJSON(w, http.StatusOK, models.HealthResponse{Status: "Ok"})
}

// @Summary Readiness check
// @Tags health
// @Produce json
// @Success 200 {object} models.HealthResponse
// @Failure 503 {object} models.ErrorResponse
// @Router /v1/health/db [get]
func HealthReady(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := conn.PingContext(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "Database unreachable")
			return
		}
		writeJSON(w, http.StatusOK, models.HealthResponse{Status: "Ok"})
	}
}

// @Summary Media endpoint placeholder
// @Tags media
// @Produce json
// @Failure 501 {object} models.ErrorResponse
// @Router /v1/media [get]
// @Router /v1/media/{id} [get]
// @Router /v1/media/gallery [get]
func Users(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}

func intParam(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, models.ErrorResponse{Error: msg})
}

func titleFromSlug(slug string) string {
	parts := strings.Split(strings.TrimSpace(slug), "-")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		words = append(words, string(runes))
	}
	return strings.Join(words, " ")
}

func excerptWordLimit(r *http.Request, fallback int) int {
	limit := intParam(r, "excerpt_words", fallback)
	if limit < 0 {
		return 0
	}
	return limit
}

func truncateWords(s string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}

	words := strings.Fields(strings.TrimSpace(s))
	if len(words) <= maxWords {
		return strings.TrimSpace(s)
	}
	return strings.Join(words[:maxWords], " ")
}

func canonicalTitleForTaxonomy(ctx context.Context, conn *sql.DB, kind, slug string) (string, error) {
	trimmedSlug := strings.TrimSpace(slug)
	if trimmedSlug == "" {
		return "", nil
	}

	var canonical sql.NullString
	err := conn.QueryRowContext(
		ctx,
		"SELECT canonical_title FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
		kind,
		trimmedSlug,
	).Scan(&canonical)
	if err == sql.ErrNoRows {
		return titleFromSlug(trimmedSlug), nil
	}
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(canonical.String)
	if title == "" {
		return titleFromSlug(trimmedSlug), nil
	}
	return title, nil
}

func subsectionsForSection(ctx context.Context, conn *sql.DB, sectionSlug string) ([]models.TaxonomySummary, error) {
	trimmedSection := strings.TrimSpace(sectionSlug)
	if trimmedSection == "" {
		return []models.TaxonomySummary{}, nil
	}

	rows, err := conn.QueryContext(
		ctx,
		"SELECT slug, canonical_title FROM site_taxonomy WHERE kind = 'subsection' AND parent_slug = ? ORDER BY id ASC",
		trimmedSection,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subsections := make([]models.TaxonomySummary, 0)
	for rows.Next() {
		var slug string
		var canonicalTitle sql.NullString
		if err := rows.Scan(&slug, &canonicalTitle); err != nil {
			return nil, err
		}

		canonical := strings.TrimSpace(canonicalTitle.String)
		if canonical == "" {
			canonical = titleFromSlug(slug)
		}

		subsections = append(subsections, models.TaxonomySummary{
			Slug:           slug,
			Name:           canonical,
			CanonicalTitle: canonical,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return subsections, nil
}

func isValidCanonicalSlug(slug string) bool {
	return db.IsCanonicalSlug(strings.TrimSpace(slug))
}

func listParams(r *http.Request, defaultLimit int) (page, limit, offset int) {
	limit = intParam(r, "limit", defaultLimit)
	if limit <= 0 {
		limit = defaultLimit
	}

	page = intParam(r, "page", 0)
	if page > 0 {
		offset = (page - 1) * limit
	} else {
		offset = intParam(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}
		page = (offset / limit) + 1
	}
	return page, limit, offset
}

func articleListItems(articles []models.Article, excerptWords int) []models.ArticleListItem {
	items := make([]models.ArticleListItem, 0, len(articles))
	for _, article := range articles {
		categories := make([]models.CategorySummary, 0, len(article.Categories))
		for _, category := range article.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
		}

		authors := make([]models.AuthorSummary, 0, len(article.Authors))
		for _, author := range article.Authors {
			authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
		}

		item := models.ArticleListItem{
			Title:         article.Title,
			ID:            article.ID,
			Authors:       authors,
			Categories:    categories,
			Excerpt:       truncateWords(article.Excerpt, excerptWords),
			Slug:          article.Slug,
			Status:        article.Status,
			CommentStatus: article.CommentStatus,
			FeaturedImage: article.PhotoURL,
			IsFeatured:    article.IsFeatured,
		}
		item.PublishedDate = article.PublishedAt
		items = append(items, item)
	}
	return items
}

func paginationResponse(page, limit, offset int, hasMore bool, totalCount int) models.Pagination {
	return models.Pagination{
		Page:       page,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
		TotalCount: totalCount,
	}
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
// @Param sort_by query string false "Sort field" Enums(display_name,created_at,updated_at)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {array} models.AuthorOverview
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors [get]
func GetAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)
		articleID := intParam(r, "article_id", 0)
		sortBy := q.Get("sort_by")
		if _, ok := db.AuthorSortByColumn[sortBy]; !ok {
			sortBy = ""
		}

		var conditions []string
		var args []any

		if articleID > 0 {
			conditions = append(conditions, "`id` IN (SELECT `author_id` FROM `articles_authors` WHERE `articles_id` = ?)")
			args = append(args, articleID)
		}

		query := "SELECT a.`id`, a.`display_name`, a.`login`, a.`email`, COUNT(aa.`articles_id`) AS `article_count` FROM `authors` a LEFT JOIN `articles_authors` aa ON a.`id` = aa.`author_id`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY a.`id`, a.`display_name`, a.`login`, a.`email`"
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
		writeJSON(w, http.StatusOK, authors)
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
		rows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ?", slug)
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
			"`login` = ?",
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
		result, err := db.Update(r.Context(), conn, "authors", setCols, "`login` = ?", append(setArgs, slug)...)
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
		result, err := db.Delete(r.Context(), conn, "authors", "`login` = ?", slug)
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

		authorRows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ?", slug)
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

		params, err := normalizeAndValidateArticleParams(ArticleParams{
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

// @Summary List articles
// @Tags articles
// @Produce json
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Param author_slug query string false "Filter by author slug"
// @Param section_slug query string false "Filter by section slug"
// @Param subsection_slug query string false "Filter by subsection slug"
// @Param status query string false "Filter by status" Enums(draft,published)
// @Param title query string false "Filter by title (partial match)"
// @Param slug query string false "Filter by slug (partial match)"
// @Param sort_by query string false "Sort field" Enums(title,slug,creation_date,published_date,status,comment_status)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} models.ArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles [get]
func GetArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorSlug: r.URL.Query().Get("author_slug"),
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

		writeJSON(w, http.StatusOK, models.ArticlesResponse{
			Articles:   articleListItems(articles, excerptWords),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary List articles by section
// @Tags articles
// @Produce json
// @Param section_slug path string true "Section slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.SectionArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/sections/{section_slug}/articles [get]
func GetSectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorSlug: r.URL.Query().Get("author_slug"),
			Section:    r.PathValue("section_slug"),
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

		sectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "section", params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		subsections, err := subsectionsForSection(r.Context(), conn, params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, models.SectionArticlesResponse{
			Section: models.TaxonomySummary{
				Slug:           params.Section,
				Name:           sectionCanonicalTitle,
				CanonicalTitle: sectionCanonicalTitle,
			},
			Subsections: subsections,
			Articles:    articleListItems(articles, excerptWords),
			Pagination:  paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary List articles by subsection
// @Tags articles
// @Produce json
// @Param subsection_slug path string true "Subsection slug"
// @Param section_slug query string false "Parent section slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.SubsectionArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/subsections/{subsection_slug}/articles [get]
func GetSubsectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorSlug: r.URL.Query().Get("author_slug"),
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.PathValue("subsection_slug"),
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

		sectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "section", params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		subsectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "subsection", params.Subsection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, models.SubsectionArticlesResponse{
			Section: models.TaxonomySummary{
				Slug:           params.Section,
				Name:           sectionCanonicalTitle,
				CanonicalTitle: sectionCanonicalTitle,
			},
			Subsection: models.TaxonomySummary{
				Slug:           params.Subsection,
				Name:           subsectionCanonicalTitle,
				CanonicalTitle: subsectionCanonicalTitle,
			},
			Articles:   articleListItems(articles, excerptWords),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

type ArticleParams struct {
	AuthorSlug string
	Section    string
	Subsection string
}

func queryArticles(r *http.Request, conn *sql.DB, params ArticleParams, limit, offset int) (*sql.Rows, error) {
	q := r.URL.Query()
	conditions, args := articleQueryFilters(r, params)
	query := "SELECT `id`, `title`, `slug`, `description`, `text`, `excerpt`, `tags`, `categories`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url` FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if q.Get("sort_by") == "" {
		query += " ORDER BY `id` DESC"
	}
	query = db.BuildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), db.ArticleSortByColumn, limit, offset)

	return conn.QueryContext(r.Context(), query, args...)
}

func countArticles(r *http.Request, conn *sql.DB, params ArticleParams) (int, error) {
	conditions, args := articleQueryFilters(r, params)
	query := "SELECT COUNT(*) FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var totalCount int
	if err := conn.QueryRowContext(r.Context(), query, args...).Scan(&totalCount); err != nil {
		return 0, err
	}
	return totalCount, nil
}

func articleQueryFilters(r *http.Request, params ArticleParams) ([]string, []any) {
	q := r.URL.Query()
	var conditions []string
	var args []any

	conditions = append(conditions, "((TRIM(COALESCE(`authors`, '')) <> '' AND TRIM(`authors`) <> '[]') OR (TRIM(COALESCE(`categories`, '')) <> '' AND TRIM(`categories`) <> '[]'))")
	conditions = append(conditions, "`archived_at` IS NULL")

	if params.AuthorSlug != "" {
		conditions = append(conditions, "`id` IN (SELECT aa.`articles_id` FROM `articles_authors` aa JOIN `authors` au ON au.`id` = aa.`author_id` WHERE au.`login` = ?)")
		args = append(args, params.AuthorSlug)
	}

	if params.Section != "" {
		appendCategorySlugCondition(&conditions, &args, params.Section)
	}

	if params.Subsection != "" {
		appendCategorySlugCondition(&conditions, &args, params.Subsection)
	}

	if status := strings.TrimSpace(q.Get("status")); status != "" {
		switch strings.ToLower(status) {
		case string(models.ArticleStatusDraft):
			conditions = append(conditions, "`pub_date` IS NULL")
		case string(models.ArticleStatusPublished):
			conditions = append(conditions, "`pub_date` IS NOT NULL")
		}
	}

	if strings.EqualFold(strings.TrimSpace(q.Get("sort_by")), string(models.ArticleSortByPublishedAt)) &&
		strings.EqualFold(strings.TrimSpace(q.Get("sort_direction")), string(models.SortDirectionAscending)) {
		conditions = append(conditions, "`pub_date` IS NOT NULL")
	}

	if title := strings.TrimSpace(q.Get("title")); title != "" {
		conditions = append(conditions, "`title` LIKE ?")
		args = append(args, "%"+title+"%")
	}

	if articleType := strings.TrimSpace(q.Get("type")); articleType != "" {
		appendArticleTypeCondition(&conditions, &args, articleType)
	}
	if excludedType := strings.TrimSpace(q.Get("exclude_type")); excludedType != "" {
		appendArticleTypeCondition(&conditions, &args, excludedType, true)
	}

	if pub_date := db.ParsePublishedAt(q.Get("published_date")); pub_date != nil {
		conditions = append(conditions, "`pub_date` >= ?")
		args = append(args, pub_date.UTC().Format("2006-01-02 15:04:05"))
	}

	if creation_date := db.ParsePublishedAt(q.Get("creation_date")); creation_date != nil {
		conditions = append(conditions, "`creation_date` >= ?")
		args = append(args, creation_date.UTC().Format("2006-01-02 15:04:05"))
	}

	if slug := strings.TrimSpace(q.Get("slug")); slug != "" {
		conditions = append(conditions, "`slug` LIKE ?")
		args = append(args, "%"+slug+"%")
	}

	return conditions, args
}

func appendCategorySlugCondition(conditions *[]string, args *[]any, slug string) {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return
	}

	patterns := make([]string, 0, 3)
	addPattern := func(value string) {
		for _, existing := range patterns {
			if existing == value {
				return
			}
		}
		patterns = append(patterns, value)
	}

	addPattern("%" + normalized + "%")
	if strings.Contains(normalized, "-") {
		addPattern("%" + strings.ReplaceAll(normalized, "-", " ") + "%")
		addPattern("%" + strings.ReplaceAll(normalized, "-", " & ") + "%")
	}

	clauseParts := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		clauseParts = append(clauseParts, "LOWER(`categories`) LIKE ?")
		*args = append(*args, pattern)
	}

	*conditions = append(*conditions, "("+strings.Join(clauseParts, " OR ")+")")
}

func appendArticleTypeCondition(conditions *[]string, args *[]any, rawType string, negate ...bool) {
	normalized := strings.ToLower(strings.TrimSpace(rawType))
	if normalized == "" {
		return
	}

	patterns := make([]string, 0, 4)
	addPattern := func(value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		for _, existing := range patterns {
			if existing == value {
				return
			}
		}
		patterns = append(patterns, value)
	}

	addPattern("%" + normalized + "%")
	addPattern("%" + strings.ReplaceAll(normalized, "-", " ") + "%")
	addPattern("%" + strings.ReplaceAll(normalized, "_", " ") + "%")

	clauseParts := make([]string, 0, len(patterns)*3)
	for _, pattern := range patterns {
		clauseParts = append(clauseParts, "LOWER(`categories`) LIKE ?")
		*args = append(*args, pattern)
		clauseParts = append(clauseParts, "LOWER(`tags`) LIKE ?")
		*args = append(*args, pattern)
		clauseParts = append(clauseParts, "LOWER(COALESCE(`metadata`, '')) LIKE ?")
		*args = append(*args, pattern)
	}

	clause := "(" + strings.Join(clauseParts, " OR ") + ")"
	if len(negate) > 0 && negate[0] {
		clause = "NOT " + clause
	}
	*conditions = append(*conditions, clause)
}

// @Summary Search articles
// @Tags articles
// @Produce json
// @Param q query string true "Search term"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Success 200 {array} models.ArticleListItem
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/search [get]
func GetSearch(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusOK, []any{})
			return
		}

		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)
		articles, err := db.SearchArticles(r.Context(), conn, q, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.ArticleListItem, 0, len(articles))
		for _, article := range articles {
			categories := make([]models.CategorySummary, 0, len(article.Categories))
			for _, category := range article.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				categories = append(categories, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
			}

			authors := make([]models.AuthorSummary, 0, len(article.Authors))
			for _, author := range article.Authors {
				authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
			}

			item := models.ArticleListItem{
				Title:         article.Title,
				ID:            article.ID,
				Authors:       authors,
				Categories:    categories,
				Excerpt:       article.Excerpt,
				Slug:          article.Slug,
				Status:        article.Status,
				CommentStatus: article.CommentStatus,
				FeaturedImage: article.PhotoURL,
				IsFeatured:    article.IsFeatured,
			}
			item.PublishedDate = article.PublishedAt
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// @Summary Get an article by slug
// @Tags articles
// @Produce json
// @Param slug path string true "Article slug"
// @Success 200 {object} models.ArticleDetailResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug} [get]
func GetArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		rows, err := db.Select(r.Context(), conn, "articles", db.ArticleColumns, "`slug` = ?", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		a, err := db.ScanArticle(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		articles := []models.Article{a}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a = articles[0]
		categories := make([]models.CategorySummary, 0, len(a.Categories))
		for _, category := range a.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
		}

		seoTags := make([]models.CategorySummary, 0, len(a.Tags))
		for _, tag := range a.Tags {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			seoTags = append(seoTags, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
		}

		authors := make([]models.AuthorSummary, 0, len(a.Authors))
		for _, author := range a.Authors {
			authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
		}

		relatedArticles, err := db.GetRelatedArticlesBySlug(r.Context(), conn, a.Slug, 3)
		if err != nil {
			if !db.IsVectorSearchUnavailableError(err) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			relatedArticles = []models.Article{}
		}

		related := make([]models.ArticleListItem, 0, len(relatedArticles))
		for _, relatedArticle := range relatedArticles {
			relatedCategories := make([]models.CategorySummary, 0, len(relatedArticle.Categories))
			for _, category := range relatedArticle.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				relatedCategories = append(relatedCategories, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
			}

			relatedAuthors := make([]models.AuthorSummary, 0, len(relatedArticle.Authors))
			for _, author := range relatedArticle.Authors {
				relatedAuthors = append(relatedAuthors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
			}

			relatedItem := models.ArticleListItem{
				Title:         relatedArticle.Title,
				ID:            relatedArticle.ID,
				Authors:       relatedAuthors,
				Categories:    relatedCategories,
				Excerpt:       relatedArticle.Excerpt,
				Slug:          relatedArticle.Slug,
				Status:        relatedArticle.Status,
				CommentStatus: relatedArticle.CommentStatus,
				FeaturedImage: relatedArticle.PhotoURL,
				IsFeatured:    relatedArticle.IsFeatured,
			}
			relatedItem.PublishedDate = relatedArticle.PublishedAt
			related = append(related, relatedItem)
		}

		resp := models.ArticleDetailResponse{
			ID:            a.ID,
			Title:         a.Title,
			Slug:          a.Slug,
			Content:       a.Content,
			Excerpt:       a.Excerpt,
			Categories:    categories,
			CommentStatus: a.CommentStatus,
			IsFeatured:    a.IsFeatured,
			Status:        a.Status,
			FeaturedImage: a.PhotoURL,
			Authors:       authors,
			SEO: models.SEOResponse{
				SEOTitle:        "",
				MetaDescription: "",
				FocusKeyword:    "",
				CanonicalURL:    "",
				Tags:            seoTags,
			},
			Related: related,
		}
		resp.PublishedDate = a.PublishedAt

		writeJSON(w, http.StatusOK, resp)
	}
}

// @Summary Create an article
// @Tags articles
// @Accept json
// @Param body body models.ArticleInput true "Article"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles [post]
func PostArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body models.ArticleInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if rawSlug := strings.TrimSpace(body.Slug); rawSlug != "" && !isValidCanonicalSlug(rawSlug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		fields := db.ArticleInputToDBFields(body)
		result, err := db.Insert(r.Context(), conn, "articles",
			[]string{"title", "slug", "description", "text", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		articleID, err := result.LastInsertId()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.ReplaceArticleAuthors(r.Context(), conn, articleID, body.Authors); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

// @Summary Replace an article
// @Tags articles
// @Accept json
// @Param slug path string true "Article slug"
// @Param body body models.Article true "Article"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [put]
func PutArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if user, ok := middleware.UserFromContext(r.Context()); ok && user.Role != models.RoleAdmin {
			if user.AuthorID == nil {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			owned, err := db.ArticleHasAuthor(r.Context(), conn, slug, *user.AuthorID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !owned {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
		}
		var body models.Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.Slug) == "" {
			body.Slug = slug
		} else if !isValidCanonicalSlug(body.Slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		fields := db.ArticleToDBFields(body)
		fields = append(fields, slug)
		result, err := db.Update(r.Context(), conn, "articles",
			[]string{"title", "slug", "excerpt", "text", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
			"`slug` = ?",
			fields...,
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
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if err := db.ReplaceArticleAuthorsBySlug(r.Context(), conn, body.Slug, authorIDsFromOverviews(body.Authors)); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Partially update an article
// @Tags articles
// @Accept json
// @Param slug path string true "Article slug"
// @Param body body models.ArticlePatch true "Fields to update"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [patch]
func PatchArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if user, ok := middleware.UserFromContext(r.Context()); ok && user.Role != models.RoleAdmin {
			if user.AuthorID == nil {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			owned, err := db.ArticleHasAuthor(r.Context(), conn, slug, *user.AuthorID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !owned {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var authorIDs *[]int64
		if rawAuthors, ok := body["authors"]; ok {
			parsedIDs, err := parseAuthorIDs(rawAuthors)
			if err != nil {
				writeError(w, http.StatusBadRequest, "authors must be an array of IDs")
				return
			}
			authorIDs = &parsedIDs
		}
		var setCols []string
		var setArgs []any
		columnByJSONField := map[string]string{
			"title":          "title",
			"slug":           "slug",
			"excerpt":        "excerpt",
			"content":        "text",
			"categories":     "categories",
			"published_date": "pub_date",
			"is_featured":    "priority",
			"status":         "pub_date",
			"comment_status": "comment_status",
			"photo_url":      "photo_url",
		}
		for jsonField, column := range columnByJSONField {
			v, ok := body[jsonField]
			if !ok {
				continue
			}
			switch jsonField {
			case "categories":
				arr, ok := v.([]any)
				if !ok {
					writeError(w, http.StatusBadRequest, "categories must be an array of strings")
					return
				}
				categories := make([]string, 0, len(arr))
				for _, raw := range arr {
					s, ok := raw.(string)
					if !ok {
						writeError(w, http.StatusBadRequest, "categories must be an array of strings")
						return
					}
					categories = append(categories, s)
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, db.FormatTags(categories))
			case "published_date":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "published_date must be an RFC3339 string")
					return
				}
				t := db.ParsePublishedAt(s)
				if t == nil {
					writeError(w, http.StatusBadRequest, "published_date has invalid format")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, t.UTC().Format("2006-01-02 15:04:05"))
			case "status":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "status must be a string")
					return
				}
				status := models.ArticleStatus(strings.TrimSpace(s))
				switch status {
				case models.ArticleStatusDraft:
					setCols = append(setCols, column)
					setArgs = append(setArgs, nil)
				case models.ArticleStatusPublished:
					setCols = append(setCols, column)
					setArgs = append(setArgs, time.Now().UTC().Format("2006-01-02 15:04:05"))
				default:
					writeError(w, http.StatusBadRequest, "status must be draft or published")
					return
				}
			case "comment_status":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "comment_status must be a string")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, strings.TrimSpace(s))
			case "slug":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "slug must be a string")
					return
				}
				trimmed := strings.TrimSpace(s)
				if !isValidCanonicalSlug(trimmed) {
					writeError(w, http.StatusBadRequest, "slug must be canonical")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, trimmed)
			default:
				setCols = append(setCols, column)
				setArgs = append(setArgs, v)
			}
		}
		if len(setCols) == 0 && authorIDs == nil {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		targetSlug := slug
		if len(setCols) > 0 {
			result, err := db.Update(r.Context(), conn, "articles", setCols, "`slug` = ?", append(setArgs, slug)...)
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
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			if newSlug, ok := body["slug"].(string); ok && strings.TrimSpace(newSlug) != "" {
				targetSlug = strings.TrimSpace(newSlug)
			}
		}
		if authorIDs != nil {
			if err := db.ReplaceArticleAuthorsBySlug(r.Context(), conn, targetSlug, *authorIDs); err != nil {
				if err == sql.ErrNoRows {
					writeError(w, http.StatusNotFound, "article not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Delete an article
// @Tags articles
// @Param slug path string true "Article slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [delete]
func DeleteArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `articles` SET `archived_at` = NOW() WHERE `slug` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Get homepage data
// @Tags homepage
// @Produce json
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.HomepageResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/homepage [get]
func GetHomepage(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sections := [...]struct {
			slug  string
			key   string
			limit int
		}{
			{slug: "news", key: "news", limit: 13},
			{slug: "opinion", key: "opinion", limit: 5},
			{slug: "sports", key: "sports", limit: 6},
			{slug: "entertainment", key: "entertainment", limit: 8},
			{slug: "comics-puzzles", key: "candp", limit: 6},
			{slug: "columns", key: "columns", limit: 5},
		}
		_, _, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		sectionArticles := models.HomepageResponse{
			DevelopingStories: []models.HomepageDevelopingStory{
				{
					Slug:       "questions-arise-over-academy-of-natural-sciences",
					Link:       "questions-arise-over-academy-of-natural-sciences",
					Title:      "Questions arise over Academy of Natural Sciences",
					Excerpt:    "Administration is tight-lipped as a petition is circulating calling on President Merlo to maintain Drexel's commitment to protecting the academy's funding, staff, and programs.",
					ShowInNews: false,
					Label: []models.HomepageLabel{
						{
							ID:   23671,
							Name: "Academy of Natural Sciences",
							Slug: "academy-of-natural-sciences",
						},
					},
				},
				{
					Slug:       "philly-pretzel-factory-under-construction",
					Link:       "philly-pretzel-factory-under-construction",
					Title:      "Philly Pretzel Factory under construction",
					Excerpt:    "According to Business Services, work is ongoing at the Philly Pretzel Factory in PISB. Opening date is not yet determined.",
					ShowInNews: false,
					Label: []models.HomepageLabel{
						{
							ID:   23374,
							Name: "Campus",
							Slug: "campus",
						},
					},
				},
			},
		}

		for _, section := range sections {
			if err := func(section struct {
				slug  string
				key   string
				limit int
			}) error {
				params := ArticleParams{"", section.slug, ""}
				limit := section.limit
				rows, err := queryArticles(r, conn, params, limit+1, offset)
				if err != nil {
					return err
				}
				defer rows.Close()

				articles, err := db.CollectArticles(rows)
				if err != nil {
					return err
				}
				if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
					return err
				}
				hasMore := len(articles) > limit
				if hasMore {
					articles = articles[:limit]
				}
				switch section.key {
				case "news":
					sectionArticles.News = articleListItems(articles, excerptWords)
				case "opinion":
					sectionArticles.Opinion = articleListItems(articles, excerptWords)
				case "sports":
					sectionArticles.Sports = articleListItems(articles, excerptWords)
				case "entertainment":
					sectionArticles.Entertainment = articleListItems(articles, excerptWords)
				case "candp":
					sectionArticles.CAndP = articleListItems(articles, excerptWords)
				case "columns":
					sectionArticles.Columns = articleListItems(articles, excerptWords)
				}
				return nil
			}(section); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, sectionArticles)
	}
}
