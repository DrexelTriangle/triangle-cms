package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	db "server/internal/database"
)

// @Summary List or search the SEO tags already in use
// @Tags articles
// @Produce json
// @Param q query string false "Search text; omit for the most-used tags"
// @Param limit query int false "How many tags to return (default 50, max 200)"
// @Success 200 {array} database.PopularTag
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/tags [get]
//
// Backs the suggestions under the article editor's SEO Tags field: the
// most-used tags when the box is empty, and a search over every tag the archive
// has once the editor starts typing. Retyping a tag from memory is how
// near-duplicates get coined, so finding the existing one has to be easier.
//
// Authenticated: it is an editing aid, and the counts describe unpublished
// drafts as well as live articles.
func GetTags(conn *sql.DB) http.Handler {
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

		// A blank q is not an error, it is the unfiltered list: the editor
		// sends the box's contents, and an empty box means "what is popular".
		tags, err := db.SearchTags(r.Context(), conn, r.URL.Query().Get("q"), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch tags")
			return
		}
		writeJSON(w, http.StatusOK, tags)
	})
}
