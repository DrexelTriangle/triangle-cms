package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

const (
	maxQuizBatchIDs  = 10
	maxQuizBodyBytes = 256 * 1024
)

// resolveQuizImages replaces every media id in the quiz with a display URL.
// Missing media rows resolve to null rather than failing the read, so a
// deleted library image never takes a quiz down.
func resolveQuizImages(ctx context.Context, conn *sql.DB, q *models.Quiz) error {
	urlFor := func(id *int64) (*string, error) {
		if id == nil {
			return nil, nil
		}
		item, err := db.GetMediaByID(ctx, conn, *id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		u := uploadURL(item.Path)
		return &u, nil
	}
	var err error
	if q.CoverURL, err = urlFor(q.CoverMediaID); err != nil {
		return err
	}
	for i := range q.Definition.Questions {
		if q.Definition.Questions[i].ImageURL, err = urlFor(q.Definition.Questions[i].ImageMediaID); err != nil {
			return err
		}
	}
	for i := range q.Definition.Results {
		if q.Definition.Results[i].ImageURL, err = urlFor(q.Definition.Results[i].ImageMediaID); err != nil {
			return err
		}
	}
	return nil
}

// @Summary Get a published quiz by slug
// @Tags quiz
// @Produce json
// @Param slug path string true "Quiz slug"
// @Success 200 {object} models.Quiz
// @Failure 404 {object} models.ErrorResponse
// @Router /v1/quiz/{slug} [get]
func GetQuizBySlug(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, err := db.GetPublishedQuizBySlug(r.Context(), conn, strings.TrimSpace(r.PathValue("slug")))
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch quiz")
			return
		}
		if err := resolveQuizImages(r.Context(), conn, q); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch quiz")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, q)
	})
}

// @Summary Get published quizzes by id
// @Tags quiz
// @Produce json
// @Param ids query string true "Comma-separated ids, at most 10"
// @Success 200 {object} map[string]models.Quiz
// @Failure 400 {object} models.ErrorResponse
// @Router /v1/quiz [get]
func GetQuizzesByIDs(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ids []int64
		seen := map[int64]bool{}
		for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				writeError(w, http.StatusBadRequest, "ids must be positive integers")
				return
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 || len(ids) > maxQuizBatchIDs {
			writeError(w, http.StatusBadRequest, "ids must hold 1 to 10 ids")
			return
		}
		quizzes, err := db.GetPublishedQuizzesByIDs(r.Context(), conn, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch quizzes")
			return
		}
		out := map[string]*models.Quiz{}
		for id, q := range quizzes {
			if err := resolveQuizImages(r.Context(), conn, q); err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to fetch quizzes")
				return
			}
			out[strconv.FormatInt(id, 10)] = q
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	})
}

// PostQuizAnswer records one answer. It is public and sits behind its own
// RateLimitByIP(60, time.Minute) in routes.go. The limiter keys on the first
// X-Forwarded-For entry. The admin SPA calls this API directly, so the header
// is trusted only because the deployment's proxy is expected to overwrite it
// with the reader's address; without that the limiter is advisory.
//
// @Summary Record a quiz answer
// @Tags quiz
// @Accept json
// @Produce json
// @Param id path int true "Quiz id"
// @Param body body models.QuizAnswerInput true "Answer"
// @Success 200 {object} models.QuizAnswerCountsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 415 {object} models.ErrorResponse
// @Router /v1/quiz/{id}/answers [post]
func PostQuizAnswer(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid quiz id")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var in models.QuizAnswerInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		counts, err := db.RecordQuizAnswer(r.Context(), conn, id, in)
		switch {
		case errors.Is(err, sql.ErrNoRows), errors.Is(err, db.ErrQuizNotPublished):
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		case errors.Is(err, db.ErrQuizVersionMismatch):
			writeError(w, http.StatusConflict, "quiz was updated")
			return
		case errors.Is(err, db.ErrQuizIndexOutOfRange):
			writeError(w, http.StatusBadRequest, "question or answer out of range")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to record answer")
			return
		}
		writeJSON(w, http.StatusOK, models.QuizAnswerCountsResponse{Counts: counts})
	})
}
