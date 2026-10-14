package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

var quizSlugCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func quizSlugFromTitle(title string) string {
	s := strings.Trim(quizSlugCleaner.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > 180 {
		s = strings.TrimRight(s[:180], "-")
	}
	if s == "" {
		return "quiz"
	}
	return s
}

func quizIDParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid quiz id")
	}
	return id, nil
}

// decodeQuizInput reads and validates a create or update body. Strict rules
// run when the body publishes or the stored quiz is already published, so a
// published quiz can never be saved into a shape it could not be published
// in. On a validation failure it has already written the 422 and returns
// ok=false.
func decodeQuizInput(w http.ResponseWriter, r *http.Request, conn *sql.DB, alreadyPublished bool) (in models.QuizInput, def string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxQuizBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return in, "", false
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Slug == "" {
		in.Slug = quizSlugFromTitle(in.Title)
	}
	if in.Status == "" {
		in.Status = models.QuizStatusDraft
	}
	db.DeriveQuizBandKeys(in.Kind, &in.Definition)
	strict := alreadyPublished || in.Status == models.QuizStatusPublished
	errs, warnings := db.ValidateQuizDefinition(in.Kind, in.Definition, strict)
	fieldErrs, err := db.ValidateQuizFields(r.Context(), conn, in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to validate quiz")
		return in, "", false
	}
	errs = append(errs, fieldErrs...)
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, models.QuizValidationResponse{Errors: errs, Warnings: warnings})
		return in, "", false
	}
	raw, err := json.Marshal(in.Definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encode quiz")
		return in, "", false
	}
	return in, string(raw), true
}

func writeQuiz(w http.ResponseWriter, r *http.Request, conn *sql.DB, id int64, status int) {
	q, err := db.GetQuizByID(r.Context(), conn, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load quiz")
		return
	}
	if err := resolveQuizImages(r.Context(), conn, q); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load quiz")
		return
	}
	writeJSON(w, status, q)
}

// @Summary List quizzes
// @Tags quiz
// @Produce json
// @Param status query string false "draft or published"
// @Success 200 {array} models.QuizListItem
// @Security BearerAuth
// @Router /v1/quizzes [get]
func GetQuizzes(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		if status != "" && status != models.QuizStatusDraft && status != models.QuizStatusPublished {
			writeError(w, http.StatusBadRequest, "status must be draft or published")
			return
		}
		items, err := db.ListQuizzes(r.Context(), conn, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list quizzes")
			return
		}
		writeJSON(w, http.StatusOK, items)
	})
}

// @Summary Get a quiz in any status
// @Tags quiz
// @Produce json
// @Param id path int true "Quiz id"
// @Success 200 {object} models.Quiz
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/quizzes/{id} [get]
func GetQuiz(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := quizIDParam(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := db.GetQuizByID(r.Context(), conn, id); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		}
		writeQuiz(w, r, conn, id, http.StatusOK)
	})
}

// @Summary Create a quiz
// @Tags quiz
// @Accept json
// @Produce json
// @Param body body models.QuizInput true "Quiz"
// @Success 201 {object} models.Quiz
// @Failure 422 {object} models.QuizValidationResponse
// @Security BearerAuth
// @Router /v1/quizzes [post]
func PostQuiz(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, def, ok := decodeQuizInput(w, r, conn, false)
		if !ok {
			return
		}
		slug, err := db.UniqueQuizSlug(r.Context(), conn, in.Slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create quiz")
			return
		}
		in.Slug = slug
		id, err := db.CreateQuiz(r.Context(), conn, in, def)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create quiz")
			return
		}
		activity.LogRequest(r, "quiz_updated", fmt.Sprintf("Created quiz: %s", in.Title))
		writeQuiz(w, r, conn, id, http.StatusCreated)
	})
}

// @Summary Update a quiz
// @Tags quiz
// @Accept json
// @Produce json
// @Param id path int true "Quiz id"
// @Param reset_counts query bool false "Allow a structural change to reset counts"
// @Param body body models.QuizInput true "Quiz with the loaded edit_version"
// @Success 200 {object} models.Quiz
// @Failure 409 {object} models.QuizConflictResponse
// @Failure 422 {object} models.QuizValidationResponse
// @Security BearerAuth
// @Router /v1/quizzes/{id} [patch]
func PatchQuiz(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := quizIDParam(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		stored, err := db.GetQuizByID(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load quiz")
			return
		}
		in, def, ok := decodeQuizInput(w, r, conn, stored.Status == models.QuizStatusPublished)
		if !ok {
			return
		}
		// The slug is the shared link once a quiz is public, so it locks.
		if stored.Status == models.QuizStatusPublished && in.Slug != stored.Slug {
			writeJSON(w, http.StatusUnprocessableEntity, models.QuizValidationResponse{Errors: []string{"The slug cannot change once the quiz is published"}})
			return
		}
		reset := r.URL.Query().Get("reset_counts") == "true"
		err = db.UpdateQuiz(r.Context(), conn, id, in, def, reset)
		switch {
		case errors.Is(err, db.ErrQuizStale):
			writeJSON(w, http.StatusConflict, models.QuizConflictResponse{Reason: "stale"})
			return
		case errors.Is(err, db.ErrQuizResetRequired):
			writeJSON(w, http.StatusConflict, models.QuizConflictResponse{Reason: "reset_required"})
			return
		case errors.Is(err, db.ErrQuizSlugTaken):
			writeJSON(w, http.StatusUnprocessableEntity, models.QuizValidationResponse{Errors: []string{"Another quiz already uses this slug"}})
			return
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to update quiz")
			return
		}
		activity.LogRequest(r, "quiz_updated", fmt.Sprintf("Updated quiz #%d: %s", id, in.Title))
		writeQuiz(w, r, conn, id, http.StatusOK)
	})
}

// @Summary Delete a quiz
// @Tags quiz
// @Accept json
// @Param id path int true "Quiz id"
// @Param body body models.QuizInput true "Only edit_version is read"
// @Success 204
// @Failure 409 {object} models.QuizConflictResponse
// @Security BearerAuth
// @Router /v1/quizzes/{id} [delete]
func DeleteQuiz(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := quizIDParam(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			EditVersion int `json:"edit_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		err = db.DeleteQuiz(r.Context(), conn, id, body.EditVersion)
		switch {
		case errors.Is(err, db.ErrQuizStale):
			writeJSON(w, http.StatusConflict, models.QuizConflictResponse{Reason: "stale"})
			return
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "quiz not found")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to delete quiz")
			return
		}
		activity.LogRequest(r, "quiz_updated", fmt.Sprintf("Deleted quiz #%d", id))
		w.WriteHeader(http.StatusNoContent)
	})
}

// @Summary Count articles that embed a quiz
// @Tags quiz
// @Produce json
// @Param id path int true "Quiz id"
// @Success 200 {object} models.QuizReferencesResponse
// @Security BearerAuth
// @Router /v1/quizzes/{id}/references [get]
func GetQuizReferences(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := quizIDParam(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		refs, err := db.CountQuizReferences(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to count references")
			return
		}
		writeJSON(w, http.StatusOK, refs)
	})
}
