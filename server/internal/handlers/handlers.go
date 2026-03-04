package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	db "database"
)

type Author struct {
	ID          int    `json:"id"`
	DisplayName string `json:"display_name"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Login       string `json:"login"`
}

type Article struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Text          string `json:"text"`
	Tags          string `json:"tags"`
	PubDate       string `json:"pub_date"`
	ModDate       string `json:"mod_date"`
	Priority      bool   `json:"priority"`
	BreakingNews  bool   `json:"breaking_news"`
	CommentStatus string `json:"comment_status"`
	PhotoURL      string `json:"photo_url"`
}

var validAuthorSortBy = map[string]bool{
	"display_name": true,
	"last_name":    true,
}

var validArticleSortBy = map[string]bool{
	"pub_date": true,
	"mod_date": true,
	"title":    true,
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

func buildOrderLimit(query, sortBy, sortDir string, validBy map[string]bool, limit, offset int) string {
	if sortBy != "" && validBy[sortBy] {
		dir := "ASC"
		if sortDir == "desc" {
			dir = "DESC"
		}
		query += " ORDER BY `" + sortBy + "` " + dir
	}
	if limit > 0 {
		query += " LIMIT " + strconv.Itoa(limit)
	}
	if offset > 0 {
		query += " OFFSET " + strconv.Itoa(offset)
	}
	return query
}

var authorCols = []string{"id", "display_name", "first_name", "last_name", "email", "login"}

func scanAuthor(rows *sql.Rows) (Author, error) {
	var a Author
	err := rows.Scan(&a.ID, &a.DisplayName, &a.FirstName, &a.LastName, &a.Email, &a.Login)
	return a, err
}

var articleCols = []string{
    "id", "title", "description", "text", "tags",
    "pub_date", "mod_date", "priority", "breaking_news",
    "comment_status", "photo_url",
}

func scanArticle(rows *sql.Rows) (Article, error) {
	var a Article
    err := rows.Scan(
        &a.ID, &a.Title, &a.Description, &a.Text, &a.Tags,
        &a.PubDate, &a.ModDate, &a.Priority, &a.BreakingNews,
        &a.CommentStatus, &a.PhotoURL,
    )
	return a, err
}

// GET /v1/authors
func ListAuthors(conn *sql.DB) http.HandlerFunc {
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

		query := "SELECT `id`, `display_name`, `first_name`, `last_name`, `email`, `login` FROM `authors`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query = buildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), validAuthorSortBy, limit, offset)

		rows, err := conn.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		var authors []Author
		for rows.Next() {
			a, err := scanAuthor(rows)
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
func CreateAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body Author
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		_, err := db.Insert(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			body.DisplayName, body.FirstName, body.LastName, body.Email, body.Login,
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
		rows, err := db.Select(r.Context(), conn, "authors", authorCols, "`id` = ?", id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		a, err := scanAuthor(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// PUT /v1/authors/{id}
func ReplaceAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body Author
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		_, err := db.Update(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			"`id` = ?",
			body.DisplayName, body.FirstName, body.LastName, body.Email, body.Login, id,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /v1/authors/{id}
func UpdateAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var setCols []string
		var setArgs []any
		for _, col := range []string{"display_name", "first_name", "last_name", "email", "login"} {
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
func ListAuthorArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rows, err := queryArticles(r, conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := collectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, articles)
	}
}

// ---- Article Handlers ------------------------------------------------------

// GET /v1/articles
func ListArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorID := r.URL.Query().Get("author_id")
		rows, err := queryArticles(r, conn, authorID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := collectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, articles)
	}
}

// queryArticles is shared by ListArticles and ListAuthorArticles.
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
    query = buildOrderLimit(query, q.Get("sort_by"), q.Get("sort_direction"), validArticleSortBy, limit, offset)

    return conn.QueryContext(r.Context(), query, args...)
}

func collectArticles(rows *sql.Rows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GET /v1/articles/{id}
func GetArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rows, err := db.Select(r.Context(), conn, "articles", articleCols, "`id` = ?", id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		a, err := scanArticle(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// POST /v1/articles
func CreateArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
        _, err := db.Insert(r.Context(), conn, "articles",
            []string{"title", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
            body.Title, body.Description, body.Text, body.Tags, body.PubDate, body.ModDate, body.Priority, body.BreakingNews, body.CommentStatus, body.PhotoURL,
        )
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

// PUT /v1/articles/{id}
func ReplaceArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
        _, err := db.Update(r.Context(), conn, "articles",
            []string{"title", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"},
            "`id` = ?",
            body.Title, body.Description, body.Text, body.Tags, body.PubDate, body.ModDate, body.Priority, body.BreakingNews, body.CommentStatus, body.PhotoURL, id,
        )
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /v1/articles/{id}
func UpdateArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var setCols []string
		var setArgs []any
		for _, col := range []string{"title", "description", "text", "tags", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url"} {
			if v, ok := body[col]; ok {
				setCols = append(setCols, col)
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

// ---- Router ----------------------------------------------------------------

func NewRouter(conn *sql.DB) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/authors",               ListAuthors(conn))
	mux.HandleFunc("POST /v1/authors",              CreateAuthor(conn))
	mux.HandleFunc("GET /v1/authors/{id}",          GetAuthor(conn))
	mux.HandleFunc("PUT /v1/authors/{id}",          ReplaceAuthor(conn))
	mux.HandleFunc("PATCH /v1/authors/{id}",        UpdateAuthor(conn))
	mux.HandleFunc("DELETE /v1/authors/{id}",       DeleteAuthor(conn))
	mux.HandleFunc("GET /v1/authors/{id}/articles", ListAuthorArticles(conn))

	mux.HandleFunc("GET /v1/articles",              ListArticles(conn))
	mux.HandleFunc("GET /v1/articles/{id}",         GetArticle(conn))
	mux.HandleFunc("POST /v1/articles",             CreateArticle(conn))
	mux.HandleFunc("PUT /v1/articles/{id}",         ReplaceArticle(conn))
	mux.HandleFunc("PATCH /v1/articles/{id}",       UpdateArticle(conn))
	mux.HandleFunc("DELETE /v1/articles/{id}",      DeleteArticle(conn))

	return mux
}