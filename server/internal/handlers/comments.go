package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"server/internal/activity"
	"server/internal/akismet"
	db "server/internal/database"
	"server/internal/models"
	"strconv"
	"strings"
	"time"
)

// @Summary List approved comments for an article
// @Tags articles
// @Produce json
// @Param slug path string true "Article slug"
// @Success 200 {object} models.ArticleCommentsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug}/comments [get]
func GetArticleComments(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		exists, err := db.ArticleExistsBySlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}

		comments, err := db.GetApprovedCommentsByArticleSlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.CommentResponse, 0, len(comments))
		for _, comment := range comments {
			resp = append(resp, models.CommentResponse{
				ID:           comment.ID,
				ArticleID:    comment.ArticleID,
				ParentID:     comment.ParentID,
				AuthorName:   comment.AuthorName,
				AuthorURL:    comment.AuthorURL,
				Content:      comment.Content,
				CreatedAt:    comment.CreatedAt,
				CreatedAtGMT: comment.CreatedAtGMT,
				Status:       comment.Status,
				Type:         comment.Type,
			})
		}

		// No auth middleware on this route: the thread is the approved comments
		// and nothing else, whoever is asking.
		setAlwaysPublicCache(w)
		writeJSON(w, http.StatusOK, models.ArticleCommentsResponse{
			ArticleSlug: slug,
			Comments:    resp,
			TotalCount:  len(resp),
		})
	}
}

// @Summary Submit a comment for an article
// @Tags articles
// @Accept json
// @Produce json
// @Param slug path string true "Article slug"
// @Param body body models.CommentInput true "Comment"
// @Success 201 {object} models.CommentResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug}/comments [post]
func PostArticleComment(conn *sql.DB, spamChecker akismet.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
		var body models.CommentInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		body.AuthorName = strings.TrimSpace(body.AuthorName)
		body.AuthorEmail = strings.TrimSpace(body.AuthorEmail)
		body.AuthorURL = strings.TrimSpace(body.AuthorURL)
		body.Content = strings.TrimSpace(body.Content)

		if body.AuthorName == "" {
			writeError(w, http.StatusBadRequest, "author_name is required")
			return
		}
		if len([]rune(body.AuthorName)) > 80 {
			writeError(w, http.StatusBadRequest, "author_name must be 80 characters or fewer")
			return
		}
		if body.Content == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}
		if len([]rune(body.Content)) > 5000 {
			writeError(w, http.StatusBadRequest, "content must be 5000 characters or fewer")
			return
		}
		if body.AuthorEmail != "" && (len(body.AuthorEmail) > 254 || !strings.Contains(body.AuthorEmail, "@")) {
			writeError(w, http.StatusBadRequest, "author_email must be a valid email address")
			return
		}
		if body.AuthorURL != "" {
			parsedURL, err := url.ParseRequestURI(body.AuthorURL)
			if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
				writeError(w, http.StatusBadRequest, "author_url must be an http or https URL")
				return
			}
		}
		if body.ParentID < 0 {
			writeError(w, http.StatusBadRequest, "parent_id must be positive")
			return
		}

		articleID, commentStatus, exists, err := db.GetArticleCommentTargetBySlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if articleCommentsClosed(commentStatus) {
			writeError(w, http.StatusForbidden, "comments are closed for this article")
			return
		}

		parentCanAcceptReply, err := db.CommentCanAcceptReply(r.Context(), conn, articleID, body.ParentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !parentCanAcceptReply {
			writeError(w, http.StatusBadRequest, "parent comment not found")
			return
		}

		now := time.Now().UTC()
		status := "pending"
		if spamChecker != nil {
			status = "approved"
			isSpam, err := spamChecker.CheckComment(r.Context(), akismet.Comment{
				UserIP:      clientIP(r),
				UserAgent:   r.UserAgent(),
				Referrer:    r.Referer(),
				Permalink:   articlePermalink(r, slug),
				Type:        "comment",
				Author:      body.AuthorName,
				AuthorEmail: body.AuthorEmail,
				AuthorURL:   body.AuthorURL,
				Content:     body.Content,
				CreatedAt:   now,
			})
			if err != nil {
				status = "pending"
				if akismet.IsConfigError(err) {
					// Nothing gets filtered until an operator fixes the key or
					// blog URL, so this is a fault, not a hiccup.
					slog.Error("akismet is misconfigured; spam filtering is not running", "article_slug", slug, "error", err)
				} else {
					slog.Warn("akismet comment check failed; comment requires moderation", "article_slug", slug, "error", err)
				}
			} else if isSpam {
				status = "spam"
			}
		}

		comment, err := db.CreateComment(r.Context(), conn, db.CreateCommentParams{
			ArticleID:   articleID,
			ParentID:    body.ParentID,
			AuthorName:  body.AuthorName,
			AuthorEmail: body.AuthorEmail,
			AuthorURL:   body.AuthorURL,
			AuthorIP:    clientIP(r),
			Content:     body.Content,
			Status:      status,
			Type:        "comment",
			CreatedAt:   now,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, models.CommentResponse{
			ID:           comment.ID,
			ArticleID:    comment.ArticleID,
			ParentID:     comment.ParentID,
			AuthorName:   comment.AuthorName,
			AuthorURL:    comment.AuthorURL,
			Content:      comment.Content,
			CreatedAt:    comment.CreatedAt,
			CreatedAtGMT: comment.CreatedAtGMT,
			Status:       comment.Status,
			Type:         comment.Type,
		})
	}
}

func articleCommentsClosed(commentStatus string) bool {
	return strings.EqualFold(strings.TrimSpace(commentStatus), "closed")
}

func articlePermalink(r *http.Request, slug string) string {
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
		if scheme == "" {
			scheme = "https"
		}
		return scheme + "://" + forwardedHost + "/article/" + slug
	}

	if r.Host != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + r.Host + "/article/" + slug
	}

	return "/article/" + slug
}

func adminCommentResponse(comment db.AdminComment) models.AdminCommentResponse {
	return models.AdminCommentResponse{
		ID:           comment.ID,
		ArticleID:    comment.ArticleID,
		ArticleTitle: comment.ArticleTitle,
		ArticleSlug:  comment.ArticleSlug,
		ParentID:     comment.ParentID,
		AuthorName:   comment.AuthorName,
		AuthorEmail:  comment.AuthorEmail,
		AuthorURL:    comment.AuthorURL,
		Content:      comment.Content,
		CreatedAt:    comment.CreatedAt,
		CreatedAtGMT: comment.CreatedAtGMT,
		Status:       comment.Status,
		Type:         comment.Type,
	}
}

// @Summary List comments for CMS moderation
// @Tags comments
// @Produce json
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(25)
// @Param offset query int false "Offset"
// @Param status query string false "Filter by status" Enums(all,pending,approved,spam,trash)
// @Param search query string false "Search comment content, author, email, or article"
// @Success 200 {object} models.AdminCommentsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments [get]
func GetComments(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := listParams(r, 25)
		status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		if status != "" && status != "all" && !db.ValidCommentStatuses[status] {
			writeError(w, http.StatusBadRequest, "status must be one of: all, pending, approved, spam, trash")
			return
		}

		comments, totalCount, counts, err := db.ListAdminComments(
			r.Context(),
			conn,
			limit,
			offset,
			status,
			r.URL.Query().Get("search"),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.AdminCommentResponse, 0, len(comments))
		for _, comment := range comments {
			resp = append(resp, adminCommentResponse(comment))
		}

		writeJSON(w, http.StatusOK, models.AdminCommentsResponse{
			Comments:   resp,
			Pagination: paginationResponse(page, limit, offset, offset+len(comments) < totalCount, totalCount),
			Counts:     counts,
		})
	}
}

// @Summary Update a comment status
// @Tags comments
// @Accept json
// @Produce json
// @Param id path int true "Comment ID"
// @Param body body models.CommentStatusPatchRequest true "Status update"
// @Success 200 {object} models.AdminCommentResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments/{id} [patch]
func PatchComment(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		var body models.CommentStatusPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		status := strings.ToLower(strings.TrimSpace(body.Status))
		if !db.ValidCommentStatuses[status] {
			writeError(w, http.StatusBadRequest, "status must be one of: pending, approved, spam, trash")
			return
		}

		comment, err := db.UpdateCommentStatus(r.Context(), conn, id, status)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		activity.LogRequest(r, "comment_status_updated", fmt.Sprintf("Comment #%d", id), "status", status)
		writeJSON(w, http.StatusOK, adminCommentResponse(comment))
	}
}

// @Summary Delete a comment
// @Tags comments
// @Param id path int true "Comment ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments/{id} [delete]
func DeleteComment(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		if err := db.DeleteComment(r.Context(), conn, id); err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		activity.LogRequest(r, "comment_deleted", fmt.Sprintf("Comment #%d", id))
		w.WriteHeader(http.StatusNoContent)
	}
}
