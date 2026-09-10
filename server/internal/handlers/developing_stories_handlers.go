package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

const maxDevelopingStoryDescription = 500

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
		payload := make([]models.DevelopingStory, 0, len(stories))
		for _, story := range stories {
			payload = append(payload, models.DevelopingStory{Title: story.Title, Description: story.Description})
		}
		writeJSON(w, http.StatusOK, models.DevelopingStoriesResponse{Stories: payload})
	})
}

// @Summary Add developing story
// @Tags developing-stories
// @Accept json
// @Param body body models.DevelopingStoryRequest true "Story title and description"
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
		description := strings.TrimSpace(body.Description)
		if !validDevelopingStoryTitle(title) {
			writeError(w, http.StatusBadRequest, "invalid story title")
			return
		}
		if !validDevelopingStoryDescription(description) {
			writeError(w, http.StatusBadRequest, "invalid story description")
			return
		}

		stories, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load developing stories")
			return
		}
		for _, existing := range stories {
			if existing.Title == title {
				writeError(w, http.StatusConflict, "developing story already exists")
				return
			}
		}

		stories = append(stories, db.DevelopingStory{Title: title, Description: description})
		if err := db.SetDevelopingStories(r.Context(), conn, stories); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save developing stories")
			return
		}
		activity.LogRequest(r, "developing_story_added", title)
		w.WriteHeader(http.StatusCreated)
	})
}

// @Summary Update developing story description
// @Tags developing-stories
// @Accept json
// @Param body body models.DevelopingStoryRequest true "Story title and description"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/developing-stories [put]
func PutDevelopingStory(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.DevelopingStoryRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		title := strings.TrimSpace(body.Title)
		description := strings.TrimSpace(body.Description)
		if !validDevelopingStoryTitle(title) {
			writeError(w, http.StatusBadRequest, "invalid story title")
			return
		}
		if !validDevelopingStoryDescription(description) {
			writeError(w, http.StatusBadRequest, "invalid story description")
			return
		}

		stories, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load developing stories")
			return
		}

		updated := false
		for idx, story := range stories {
			if story.Title != title {
				continue
			}
			stories[idx].Description = description
			updated = true
			break
		}
		if !updated {
			writeError(w, http.StatusNotFound, "developing story not found")
			return
		}

		if err := db.SetDevelopingStories(r.Context(), conn, stories); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save developing stories")
			return
		}
		activity.LogRequest(r, "developing_story_updated", title)
		w.WriteHeader(http.StatusNoContent)
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

		next := make([]db.DevelopingStory, 0, len(stories))
		removed := false
		for _, story := range stories {
			if story.Title == title {
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
		activity.LogRequest(r, "developing_story_deleted", title)
		w.WriteHeader(http.StatusNoContent)
	})
}

func validDevelopingStoryTitle(title string) bool {
	n := len(strings.TrimSpace(title))
	return n > 0 && n <= 200
}

func validDevelopingStoryDescription(description string) bool {
	return len(strings.TrimSpace(description)) <= maxDevelopingStoryDescription
}
