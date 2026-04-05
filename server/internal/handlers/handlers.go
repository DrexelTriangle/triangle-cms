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
	"server/internal/models"
)

func Users(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	resp := struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	}{
		Status:  "OK",
		Message: "Users endpoint hit",
		Code:    http.StatusOK,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
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
	writeJSON(w, status, map[string]string{"error": msg})
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

func articleListItems(articles []models.Article, excerptWords int) []map[string]any {
	items := make([]map[string]any, 0, len(articles))
	for _, article := range articles {
		categories := make([]map[string]string, 0, len(article.Categories))
		for _, category := range article.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, map[string]string{
				"name": name,
				"slug": db.CanonicalizeSlug(name),
			})
		}

		authors := make([]map[string]any, 0, len(article.Authors))
		for _, author := range article.Authors {
			authors = append(authors, map[string]any{
				"id":   author.ID,
				"name": author.DisplayName,
				"slug": author.Slug,
			})
		}

		item := map[string]any{
			"title":          article.Title,
			"id":             article.ID,
			"authors":        authors,
			"categories":     categories,
			"excerpt":        truncateWords(article.Excerpt, excerptWords),
			"slug":           article.Slug,
			"status":         article.Status,
			"comment_status": article.CommentStatus,
			"featured_image": article.PhotoURL,
			"is_featured":    article.IsFeatured,
		}
		if article.PublishedAt != nil {
			item["published_date"] = article.PublishedAt
		}
		items = append(items, item)
	}
	return items
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

// GET /v1/authors
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

		query := "SELECT `id`, `display_name`, `login` FROM `authors`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		if sortBy == "" {
			query += " ORDER BY `id` DESC"
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

// POST /v1/authors
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

// GET /v1/authors/{slug}
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

// PUT /v1/authors/{slug}
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

// PATCH /v1/authors/{slug}
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

// DELETE /v1/authors/{slug}
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

// GET /v1/authors/{slug}/articles
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

		writeJSON(w, http.StatusOK, map[string]any{
			"author": map[string]any{
				"id":           author.ID,
				"slug":         author.Slug,
				"display_name": author.DisplayName,
			},
			"articles": articleListItems(articles, excerptWords),
			"pagination": map[string]any{
				"page":     page,
				"limit":    limit,
				"offset":   offset,
				"hasMore":  hasMore,
				"has_more": hasMore,
			},
		})
	}
}

// ---- Article Handlers ------------------------------------------------------

// GET /v1/articles
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

		writeJSON(w, http.StatusOK, map[string]any{
			"articles": articleListItems(articles, excerptWords),
			"pagination": map[string]any{
				"page":     page,
				"limit":    limit,
				"offset":   offset,
				"hasMore":  hasMore,
				"has_more": hasMore,
			},
		})
	}
}

// GET /v1/sections/{section_slug}/articles
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

		writeJSON(w, http.StatusOK, map[string]any{
			"section": map[string]any{
				"slug":            params.Section,
				"name":            sectionCanonicalTitle,
				"canonical_title": sectionCanonicalTitle,
			},
			"subsections": []any{},
			"articles":    articleListItems(articles, excerptWords),
			"pagination": map[string]any{
				"page":     page,
				"limit":    limit,
				"offset":   offset,
				"hasMore":  hasMore,
				"has_more": hasMore,
			},
		})
	}
}

// GET /v1/subsections/{subsection_slug}/articles
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

		writeJSON(w, http.StatusOK, map[string]any{
			"section": map[string]any{
				"slug":            params.Section,
				"name":            sectionCanonicalTitle,
				"canonical_title": sectionCanonicalTitle,
			},
			"subsection": map[string]any{
				"slug":            params.Subsection,
				"name":            subsectionCanonicalTitle,
				"canonical_title": subsectionCanonicalTitle,
			},
			"articles": articleListItems(articles, excerptWords),
			"pagination": map[string]any{
				"page":     page,
				"limit":    limit,
				"offset":   offset,
				"hasMore":  hasMore,
				"has_more": hasMore,
			},
		})
	}
}

// queryArticles is shared by GetArticles and GetAuthorArticles.
type ArticleParams struct {
	AuthorSlug string
	Section    string
	Subsection string
}

// queryArticles is shared by GetArticles and GetAuthorArticles.
func queryArticles(r *http.Request, conn *sql.DB, params ArticleParams, limit, offset int) (*sql.Rows, error) {
	q := r.URL.Query()
	var conditions []string
	var args []any

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

	if title := strings.TrimSpace(q.Get("title")); title != "" {
		conditions = append(conditions, "`title` LIKE ?")
		args = append(args, "%"+title+"%")
	}

	if pub_date := db.ParsePublishedAt(q.Get("published_date")); pub_date != nil {
		conditions = append(conditions, "`pub_date` >= ?")
		args = append(args, pub_date.UTC().Format("2026-03-23 15:04:05"))
	}

	if creation_date := db.ParsePublishedAt(q.Get("creation_date")); creation_date != nil {
		conditions = append(conditions, "`creation_date` >= ?")
		args = append(args, creation_date.UTC().Format("2026-03-23 15:04:05"))
	}

	if slug := strings.TrimSpace(q.Get("slug")); slug != "" {
		conditions = append(conditions, "`slug` LIKE ?")
		args = append(args, "%"+slug+"%")
	}

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

// GET /v1/search?q={term}
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

		resp := make([]map[string]any, 0, len(articles))
		for _, article := range articles {
			categories := make([]map[string]string, 0, len(article.Categories))
			for _, category := range article.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				categories = append(categories, map[string]string{
					"name": name,
					"slug": db.CanonicalizeSlug(name),
				})
			}

			authors := make([]map[string]any, 0, len(article.Authors))
			for _, author := range article.Authors {
				authors = append(authors, map[string]any{
					"id":   author.ID,
					"name": author.DisplayName,
					"slug": author.Slug,
				})
			}

			item := map[string]any{
				"title":          article.Title,
				"id":             article.ID,
				"authors":        authors,
				"categories":     categories,
				"excerpt":        article.Excerpt,
				"slug":           article.Slug,
				"status":         article.Status,
				"comment_status": article.CommentStatus,
				"featured_image": article.PhotoURL,
				"is_featured":    article.IsFeatured,
			}
			if article.PublishedAt != nil {
				item["published_date"] = article.PublishedAt
			}
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// GET /v1/articles/{slug}
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
		categories := make([]map[string]string, 0, len(a.Categories))
		for _, category := range a.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, map[string]string{
				"name": name,
				"slug": db.CanonicalizeSlug(name),
			})
		}

		seoTags := make([]map[string]string, 0, len(a.Tags))
		for _, tag := range a.Tags {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			seoTags = append(seoTags, map[string]string{
				"name": name,
				"slug": db.CanonicalizeSlug(name),
			})
		}

		authors := make([]map[string]any, 0, len(a.Authors))
		for _, author := range a.Authors {
			authors = append(authors, map[string]any{
				"id":   author.ID,
				"name": author.DisplayName,
				"slug": author.Slug,
			})
		}

		relatedArticles, err := db.GetRelatedArticlesBySlug(r.Context(), conn, a.Slug, 3)
		if err != nil {
			if !db.IsVectorSearchUnavailableError(err) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			relatedArticles = []models.Article{}
		}

		related := make([]map[string]any, 0, len(relatedArticles))
		for _, relatedArticle := range relatedArticles {
			relatedCategories := make([]map[string]string, 0, len(relatedArticle.Categories))
			for _, category := range relatedArticle.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				relatedCategories = append(relatedCategories, map[string]string{
					"name": name,
					"slug": db.CanonicalizeSlug(name),
				})
			}

			relatedAuthors := make([]map[string]any, 0, len(relatedArticle.Authors))
			for _, author := range relatedArticle.Authors {
				relatedAuthors = append(relatedAuthors, map[string]any{
					"id":   author.ID,
					"name": author.DisplayName,
					"slug": author.Slug,
				})
			}

			relatedItem := map[string]any{
				"title":          relatedArticle.Title,
				"id":             relatedArticle.ID,
				"authors":        relatedAuthors,
				"categories":     relatedCategories,
				"excerpt":        relatedArticle.Excerpt,
				"slug":           relatedArticle.Slug,
				"status":         relatedArticle.Status,
				"comment_status": relatedArticle.CommentStatus,
				"featured_image": relatedArticle.PhotoURL,
				"is_featured":    relatedArticle.IsFeatured,
			}
			if relatedArticle.PublishedAt != nil {
				relatedItem["published_date"] = relatedArticle.PublishedAt
			}
			related = append(related, relatedItem)
		}

		resp := map[string]any{
			"id":             a.ID,
			"title":          a.Title,
			"slug":           a.Slug,
			"content":        a.Content,
			"excerpt":        a.Excerpt,
			"categories":     categories,
			"comment_status": a.CommentStatus,
			"is_featured":    a.IsFeatured,
			"status":         a.Status,
			"featured_image": a.PhotoURL,
			"authors":        authors,
			"seo": map[string]any{
				"seo_title":        "",
				"meta_description": "",
				"focus_keyword":    "",
				"canonical_url":    "",
				"tags":             seoTags,
			},
			"related": related,
		}
		if a.PublishedAt != nil {
			resp["published_date"] = a.PublishedAt
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// POST /v1/articles
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

// PUT /v1/articles/{slug}
func PutArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
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

// PATCH /v1/articles/{slug}
func PatchArticle(conn *sql.DB) http.HandlerFunc {
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

// DELETE /v1/articles/{slug}
func DeleteArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		_, err := db.Delete(r.Context(), conn, "articles", "`slug` = ?", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
