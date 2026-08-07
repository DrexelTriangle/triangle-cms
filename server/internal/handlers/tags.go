package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	db "server/internal/database"
)

// @Summary List the most-used SEO tags
// @Tags articles
// @Produce json
// @Param limit query int false "How many tags to return (default 50, max 200)"
// @Success 200 {array} database.PopularTag
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/tags/popular [get]
//
// Backs the click-to-add suggestions under the article editor's SEO Tags field.
// Authenticated: it is an editing aid, and the counts describe unpublished
// drafts as well as live articles.
func GetPopularTags(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				writeError(w, http.StatusBadRequest, "limit must be a positive integer")
				return
			}
			limit = parsed
		}

		tags, err := db.PopularTags(r.Context(), conn, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch popular tags")
			return
		}
		writeJSON(w, http.StatusOK, tags)
	})
}
