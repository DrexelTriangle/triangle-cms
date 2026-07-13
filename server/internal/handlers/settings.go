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

// @Summary Get site settings
// @Tags settings
// @Produce json
// @Success 200 {object} models.SiteSettingsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/settings/site [get]
func GetSiteSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siteTitle, err := db.GetSiteTitle(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch site settings")
			return
		}
		writeJSON(w, http.StatusOK, models.SiteSettingsResponse{SiteTitle: siteTitle})
	})
}

// @Summary Update site settings
// @Tags settings
// @Accept json
// @Produce json
// @Param body body models.SiteSettingsPatchRequest true "Site settings"
// @Success 200 {object} models.SiteSettingsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/site [patch]
func PatchSiteSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.SiteSettingsPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		siteTitle := strings.TrimSpace(body.SiteTitle)
		if siteTitle == "" {
			writeError(w, http.StatusBadRequest, "site_title is required")
			return
		}

		if err := db.SetSiteTitle(r.Context(), conn, siteTitle); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update site settings")
			return
		}
		activity.LogRequest(r, "settings_changed", "Site title updated", "site_title", siteTitle)
		writeJSON(w, http.StatusOK, models.SiteSettingsResponse{SiteTitle: siteTitle})
	})
}

// @Summary Get breaking-news banner settings
// @Tags settings
// @Produce json
// @Success 200 {object} models.BreakingNewsSettingsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/settings/breaking-news [get]
func GetBreakingNews(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := db.GetBreakingNews(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch breaking-news settings")
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})
}

// @Summary Update breaking-news banner settings
// @Tags settings
// @Accept json
// @Produce json
// @Param body body models.BreakingNewsSettingsPatchRequest true "Breaking-news settings"
// @Success 200 {object} models.BreakingNewsSettingsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/breaking-news [patch]
func PatchBreakingNews(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.BreakingNewsSettingsPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		body.Text = strings.TrimSpace(body.Text)
		if body.Enabled && body.Text == "" {
			writeError(w, http.StatusBadRequest, "text is required when the banner is enabled")
			return
		}

		if err := db.SetBreakingNews(r.Context(), conn, body); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update breaking-news settings")
			return
		}
		state := "disabled"
		if body.Enabled {
			state = "enabled"
		}
		activity.LogRequest(r, "settings_changed", "Breaking-news banner updated", "breaking_news", state)
		writeJSON(w, http.StatusOK, body)
	})
}

// @Summary Rebuild taxonomy article counts
// @Tags settings
// @Success 204
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/taxonomy/rebuild [post]
func PostRebuildTaxonomyCounts(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.RebuildTaxonomyArticleCounts(r.Context(), conn); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to rebuild taxonomy article counts")
			return
		}
		activity.LogRequest(r, "settings_changed", "Rebuilt taxonomy article counts")
		w.WriteHeader(http.StatusNoContent)
	})
}
