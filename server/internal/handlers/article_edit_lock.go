package handlers

import (
	"database/sql"
	"net/http"
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		// The lease is keyed by row, so two editors on the same article take the
		// same key whether they arrived on the id-qualified route or on a legacy
		// /articles/:slug link. Keying by whatever the URL happened to carry
		// would hand both of them the lock.
		target, err = resolveArticleTarget(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		lease, granted := articleEditLeases.Acquire(target.lockKey(), user.ID, user.Name)
		status := http.StatusOK
		if !granted {
			status = http.StatusConflict
		}
		writeJSON(w, status, articleEditLockResponse{
			Slug:       target.slug,
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		user, ok := middleware.UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		// Resolved the same way AcquireArticleEditLock resolves it, or the
		// release would miss the lease the acquire took.
		target, err = resolveArticleTarget(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		articleEditLeases.Release(target.lockKey(), user.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}
