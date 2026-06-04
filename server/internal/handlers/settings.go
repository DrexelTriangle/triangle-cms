package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

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
		writeJSON(w, http.StatusOK, models.SiteSettingsResponse{SiteTitle: siteTitle})
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
		w.WriteHeader(http.StatusNoContent)
	})
}
