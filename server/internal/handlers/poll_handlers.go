package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

// Poll handlers come in two layers.
//
// /v1/poll/* is the original single-poll API. The public site is built against
// it and its response shapes are frozen; it now reads and writes whichever poll
// is currently active rather than a global bag of counts.
//
// /v1/polls/* is the poll archive: real poll records with questions, run dates
// and preserved results.

const maxPollQuestionLen = 255

// ---------------------------------------------------------------------------
// Legacy single-poll API (public site depends on these response shapes)
// ---------------------------------------------------------------------------

// @Summary Get poll counts
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollCountsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll [get]
func GetPoll(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poll, err := db.GetActivePoll(r.Context(), conn)
		// No active poll is a normal state (between polls), not an error. Return
		// empty counts so the public site renders an empty widget instead of 500.
		if errors.Is(err, db.ErrNoActivePoll) {
			writeJSON(w, http.StatusOK, models.PollCountsResponse{Counts: map[string]int64{}})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll counts")
			return
		}
		writeJSON(w, http.StatusOK, models.PollCountsResponse{Counts: countsOf(poll)})
	})
}

// @Summary Submit poll vote
// @Tags poll
// @Accept json
// @Accept application/x-www-form-urlencoded
// @Produce json
// @Param body body models.PollOptionRequest false "Poll vote payload"
// @Param poll formData string false "Poll vote option (form payload)"
// @Success 200 {object} models.PollCountsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll [post]
func PostPoll(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		option, err := parseSubmittedPollOption(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid poll option")
			return
		}
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "Invalid poll option")
			return
		}

		poll, err := db.VoteOnActivePoll(r.Context(), conn, option)
		switch {
		case errors.Is(err, db.ErrNoActivePoll):
			writeError(w, http.StatusConflict, "No poll is currently running")
			return
		case errors.Is(err, db.ErrPollNotOpen):
			writeError(w, http.StatusConflict, "This poll is closed")
			return
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusBadRequest, "Invalid poll option")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to update poll")
			return
		}

		writeJSON(w, http.StatusOK, models.PollCountsResponse{Counts: countsOf(poll)})
	})
}

// @Summary Get poll options
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollOptionsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll/options [get]
func GetPollOptions(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poll, err := db.GetActivePoll(r.Context(), conn)
		if errors.Is(err, db.ErrNoActivePoll) {
			writeJSON(w, http.StatusOK, models.PollOptionsResponse{Options: []string{}})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll options")
			return
		}
		options := make([]string, 0, len(poll.Options))
		for _, opt := range poll.Options {
			options = append(options, opt.Name)
		}
		writeJSON(w, http.StatusOK, models.PollOptionsResponse{Options: options})
	})
}

// @Summary Get poll title
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollTitleResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll/title [get]
func GetPollTitle(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poll, err := db.GetActivePoll(r.Context(), conn)
		if errors.Is(err, db.ErrNoActivePoll) {
			writeJSON(w, http.StatusOK, models.PollTitleResponse{Title: ""})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll title")
			return
		}
		writeJSON(w, http.StatusOK, models.PollTitleResponse{Title: poll.Question})
	})
}

// @Summary Update poll title
// @Tags poll
// @Accept json
// @Produce json
// @Param body body models.PollTitleRequest true "Poll title update"
// @Success 200 {object} models.PollTitleResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/title [patch]
func PatchPollTitle(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollTitleRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		title := strings.TrimSpace(body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "poll title is required")
			return
		}
		if len(title) > maxPollQuestionLen {
			writeError(w, http.StatusBadRequest, "poll title too long")
			return
		}

		poll, err := db.GetActivePoll(r.Context(), conn)
		if errors.Is(err, db.ErrNoActivePoll) {
			writeError(w, http.StatusNotFound, "no active poll to rename")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update poll title")
			return
		}
		if err := db.UpdatePoll(r.Context(), conn, poll.ID, &title, nil, nil, nil, false, false); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update poll title")
			return
		}

		// Keep the legacy cms_settings row in step so a rollback to the previous
		// binary still shows the current question.
		_ = db.SetPollTitle(r.Context(), conn, title)

		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Poll title: %s", title))
		writeJSON(w, http.StatusOK, models.PollTitleResponse{Title: title})
	})
}

// @Summary Add poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRequest true "Poll option"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [post]
func PostPollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		poll, err := activePollOr404(w, r, conn)
		if poll == nil {
			return
		}
		if _, err = db.AddPollOption(r.Context(), conn, poll.ID, option); err != nil {
			writeError(w, http.StatusConflict, "poll option already exists")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Added poll option: %s", option))
		w.WriteHeader(http.StatusCreated)
	})
}

// @Summary Rename poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRenameRequest true "Poll option rename"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [patch]
func PatchPollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRenameRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		oldOption := strings.TrimSpace(body.OldOption)
		newOption := strings.TrimSpace(body.NewOption)
		if !validPollOption(oldOption) || !validPollOption(newOption) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		poll, _ := activePollOr404(w, r, conn)
		if poll == nil {
			return
		}
		optionID, ok := findOptionID(poll, oldOption)
		if !ok {
			writeError(w, http.StatusNotFound, "poll option not found")
			return
		}
		if err := db.RenamePollOption(r.Context(), conn, poll.ID, optionID, newOption); err != nil {
			writeError(w, http.StatusConflict, "unable to rename poll option")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Renamed poll option: %s → %s", oldOption, newOption))
		w.WriteHeader(http.StatusNoContent)
	})
}

// @Summary Delete poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRequest true "Poll option"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [delete]
func DeletePollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		poll, _ := activePollOr404(w, r, conn)
		if poll == nil {
			return
		}
		optionID, ok := findOptionID(poll, option)
		if !ok {
			writeError(w, http.StatusNotFound, "poll option not found")
			return
		}
		if err := db.DeletePollOption(r.Context(), conn, poll.ID, optionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "poll option not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "unable to delete poll option")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Deleted poll option: %s", option))
		w.WriteHeader(http.StatusNoContent)
	})
}

// ---------------------------------------------------------------------------
// Poll archive
// ---------------------------------------------------------------------------

// @Summary List published polls
// @Description Public poll archive: every poll an editor has published, newest first. Drafts are excluded.
// @Tags poll
// @Produce json
// @Param limit query int false "Max polls to return (default 50, max 200)"
// @Param offset query int false "Polls to skip"
// @Success 200 {object} models.PollListResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/polls [get]
func GetPolls(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pollPageParams(r)
		polls, err := db.ListPolls(r.Context(), conn, false, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch polls")
			return
		}
		writeJSON(w, http.StatusOK, models.PollListResponse{Polls: pollViews(polls, livePollID(r, conn))})
	})
}

// @Summary List all polls including drafts
// @Description Editor-facing listing. Separate from GET /v1/polls so unpublished questions are never reachable without admin auth.
// @Tags poll
// @Produce json
// @Param limit query int false "Max polls to return (default 50, max 200)"
// @Param offset query int false "Polls to skip"
// @Success 200 {object} models.PollListResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/manage [get]
func GetPollsManage(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pollPageParams(r)
		polls, err := db.ListPolls(r.Context(), conn, true, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch polls")
			return
		}
		writeJSON(w, http.StatusOK, models.PollListResponse{Polls: pollViews(polls, livePollID(r, conn))})
	})
}

// @Summary Get a single poll
// @Tags poll
// @Produce json
// @Param id path int true "Poll ID"
// @Success 200 {object} models.PollResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Router /v1/polls/{id} [get]
func GetPollByID(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}
		poll, err := db.GetPollByID(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "poll not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll")
			return
		}
		// Drafts are not public. This endpoint is unauthenticated so that the
		// site can deep-link a published poll; an unpublished one must 404.
		if poll.Status == db.PollStatusDraft {
			writeError(w, http.StatusNotFound, "poll not found")
			return
		}
		writeJSON(w, http.StatusOK, models.PollResponse{Poll: pollView(*poll, livePollID(r, conn))})
	})
}

// @Summary Create a poll
// @Description Creating a poll with status "active" publishes it when its start date arrives; if that date has already passed (or none was given) it goes live at once and closes the poll that was running.
// @Tags poll
// @Accept json
// @Produce json
// @Param body body models.PollRequest true "Poll"
// @Success 201 {object} models.PollResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls [post]
func PostPollRecord(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		question := ""
		if body.Question != nil {
			question = strings.TrimSpace(*body.Question)
		}
		if question == "" {
			writeError(w, http.StatusBadRequest, "poll question is required")
			return
		}
		if len(question) > maxPollQuestionLen {
			writeError(w, http.StatusBadRequest, "poll question too long")
			return
		}

		status := db.PollStatusDraft
		if body.Status != nil {
			status = strings.ToLower(strings.TrimSpace(*body.Status))
		}
		if !db.ValidPollStatuses[status] {
			writeError(w, http.StatusBadRequest, "invalid poll status")
			return
		}

		startsAt, _, err := parsePollTime(body.StartsAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid starts_at")
			return
		}
		endsAt, _, err := parsePollTime(body.EndsAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ends_at")
			return
		}
		if startsAt != nil && endsAt != nil && endsAt.Before(*startsAt) {
			writeError(w, http.StatusBadRequest, "ends_at must be after starts_at")
			return
		}

		for _, opt := range body.Options {
			if !validPollOption(strings.TrimSpace(opt)) {
				writeError(w, http.StatusBadRequest, "invalid poll option")
				return
			}
		}

		id, err := db.CreatePoll(r.Context(), conn, question, status, startsAt, endsAt, body.Options)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create poll")
			return
		}

		poll, err := db.GetPollByID(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load created poll")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Created poll: %s", question))
		writeJSON(w, http.StatusCreated, models.PollResponse{Poll: pollView(*poll, livePollID(r, conn))})
	})
}

// @Summary Update a poll
// @Description Omitted fields are left unchanged; an explicit null on starts_at or ends_at clears that date.
// @Tags poll
// @Accept json
// @Produce json
// @Param id path int true "Poll ID"
// @Param body body models.PollRequest true "Poll fields to change"
// @Success 200 {object} models.PollResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/{id} [patch]
func PatchPollRecord(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}

		var body models.PollRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		var question *string
		if body.Question != nil {
			trimmed := strings.TrimSpace(*body.Question)
			if trimmed == "" {
				writeError(w, http.StatusBadRequest, "poll question is required")
				return
			}
			if len(trimmed) > maxPollQuestionLen {
				writeError(w, http.StatusBadRequest, "poll question too long")
				return
			}
			question = &trimmed
		}

		var status *string
		if body.Status != nil {
			normalized := strings.ToLower(strings.TrimSpace(*body.Status))
			if !db.ValidPollStatuses[normalized] {
				writeError(w, http.StatusBadRequest, "invalid poll status")
				return
			}
			status = &normalized
		}

		startsAt, clearStarts, err := parsePollTime(body.StartsAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid starts_at")
			return
		}
		endsAt, clearEnds, err := parsePollTime(body.EndsAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ends_at")
			return
		}
		// Only checkable when the same request supplies both; a PATCH that moves
		// one date against a stored one is caught by the editor before it is sent.
		if startsAt != nil && endsAt != nil && endsAt.Before(*startsAt) {
			writeError(w, http.StatusBadRequest, "ends_at must be after starts_at")
			return
		}

		if err := db.UpdatePoll(r.Context(), conn, id, question, status, startsAt, endsAt, clearStarts, clearEnds); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "poll not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to update poll")
			return
		}

		poll, err := db.GetPollByID(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load poll")
			return
		}
		// The legacy cms_settings title tracks whatever is on the site. A poll
		// queued behind a start date must not claim it early, or a rollback to
		// the previous binary would show a question readers cannot vote on.
		liveID := livePollID(r, conn)
		if poll.ID == liveID {
			_ = db.SetPollTitle(r.Context(), conn, poll.Question)
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Updated poll #%d: %s", id, poll.Question))
		writeJSON(w, http.StatusOK, models.PollResponse{Poll: pollView(*poll, liveID)})
	})
}

// @Summary Delete a poll
// @Description Permanently removes the poll and its recorded results.
// @Tags poll
// @Param id path int true "Poll ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/{id} [delete]
func DeletePollRecord(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}
		if err := db.DeletePoll(r.Context(), conn, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "poll not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to delete poll")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Deleted poll #%d", id))
		w.WriteHeader(http.StatusNoContent)
	})
}

// @Summary Add an option to a poll
// @Tags poll
// @Accept json
// @Param id path int true "Poll ID"
// @Param body body models.PollOptionNameRequest true "Poll option"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/{id}/options [post]
func PostPollRecordOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}
		var body models.PollOptionNameRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}
		if _, err := db.AddPollOption(r.Context(), conn, id, option); err != nil {
			writeError(w, http.StatusConflict, "poll option already exists")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Added option to poll #%d: %s", id, option))
		w.WriteHeader(http.StatusCreated)
	})
}

// @Summary Rename an option on a poll
// @Tags poll
// @Accept json
// @Param id path int true "Poll ID"
// @Param option_id path int true "Option ID"
// @Param body body models.PollOptionNameRequest true "New option name"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/{id}/options/{option_id} [patch]
func PatchPollRecordOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollID, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}
		optionID, err := pollIDParam(r, "option_id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid option id")
			return
		}
		var body models.PollOptionNameRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}
		if err := db.RenamePollOption(r.Context(), conn, pollID, optionID, option); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "poll option not found")
				return
			}
			writeError(w, http.StatusConflict, "unable to rename poll option")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Renamed option on poll #%d: %s", pollID, option))
		w.WriteHeader(http.StatusNoContent)
	})
}

// @Summary Delete an option from a poll
// @Tags poll
// @Param id path int true "Poll ID"
// @Param option_id path int true "Option ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/polls/{id}/options/{option_id} [delete]
func DeletePollRecordOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollID, err := pollIDParam(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid poll id")
			return
		}
		optionID, err := pollIDParam(r, "option_id")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid option id")
			return
		}
		if err := db.DeletePollOption(r.Context(), conn, pollID, optionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "poll option not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "unable to delete poll option")
			return
		}
		activity.LogRequest(r, "poll_updated", fmt.Sprintf("Deleted option %d from poll #%d", optionID, pollID))
		w.WriteHeader(http.StatusNoContent)
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// livePollID resolves which poll is on the site right now so views can be
// labelled with their derived state. A lookup failure is not worth failing the
// request over: 0 simply means "nothing is live", and every other field still
// renders.
func livePollID(r *http.Request, conn *sql.DB) int64 {
	id, err := db.LivePollID(r.Context(), conn)
	if err != nil {
		return 0
	}
	return id
}

func pollViews(polls []db.Poll, liveID int64) []models.PollView {
	views := make([]models.PollView, 0, len(polls))
	for _, p := range polls {
		views = append(views, pollView(p, liveID))
	}
	return views
}

func pollView(p db.Poll, liveID int64) models.PollView {
	total := p.TotalVotes()
	options := make([]models.PollOptionView, 0, len(p.Options))
	for _, opt := range p.Options {
		var pct float64
		if total > 0 {
			// Rounded to one decimal so clients can print the value directly
			// without each one picking a different precision.
			pct = math.Round(float64(opt.VoteCount)/float64(total)*1000) / 10
		}
		options = append(options, models.PollOptionView{
			ID:         opt.ID,
			Option:     opt.Name,
			Votes:      opt.VoteCount,
			Percentage: pct,
		})
	}

	view := models.PollView{
		ID:         p.ID,
		Question:   p.Question,
		Status:     p.Status,
		State:      p.State(time.Now(), liveID),
		TotalVotes: total,
		Options:    options,
	}
	if p.StartsAt != nil {
		view.StartsAt = p.StartsAt.Format(time.RFC3339)
	}
	if p.EndsAt != nil {
		view.EndsAt = p.EndsAt.Format(time.RFC3339)
	}
	return view
}

func countsOf(p *db.Poll) map[string]int64 {
	counts := make(map[string]int64, len(p.Options))
	for _, opt := range p.Options {
		counts[opt.Name] = opt.VoteCount
	}
	return counts
}

func findOptionID(p *db.Poll, name string) (int64, bool) {
	for _, opt := range p.Options {
		if opt.Name == name {
			return opt.ID, true
		}
	}
	return 0, false
}

// activePollOr404 writes the error response itself and returns nil when there
// is no active poll, so callers can bail with a single nil check.
func activePollOr404(w http.ResponseWriter, r *http.Request, conn *sql.DB) (*db.Poll, error) {
	poll, err := db.GetActivePoll(r.Context(), conn)
	if errors.Is(err, db.ErrNoActivePoll) {
		writeError(w, http.StatusNotFound, "no active poll")
		return nil, err
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load active poll")
		return nil, err
	}
	return poll, nil
}

func pollIDParam(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(r.PathValue(name)), 10, 64)
}

func pollPageParams(r *http.Request) (int, int) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}

	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = parsed
		}
	}
	return limit, offset
}

// parsePollTime maps a JSON field to (value, clear, error).
//
// Three states have to be distinguished: field absent (nil, false) means leave
// unchanged; explicit null or "" (nil, true) means clear the column; a
// timestamp (value, false) means set it. Collapsing null and absent would make
// every PATCH silently wipe the dates it didn't mention.
func parsePollTime(raw *string) (*time.Time, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, true, nil
	}

	// datetime-local inputs submit "2006-01-02T15:04" with no zone, which is
	// what the CMS poll form sends.
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return &parsed, false, nil
		}
	}
	return nil, false, fmt.Errorf("unrecognised timestamp: %q", trimmed)
}

func parseSubmittedPollOption(r *http.Request) (string, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	switch {
	case strings.Contains(contentType, "application/json"):
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", err
		}
		return strings.TrimSpace(body.Option), nil
	default:
		if err := r.ParseForm(); err != nil {
			return "", err
		}
		return strings.TrimSpace(r.FormValue("poll")), nil
	}
}

func validPollOption(option string) bool {
	trimmed := strings.TrimSpace(option)
	return trimmed != "" && len(trimmed) <= 128
}
