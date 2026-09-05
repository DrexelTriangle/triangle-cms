package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"strconv"
	"strings"
	"time"
)

// @Summary Liveness check
// @Tags health
// @Produce json
// @Success 200 {object} models.HealthResponse
// @Router /v1/health [get]
func HealthCheck(w http.ResponseWriter, r *http.Request) {

	writeJSON(w, http.StatusOK, models.HealthResponse{Status: "Ok"})
}

// @Summary Readiness check
// @Tags health
// @Produce json
// @Success 200 {object} models.HealthResponse
// @Failure 503 {object} models.ErrorResponse
// @Router /v1/health/db [get]
func HealthReady(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := conn.PingContext(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "Database unreachable")
			return
		}
		writeJSON(w, http.StatusOK, models.HealthResponse{Status: "Ok"})
	}
}

func intParam(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, models.ErrorResponse{Error: msg})
}

// publicReadCacheControl bounds how stale a public read may be.
//
// Without a Cache-Control header every intermediary picks its own heuristic,
// and Scalene's fetch cache has no expiry to respect at all, so an edit could
// take an unbounded time to appear on the site. 60s is short enough that an
// editor who fixes a headline sees it on the next reload rather than wondering
// whether the save worked, and long enough that a page is not re-rendered per
// visitor. stale-while-revalidate keeps that bound cheap: a traffic spike is
// still served from cache while one request refreshes it behind the scenes.
const publicReadCacheControl = "public, max-age=60, stale-while-revalidate=300"

// uncacheableCacheControl is what an editor's copy of a public read gets.
const uncacheableCacheControl = "private, no-store"

// setPublicReadCache allows caching only for anonymous public reads.
// Credentialed requests may include drafts and must remain uncacheable.
// Append Vary to preserve Accept-Encoding. CDN rules must bypass credentialed
// requests where Cookie and Authorization Vary values are unsupported.
func setPublicReadCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Cookie")
	w.Header().Add("Vary", "Authorization")
	if _, isEditor := middleware.UserFromContext(r.Context()); isEditor {
		w.Header().Set("Cache-Control", uncacheableCacheControl)
		return
	}
	w.Header().Set("Cache-Control", publicReadCacheControl)
}

// setAlwaysPublicCache is setPublicReadCache for the reads that have no
// authenticated form at all: no auth middleware is registered on them, so
// every caller gets the same bytes and there is nothing to Vary on.
func setAlwaysPublicCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", publicReadCacheControl)
}

func clientIP(r *http.Request) string {
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				return candidate
			}
		}
	}

	ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && ip != "" {
		return ip
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func titleFromSlug(slug string) string {
	parts := strings.Split(strings.TrimSpace(slug), "-")
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		words = append(words, string(runes))
	}
	return strings.Join(words, " ")
}

func excerptWordLimit(r *http.Request, fallback int) int {
	limit := intParam(r, "excerpt_words", fallback)
	if limit < 0 {
		return 0
	}
	return limit
}

func truncateWords(s string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}

	words := strings.Fields(strings.TrimSpace(s))
	if len(words) <= maxWords {
		return strings.TrimSpace(s)
	}
	return strings.Join(words[:maxWords], " ")
}

func canonicalTitleForTaxonomy(ctx context.Context, conn *sql.DB, kind, slug string) (string, error) {
	trimmedSlug := strings.TrimSpace(slug)
	if trimmedSlug == "" {
		return "", nil
	}

	var canonical sql.NullString
	err := conn.QueryRowContext(
		ctx,
		"SELECT canonical_title FROM site_taxonomy WHERE kind = ? AND slug = ? LIMIT 1",
		kind,
		trimmedSlug,
	).Scan(&canonical)
	if err == sql.ErrNoRows {
		return titleFromSlug(trimmedSlug), nil
	}
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(canonical.String)
	if title == "" {
		return titleFromSlug(trimmedSlug), nil
	}
	return title, nil
}

// visibleSubsectionsOf returns visible direct children for page navigation.
// Hidden descendants still have pages and contribute to section listings.
func visibleSubsectionsOf(ctx context.Context, conn *sql.DB, parentSlug string) ([]models.TaxonomySummary, error) {
	trimmedSection := strings.TrimSpace(parentSlug)
	if trimmedSection == "" {
		return []models.TaxonomySummary{}, nil
	}

	rows, err := conn.QueryContext(
		ctx,
		"SELECT slug, canonical_title FROM site_taxonomy WHERE kind = 'subsection' AND parent_slug = ? AND is_visible = 1 ORDER BY id ASC",
		trimmedSection,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	subsections := make([]models.TaxonomySummary, 0)
	for rows.Next() {
		var slug string
		var canonicalTitle sql.NullString
		if err := rows.Scan(&slug, &canonicalTitle); err != nil {
			return nil, err
		}

		canonical := strings.TrimSpace(canonicalTitle.String)
		if canonical == "" {
			canonical = titleFromSlug(slug)
		}

		subsections = append(subsections, models.TaxonomySummary{
			Slug:           slug,
			Name:           canonical,
			CanonicalTitle: canonical,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return subsections, nil
}

func formatArticleDate(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func isValidCanonicalSlug(slug string) bool {
	return db.IsCanonicalSlug(strings.TrimSpace(slug))
}

func listParams(r *http.Request, defaultLimit int) (page, limit, offset int) {
	limit = intParam(r, "limit", defaultLimit)
	if limit <= 0 {
		limit = defaultLimit
	}

	page = intParam(r, "page", 0)
	if page > 0 {
		offset = (page - 1) * limit
	} else {
		offset = intParam(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}
		page = (offset / limit) + 1
	}
	return page, limit, offset
}

func paginationResponse(page, limit, offset int, hasMore bool, totalCount int) models.Pagination {
	return models.Pagination{
		Page:       page,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
		TotalCount: totalCount,
	}
}
