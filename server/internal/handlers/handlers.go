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

func resolveArticleIDBySlug(ctx context.Context, conn *sql.DB, slug string) (int64, error) {
	var articleID int64
	err := conn.QueryRowContext(ctx, "SELECT `id` FROM `articles` WHERE `slug` = ?", slug).Scan(&articleID)
	if err != nil {
		return 0, err
	}
	return articleID, nil
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

		query := "SELECT `id`, `display_name` FROM `authors`"
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
		_, err := db.Insert(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email"},
			body.DisplayName, body.FirstName, body.LastName, body.Email,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

// GET /v1/authors/{id}
func GetAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`id` = ?", id)
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

// PUT /v1/authors/{id}
func PutAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body models.AuthorInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		_, err := db.Update(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email"},
			"`id` = ?",
			body.DisplayName, body.FirstName, body.LastName, body.Email, id,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /v1/authors/{id}
func PatchAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
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
		if len(setCols) == 0 {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		_, err := db.Update(r.Context(), conn, "authors", setCols, "`id` = ?", append(setArgs, id)...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DELETE /v1/authors/{id}
func DeleteAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := db.Delete(r.Context(), conn, "authors", "`id` = ?", id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// GET /v1/authors/{id}/articles
func GetAuthorArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorID:   id,
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// ---- Article Handlers ------------------------------------------------------

// GET /v1/articles
func GetArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorID:   r.URL.Query().Get("author_id"),
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// GET /v1/sections/{section_slug}/articles
func GetSectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorID:   r.URL.Query().Get("author_id"),
			Section:    r.PathValue("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// GET /v1/subsections/{subsection_slug}/articles
func GetSubsectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(ArticleParams{
			AuthorID:   r.URL.Query().Get("author_id"),
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.PathValue("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// queryArticles is shared by GetArticles and GetAuthorArticles.
type ArticleParams struct {
	AuthorID   string
	Section    string
	Subsection string
}

// queryArticles is shared by GetArticles and GetAuthorArticles.
func queryArticles(r *http.Request, conn *sql.DB, params ArticleParams) (*sql.Rows, error) {
	q := r.URL.Query()
	limit := intParam(r, "limit", 20)
	offset := intParam(r, "offset", 0)
	var conditions []string
	var args []any

	if params.AuthorID != "" {
		conditions = append(conditions, "`id` IN (SELECT `articles_id` FROM `articles_authors` WHERE `author_id` = ?)")
		args = append(args, params.AuthorID)
	}

	if params.Section != "" {
		conditions = append(conditions, "LOWER(`tags`) LIKE ?")
		args = append(args, "%"+strings.ToLower(params.Section)+"%")
	}

	if params.Subsection != "" {
		conditions = append(conditions, "LOWER(`tags`) LIKE ?")
		args = append(args, "%"+strings.ToLower(params.Subsection)+"%")
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

	query := "SELECT `id`, `title`, `slug`, `description`, `text`, `tags`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url` FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if q.Get("sort_by") == "" {
		query += " ORDER BY `id` DESC"
	}
	query = db.BuildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), db.ArticleSortByColumn, limit, offset)

	return conn.QueryContext(r.Context(), query, args...)
}

// GET /v1/articles/{slug}
func GetArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
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
		writeJSON(w, http.StatusOK, a)
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
		fields := db.ArticleInputToDBFields(body)
		result, err := db.Insert(r.Context(), conn, "articles",
			[]string{"title", "slug", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
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
		var body models.Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		articleID, err := resolveArticleIDBySlug(r.Context(), conn, slug)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if strings.TrimSpace(body.Slug) == "" {
			body.Slug = slug
		}
		fields := db.ArticleToDBFields(body)
		fields = append(fields, slug)
		_, err = db.Update(r.Context(), conn, "articles",
			[]string{"title", "slug", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
			"`slug` = ?",
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.ReplaceArticleAuthors(r.Context(), conn, articleID, authorIDsFromOverviews(body.Authors)); err != nil {
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
			"excerpt":        "description",
			"content":        "text",
			"categories":     "tags",
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
			default:
				setCols = append(setCols, column)
				setArgs = append(setArgs, v)
			}
		}
		if len(setCols) == 0 && authorIDs == nil {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		var articleID int64
		if authorIDs != nil {
			resolvedID, err := resolveArticleIDBySlug(r.Context(), conn, slug)
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			articleID = resolvedID
		}
		if len(setCols) > 0 {
			_, err := db.Update(r.Context(), conn, "articles", setCols, "`slug` = ?", append(setArgs, slug)...)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if authorIDs != nil {
			if err := db.ReplaceArticleAuthors(r.Context(), conn, articleID, *authorIDs); err != nil {
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
		_, err := db.Delete(r.Context(), conn, "articles", "`slug` = ?", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
