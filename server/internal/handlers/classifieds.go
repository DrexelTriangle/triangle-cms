package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"server/internal/slack"
)

const (
	maxClassifiedMessageLength = 2000
	maxClassifiedFieldLength   = 255
)

// GetClassifieds is the public listing: approved and unexpired only.
//
// @Summary List published classifieds
// @Tags classifieds
// @Produce json
// @Param limit query int false "Max results" default(50)
// @Param offset query int false "Offset"
// @Success 200 {object} models.ClassifiedsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/classifieds [get]
func GetClassifieds(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, limit, offset := listParams(r, 50)
		items, err := db.ListPublicClassifieds(r.Context(), conn, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, models.ClassifiedsResponse{Classifieds: items})
	})
}

// How long the Slack post gets once the reader's response has already been
// written. It is not on the request's critical path, but it must not outlive
// the process's patience either.
const classifiedNotifyTimeout = 10 * time.Second

// PostClassified accepts a submission from the public form. It always lands as
// pending: nothing a reader posts reaches the site without a moderator, whether
// that moderator clicks in the CMS or in Slack.
//
// notifier may be nil, which means Slack is not configured: the submission is
// stored and waits in the CMS queue instead.
//
// @Summary Submit a classified
// @Tags classifieds
// @Accept json
// @Produce json
// @Param body body models.ClassifiedSubmitRequest true "Classified submission"
// @Success 201 {object} models.Classified
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/classifieds [post]
func PostClassified(conn *sql.DB, notifier slack.Notifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.ClassifiedSubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		body.Name = strings.TrimSpace(body.Name)
		body.Email = strings.TrimSpace(body.Email)
		body.Label = strings.TrimSpace(body.Label)
		body.Message = strings.TrimSpace(body.Message)
		body.EndDate = strings.TrimSpace(body.EndDate)

		switch {
		case body.Name == "":
			writeError(w, http.StatusBadRequest, "name is required")
			return
		case body.Email == "" || !strings.Contains(body.Email, "@"):
			writeError(w, http.StatusBadRequest, "a valid email is required")
			return
		case body.Message == "":
			writeError(w, http.StatusBadRequest, "message is required")
			return
		case len(body.Message) > maxClassifiedMessageLength:
			writeError(w, http.StatusBadRequest, "message is too long")
			return
		case len(body.Name) > maxClassifiedFieldLength || len(body.Email) > maxClassifiedFieldLength:
			writeError(w, http.StatusBadRequest, "name or email is too long")
			return
		case len(body.Label) > 64:
			writeError(w, http.StatusBadRequest, "category is too long")
			return
		}

		created, err := db.InsertClassified(r.Context(), conn, body, clientIP(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		notifyClassifiedSubmitted(notifier, created)
		writeJSON(w, http.StatusCreated, created)
	})
}

// notifyClassifiedSubmitted posts the submission to Slack in the background.
//
// It deliberately cannot fail the request. The row is already in the CMS queue,
// so a dead webhook costs a notification, not a reader's submission — and the
// reader must not be shown a 500 for a moderation channel they have no idea
// exists. The context is detached from the request because the response is
// written immediately after this returns, which would otherwise cancel the post
// mid-flight.
func notifyClassifiedSubmitted(notifier slack.Notifier, c models.Classified) {
	if notifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), classifiedNotifyTimeout)
		defer cancel()

		if err := notifier.NotifyClassified(ctx, slack.Classified{
			ID:      c.ID,
			Name:    c.Name,
			Email:   c.Email,
			Label:   c.Label,
			Message: c.Message,
			EndDate: c.EndDate,
		}); err != nil {
			slog.Error("could not post classified to Slack; it is still in the CMS moderation queue",
				"classified_id", c.ID, "error", err)
		}
	}()
}

// GetClassifiedsManage is the moderation queue's listing: every status, with
// per-status counts for the filter tabs.
//
// @Summary List classifieds for moderation
// @Tags classifieds
// @Produce json
// @Param status query string false "Filter by status" Enums(pending, approved, rejected)
// @Param limit query int false "Max results" default(25)
// @Param offset query int false "Offset"
// @Success 200 {object} models.ClassifiedsManageResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/classifieds/manage [get]
func GetClassifiedsManage(conn *sql.DB, notifier slack.Notifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		if status != "" && status != "all" && !db.ValidClassifiedStatuses[status] {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		if status == "all" {
			status = ""
		}

		page, limit, offset := listParams(r, 25)
		items, totalCount, err := db.ListClassifieds(r.Context(), conn, status, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		counts, err := db.CountClassifiedsByStatus(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, models.ClassifiedsManageResponse{
			Classifieds:     items,
			Pagination:      paginationResponse(page, limit, offset, offset+len(items) < totalCount, totalCount),
			Counts:          counts,
			SlackConfigured: SlackModerationConfigured(notifier),
		})
	})
}

// PatchClassified moves a classified between statuses from the CMS queue.
//
// @Summary Set a classified's status
// @Tags classifieds
// @Accept json
// @Produce json
// @Param id path int true "Classified ID"
// @Param body body models.ClassifiedStatusPatchRequest true "New status"
// @Success 200 {object} models.Classified
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/classifieds/{id} [patch]
func PatchClassified(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}

		var body models.ClassifiedStatusPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		status := strings.ToLower(strings.TrimSpace(body.Status))
		if !db.ValidClassifiedStatuses[status] {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}

		if _, err := db.GetClassified(r.Context(), conn, id); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "classified not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		decidedBy := "unknown"
		if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
			decidedBy = user.Email
		}

		if err := db.SetClassifiedStatus(r.Context(), conn, id, status, decidedBy, "cms"); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		updated, err := db.GetClassified(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "classified_moderated", "Classified "+strconv.FormatInt(id, 10)+" "+status, "status", status)
		writeJSON(w, http.StatusOK, updated)
	})
}

// @Summary Delete a classified
// @Tags classifieds
// @Produce json
// @Param id path int true "Classified ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/classifieds/{id} [delete]
func DeleteClassified(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}

		deleted, err := db.DeleteClassified(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "classified not found")
			return
		}
		activity.LogRequest(r, "classified_deleted", "Classified "+strconv.FormatInt(id, 10)+" deleted")
		w.WriteHeader(http.StatusNoContent)
	})
}

func classifiedIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
