package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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

// GET /v1/authors
func GetAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)
		articleID := intParam(r, "article_id", 0)

		var conditions []string
		var args []any

		if articleID > 0 {
			conditions = append(conditions, "`id` IN (SELECT `author_id` FROM `articles_authors` WHERE `articles_id` = ?)")
			args = append(args, articleID)
		}

		query := "SELECT `id`, `display_name`, `first_name`, `last_name`, `email` FROM `authors`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		if q.Get("sort_by") == "" {
			query += " ORDER BY `id` DESC"
		}
		query = db.BuildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), db.AuthorSortByColumn, limit, offset)

		rows, err := conn.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var authors []models.Author
		for rows.Next() {
			a, err := db.ScanAuthor(rows)
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
		rows, err := queryArticles(r, conn, id)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// ---- Article Handlers ------------------------------------------------------

// GET /v1/articles
func GetArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorID := r.URL.Query().Get("author_id")
		rows, err := queryArticles(r, conn, authorID)
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
		writeJSON(w, http.StatusOK, articles)
	}
}

// queryArticles is shared by GetArticles and GetAuthorArticles.
func queryArticles(r *http.Request, conn *sql.DB, authorID string) (*sql.Rows, error) {
	q := r.URL.Query()
	limit := intParam(r, "limit", 20)
	offset := intParam(r, "offset", 0)
	var conditions []string
	var args []any

	if authorID != "" {
		conditions = append(conditions, "`id` IN (SELECT `articles_id` FROM `articles_authors` WHERE `author_id` = ?)")
		args = append(args, authorID)
	}

	query := "SELECT `id`, `title`, `description`, `text`, `tags`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url` FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query = db.BuildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), db.ArticleSortByColumn, limit, offset)

	return conn.QueryContext(r.Context(), query, args...)
}

// GET /v1/articles/{id}
func GetArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rows, err := db.Select(r.Context(), conn, "articles", db.ArticleColumns, "`id` = ?", id)
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
		_, err := db.Insert(r.Context(), conn, "articles",
			[]string{"title", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

// PUT /v1/articles/{id}
func PutArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body models.Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		fields := db.ArticleToDBFields(body)
		fields = append(fields, id)
		_, err := db.Update(r.Context(), conn, "articles",
			[]string{"title", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
			"`id` = ?",
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /v1/articles/{id}
func PatchArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var setCols []string
		var setArgs []any
		columnByJSONField := map[string]string{
			"title":        "title",
			"excerpt":      "description",
			"content":      "text",
			"categories":   "tags",
			"published_at": "pub_date",
			"is_featured":  "priority",
			"status":       "comment_status",
			"photo_url":    "photo_url",
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
			case "published_at":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "published_at must be an RFC3339 string")
					return
				}
				t := db.ParsePublishedAt(s)
				if t == nil {
					writeError(w, http.StatusBadRequest, "published_at has invalid format")
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
				setCols = append(setCols, column)
				setArgs = append(setArgs, strings.TrimSpace(s))
			default:
				setCols = append(setCols, column)
				setArgs = append(setArgs, v)
			}
		}
		if len(setCols) == 0 {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		_, err := db.Update(r.Context(), conn, "articles", setCols, "`id` = ?", append(setArgs, id)...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// DELETE /v1/articles/{id}
func DeleteArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := db.Delete(r.Context(), conn, "articles", "`id` = ?", id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
