package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	db "server/internal/database"
)

type siteSettingsResponse struct {
	SiteTitle string `json:"site_title"`
}

type siteSettingsPatchRequest struct {
	SiteTitle string `json:"site_title"`
}

func GetSiteSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siteTitle, err := db.GetSiteTitle(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch site settings")
			return
		}
		writeJSON(w, http.StatusOK, siteSettingsResponse{SiteTitle: siteTitle})
	})
}

func PatchSiteSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body siteSettingsPatchRequest
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
		writeJSON(w, http.StatusOK, siteSettingsResponse{SiteTitle: siteTitle})
	})
}
