package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
		setAlwaysPublicCache(w)
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
		setAlwaysPublicCache(w)
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

// @Summary Get homepage carousel settings
// @Tags settings
// @Produce json
// @Success 200 {object} models.HomepageCarouselSettingsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/settings/homepage-carousel [get]
func GetHomepageCarousel(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slides, err := db.GetHomepageCarousel(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch homepage carousel settings")
			return
		}
		setAlwaysPublicCache(w)
		writeJSON(w, http.StatusOK, models.HomepageCarouselSettingsResponse{Slides: slides})
	})
}

// @Summary Update homepage carousel settings
// @Tags settings
// @Accept json
// @Produce json
// @Param body body models.HomepageCarouselSettingsPatchRequest true "Homepage carousel settings"
// @Success 200 {object} models.HomepageCarouselSettingsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/homepage-carousel [patch]
func PatchHomepageCarousel(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.HomepageCarouselSettingsPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		slides := db.NormalizeHomepageCarouselSlides(body.Slides)
		if err := validateHomepageCarouselSlides(slides); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		if err := db.SetHomepageCarousel(r.Context(), conn, slides); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update homepage carousel settings")
			return
		}
		activity.LogRequest(r, "settings_changed", "Homepage carousel updated", "homepage_carousel", strconv.Itoa(len(slides)))
		writeJSON(w, http.StatusOK, models.HomepageCarouselSettingsResponse{Slides: slides})
	})
}

// @Summary Get the public-site footer menu
// @Tags settings
// @Produce json
// @Success 200 {object} models.FooterSettingsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/settings/footer [get]
func GetFooterSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := db.GetFooterSettings(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch footer settings")
			return
		}
		setAlwaysPublicCache(w)
		writeJSON(w, http.StatusOK, settings)
	})
}

// @Summary Update the public-site footer menu
// @Tags settings
// @Accept json
// @Produce json
// @Param body body models.FooterSettingsPatchRequest true "Footer menu"
// @Success 200 {object} models.FooterSettingsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/footer [patch]
func PatchFooterSettings(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.FooterSettingsPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		if err := db.SetFooterSettings(r.Context(), conn, body); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update footer settings")
			return
		}

		// Read back rather than echo: the stored menu is normalized, and an
		// empty save reverts to the default columns.
		saved, err := db.GetFooterSettings(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch footer settings")
			return
		}
		activity.LogRequest(r, "settings_changed", "Footer menu updated")
		writeJSON(w, http.StatusOK, saved)
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

func validateHomepageCarouselSlides(slides []models.HomepageCarouselSlide) error {
	if len(slides) > 20 {
		return fmt.Errorf("homepage carousel cannot have more than 20 slides")
	}
	for idx, slide := range slides {
		n := idx + 1
		if slide.Enabled && slide.LinkURL == "" {
			return fmt.Errorf("slide %d link_url is required when enabled", n)
		}
		if slide.Enabled && slide.Title == "" && slide.ImageURL == "" {
			return fmt.Errorf("slide %d needs a title or image_url when enabled", n)
		}
		if len(slide.Title) > 160 {
			return fmt.Errorf("slide %d title cannot exceed 160 characters", n)
		}
		if len(slide.LinkURL) > 2048 {
			return fmt.Errorf("slide %d link_url cannot exceed 2048 characters", n)
		}
		if len(slide.ImageURL) > 2048 {
			return fmt.Errorf("slide %d image_url cannot exceed 2048 characters", n)
		}
		if len(slide.BackgroundColor) > 32 {
			return fmt.Errorf("slide %d background_color cannot exceed 32 characters", n)
		}
		if len(slide.TextColor) > 32 {
			return fmt.Errorf("slide %d text_color cannot exceed 32 characters", n)
		}
		if slide.LinkURL != "" && !validCarouselURL(slide.LinkURL) {
			return fmt.Errorf("slide %d link_url must be a relative URL or absolute http(s) URL", n)
		}
		if slide.ImageURL != "" && !validCarouselURL(slide.ImageURL) {
			return fmt.Errorf("slide %d image_url must be a relative URL or absolute http(s) URL", n)
		}
	}
	return nil
}

func validCarouselURL(raw string) bool {
	if strings.HasPrefix(raw, "/") {
		return !strings.HasPrefix(raw, "//")
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
