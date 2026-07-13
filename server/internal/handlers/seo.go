package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

// @Summary Get site-wide SEO / social defaults
// @Tags settings
// @Produce json
// @Success 200 {object} models.SEOSettingsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/settings/seo [get]
func GetSEOSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := db.GetSEOSettings(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch SEO settings")
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})
}

// @Summary Update site-wide SEO / social defaults
// @Tags settings
// @Accept json
// @Produce json
// @Param body body models.SEOSettingsPatchRequest true "SEO settings"
// @Success 200 {object} models.SEOSettingsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/seo [patch]
func PatchSEOSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.SEOSettingsPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		if err := db.SetSEOSettings(r.Context(), conn, body); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update SEO settings")
			return
		}

		// Return the persisted values, including any defaults applied for blank fields.
		saved, err := db.GetSEOSettings(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch SEO settings")
			return
		}
		activity.LogRequest(r, "settings_changed", "SEO settings updated")
		writeJSON(w, http.StatusOK, saved)
	})
}

// @Summary Audit published articles for SEO issues
// @Tags settings
// @Produce json
// @Success 200 {object} models.SEOAuditResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/seo/audit [get]
func GetSEOAudit(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		audit, err := db.GetSEOAudit(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to run SEO audit")
			return
		}
		writeJSON(w, http.StatusOK, audit)
	})
}
