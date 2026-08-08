package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"server/internal/models"
)

// Endpoints that exist purely to serve the public site, replacing the
// equivalents it used to read from the legacy WordPress install.

// GetRandomArticle returns one randomly chosen live article. The public site
// only needs somewhere to redirect to, so this returns the slug rather than the
// whole article.
//
// @Summary Get a random published article
// @Tags articles
// @Produce json
// @Success 200 {object} models.RandomArticleResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/random [get]
func GetRandomArticle(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unauthenticated endpoint, so it is pinned to published, non-archived
		// rows the same way the public article listing is.
		var slug, title string
		err := conn.QueryRowContext(r.Context(), "SELECT `slug`, `title` FROM `articles` WHERE `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL AND TRIM(COALESCE(`slug`, '')) <> '' ORDER BY RAND() LIMIT 1").Scan(&slug, &title)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "no published articles")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, models.RandomArticleResponse{Slug: slug, Title: title})
	})
}

// GetSitemapSlugs returns every live article's slug and last-modified date, for
// the public site's year-partitioned sitemaps. It is deliberately unpaginated:
// the caller needs the whole set to bucket it by year, and both sitemap routes
// fetch it in full.
//
// Articles flagged noindex are excluded: listing a URL in the sitemap while its
// page asks robots not to index it is a contradiction search engines report as
// an error, so the flag has to be honoured in both places or neither.
//
// @Summary List slugs and modification dates for the sitemap
// @Tags articles
// @Produce json
// @Success 200 {array} models.SitemapSlug
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/sitemap/slugs [get]
func GetSitemapSlugs(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := conn.QueryContext(r.Context(), "SELECT `slug`, COALESCE(`mod_date`, `pub_date`) FROM `articles` WHERE `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL AND TRIM(COALESCE(`slug`, '')) <> '' AND COALESCE(`noindex`, 0) = 0 ORDER BY `pub_date` DESC")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		slugs := make([]models.SitemapSlug, 0)
		for rows.Next() {
			var slug string
			var lastmod sql.NullTime
			if err := rows.Scan(&slug, &lastmod); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			entry := models.SitemapSlug{Slug: slug}
			if lastmod.Valid {
				entry.LastMod = lastmod.Time.UTC().Format(time.RFC3339)
			}
			slugs = append(slugs, entry)
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		setAlwaysPublicCache(w)
		writeJSON(w, http.StatusOK, slugs)
	})
}
