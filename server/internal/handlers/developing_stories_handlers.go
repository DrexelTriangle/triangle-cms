package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// @Summary Get developing stories
// @Tags developing-stories
// @Produce json
// @Success 200 {object} models.DevelopingStoriesResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/developing-stories [get]
func GetDevelopingStories(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stories, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch developing stories")
			return
		}
		writeJSON(w, http.StatusOK, models.DevelopingStoriesResponse{Stories: stories})
	})
}

// @Summary Add developing story
// @Tags developing-stories
// @Accept json
// @Param body body models.DevelopingStoryRequest true "Story title"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/developing-stories [post]
func PostDevelopingStory(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.DevelopingStoryRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		title := strings.TrimSpace(body.Title)
		if !validDevelopingStoryTitle(title) {
			writeError(w, http.StatusBadRequest, "invalid story title")
			return
		}

		stories, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load developing stories")
			return
		}
		for _, existing := range stories {
			if existing == title {
				writeError(w, http.StatusConflict, "developing story already exists")
				return
			}
		}

		stories = append(stories, title)
		if err := db.SetDevelopingStories(r.Context(), conn, stories); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save developing stories")
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
}

// @Summary Delete developing story
// @Tags developing-stories
// @Accept json
// @Param body body models.DevelopingStoryRequest true "Story title"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/developing-stories [delete]
func DeleteDevelopingStory(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.DevelopingStoryRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		title := strings.TrimSpace(body.Title)
		if !validDevelopingStoryTitle(title) {
			writeError(w, http.StatusBadRequest, "invalid story title")
			return
		}

		stories, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load developing stories")
			return
		}

		next := make([]string, 0, len(stories))
		removed := false
		for _, story := range stories {
			if story == title {
				removed = true
				continue
			}
			next = append(next, story)
		}
		if !removed {
			writeError(w, http.StatusNotFound, "developing story not found")
			return
		}

		if err := db.SetDevelopingStories(r.Context(), conn, next); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save developing stories")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func validDevelopingStoryTitle(title string) bool {
	n := len(strings.TrimSpace(title))
	return n > 0 && n <= 200
}
