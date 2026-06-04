package handlers

import (
	"errors"
	"net/http"

	"server/internal/activity"
	"server/internal/models"
)

// @Summary Get recent CMS activity
// @Tags activity
// @Produce json
// @Param limit query int false "Max events" default(100)
// @Success 200 {object} models.ActivityResponse
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/activity [get]
func GetActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := activity.ListDefault(r.Context(), activity.Query{
			Kind:  "activity",
			Limit: intParam(r, "limit", 100),
		})
		if err != nil {
			if errors.Is(err, activity.ErrStoreUnavailable) {
				writeError(w, http.StatusServiceUnavailable, "activity store unavailable")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to fetch activity")
			return
		}

		events := make([]models.ActivityEventResponse, 0, len(result.Entries))
		for _, entry := range result.Entries {
			events = append(events, models.ActivityEventResponse{
				ID:        entry.ID,
				User:      entry.ActorName,
				Action:    entry.Action,
				Target:    entry.Target,
				Date:      entry.Timestamp,
				UserRole:  entry.ActorRole,
				Message:   entry.Message,
				Level:     entry.Level,
				Method:    entry.Method,
				Path:      entry.Path,
				RawStatus: entry.Status,
			})
		}

		writeJSON(w, http.StatusOK, models.ActivityResponse{
			Events:     events,
			TotalCount: result.TotalCount,
		})
	}
}
