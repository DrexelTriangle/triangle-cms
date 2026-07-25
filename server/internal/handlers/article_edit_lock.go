package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"server/internal/middleware"
)

// articleEditLockResponse describes who currently holds the advisory editing
// lease for an article.
type articleEditLockResponse struct {
	Slug       string `json:"slug"`
	HeldBySelf bool   `json:"held_by_self"`
	HolderID   int64  `json:"holder_id"`
	HolderName string `json:"holder_name"`
	ExpiresAt  string `json:"expires_at"`
}

// AcquireArticleEditLock grants or refreshes the caller's editing lease for an
// article. The editor calls this when it opens (and periodically after) so a
// second person is told up front that someone else is already editing, instead
// of doing work they can't save. It returns 200 when the caller holds the lease
// and 409 (with the current holder) when someone else does.
//
// @Summary Acquire or refresh the editing lock for an article
// @Tags articles
// @Produce json
// @Param slug path string true "Article slug"
// @Success 200 {object} articleEditLockResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} articleEditLockResponse
// @Security BearerAuth
// @Router /v1/articles/{slug}/edit-lock [put]
func AcquireArticleEditLock(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		lease, granted := articleEditLeases.Acquire(slug, user.ID, user.Name)
		status := http.StatusOK
		if !granted {
			status = http.StatusConflict
		}
		writeJSON(w, status, articleEditLockResponse{
			Slug:       slug,
			HeldBySelf: granted,
			HolderID:   lease.HolderID,
			HolderName: lease.HolderName,
			ExpiresAt:  lease.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
}

// ReleaseArticleEditLock drops the caller's editing lease for an article, called
// when the editor is closed. It is idempotent and only releases a lease the
// caller actually holds.
//
// @Summary Release the editing lock for an article
// @Tags articles
// @Param slug path string true "Article slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug}/edit-lock [delete]
func ReleaseArticleEditLock(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		articleEditLeases.Release(slug, user.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}
