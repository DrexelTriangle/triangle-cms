package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	"server/internal/akismet"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
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

func requireArticleWriteRole(w http.ResponseWriter, r *http.Request) bool {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || (user.Role != models.RoleAdmin && user.Role != models.RoleEditor) {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
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

func subsectionsForSection(ctx context.Context, conn *sql.DB, sectionSlug string) ([]models.TaxonomySummary, error) {
	trimmedSection := strings.TrimSpace(sectionSlug)
	if trimmedSection == "" {
		return []models.TaxonomySummary{}, nil
	}

	rows, err := conn.QueryContext(
		ctx,
		"SELECT slug, canonical_title FROM site_taxonomy WHERE kind = 'subsection' AND parent_slug = ? ORDER BY id ASC",
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

func taxonomyCountSlugs(categories []string) []string {
	uniqueSlugs := make([]string, 0, len(categories))
	seen := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		slug := db.CanonicalizeSlug(category)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		uniqueSlugs = append(uniqueSlugs, slug)
	}
	return uniqueSlugs
}

func adjustTaxonomyArticleCounts(ctx context.Context, conn *sql.DB, categories []string, delta int64) error {
	if conn == nil || len(categories) == 0 || delta == 0 {
		return nil
	}

	uniqueSlugs := taxonomyCountSlugs(categories)
	if len(uniqueSlugs) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(uniqueSlugs))
	args := make([]any, 0, len(uniqueSlugs)+2)
	args = append(args, string(models.TaxonomyTypeSection), string(models.TaxonomyTypeSubsection))
	for _, slug := range uniqueSlugs {
		placeholders = append(placeholders, "?")
		args = append(args, slug)
	}

	query := "UPDATE site_taxonomy SET article_count = GREATEST(0, article_count + ?) WHERE kind IN (?, ?) AND slug IN (" + strings.Join(placeholders, ", ") + ")"
	args = append([]any{delta}, args...)
	_, err := conn.ExecContext(ctx, query, args...)
	return err
}

func incrementTaxonomyArticleCounts(ctx context.Context, conn *sql.DB, categories []string) error {
	return adjustTaxonomyArticleCounts(ctx, conn, categories, 1)
}

func decrementTaxonomyArticleCounts(ctx context.Context, conn *sql.DB, categories []string) error {
	return adjustTaxonomyArticleCounts(ctx, conn, categories, -1)
}

func parseArticleCategoryValue(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var categories []string
	if err := json.Unmarshal([]byte(raw.String), &categories); err != nil {
		return strings.Split(raw.String, ",")
	}
	return categories
}

func reconcileTaxonomyArticleCounts(ctx context.Context, conn *sql.DB, oldCategories, newCategories []string) error {
	oldSlugs := taxonomyCountSlugs(oldCategories)
	newSlugs := taxonomyCountSlugs(newCategories)

	oldSet := make(map[string]struct{}, len(oldSlugs))
	for _, slug := range oldSlugs {
		oldSet[slug] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newSlugs))
	for _, slug := range newSlugs {
		newSet[slug] = struct{}{}
	}

	toDecrement := make([]string, 0)
	for _, slug := range oldSlugs {
		if _, ok := newSet[slug]; !ok {
			toDecrement = append(toDecrement, slug)
		}
	}

	toIncrement := make([]string, 0)
	for _, slug := range newSlugs {
		if _, ok := oldSet[slug]; !ok {
			toIncrement = append(toIncrement, slug)
		}
	}

	if err := decrementTaxonomyArticleCounts(ctx, conn, toDecrement); err != nil {
		return err
	}
	if err := incrementTaxonomyArticleCounts(ctx, conn, toIncrement); err != nil {
		return err
	}
	return nil
}

func loadArticleCategoriesByArchiveState(ctx context.Context, conn *sql.DB, slug string, archived bool) ([]string, bool, error) {
	query := "SELECT categories FROM articles WHERE slug = ? AND archived_at IS NULL LIMIT 1"
	if archived {
		query = "SELECT categories FROM articles WHERE slug = ? AND archived_at IS NOT NULL LIMIT 1"
	}

	var raw sql.NullString
	err := conn.QueryRowContext(ctx, query, slug).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return parseArticleCategoryValue(raw), true, nil
}

// last_pub_date remembers the publish date an article had before it was pulled
// back to draft, so re-publishing restores the original date instead of stamping
// today's. See articlePatchDateColumns.
func loadArticlePublishDate(ctx context.Context, conn *sql.DB, slug string) (publishedAt, lastPublishedAt sql.NullTime, err error) {
	err = conn.QueryRowContext(ctx,
		"SELECT pub_date, last_pub_date FROM articles WHERE slug = ? AND archived_at IS NULL LIMIT 1",
		slug,
	).Scan(&publishedAt, &lastPublishedAt)
	if err == sql.ErrNoRows {
		return sql.NullTime{}, sql.NullTime{}, nil
	}
	return publishedAt, lastPublishedAt, err
}

func formatArticleDate(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// Saving an article must never move its publish date on its own: the editor
// sends the whole form on every save and every autosave, so any date the handler
// invents here would silently overwrite the real one.
//   - An explicit published_date always wins.
//   - Going to draft clears the live date but parks it in last_pub_date.
//   - Publishing without a date reuses the article's own date -- current first,
//     then the parked one -- and only falls back to now for something that has
//     genuinely never been published.
func articlePatchDateColumns(statusSet bool, status models.ArticleStatus, publishedDateSet bool, publishedDateValue, scheduledDateValue any, currentPublishedAt, lastPublishedAt sql.NullTime, now time.Time) ([]string, []any) {
	if statusSet && status == models.ArticleStatusDraft {
		cols := []string{"pub_date", "scheduled_pub_date"}
		args := []any{nil, nil}
		if currentPublishedAt.Valid {
			cols = append(cols, "last_pub_date")
			args = append(args, formatArticleDate(currentPublishedAt.Time))
		}
		return cols, args
	}
	if publishedDateSet {
		return []string{"pub_date", "scheduled_pub_date"}, []any{publishedDateValue, scheduledDateValue}
	}
	if statusSet && status == models.ArticleStatusPublished {
		cols := []string{"scheduled_pub_date"}
		args := []any{nil}
		if !currentPublishedAt.Valid {
			restored := formatArticleDate(now)
			if lastPublishedAt.Valid {
				restored = formatArticleDate(lastPublishedAt.Time)
			}
			cols = append([]string{"pub_date"}, cols...)
			args = append([]any{restored}, args...)
		}
		return cols, args
	}
	return nil, nil
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

// orderCategoriesForSection moves the categories belonging to the section being
// rendered to the front, so the first one is the one that explains why this
// article is in this list.
//
// The card shows a single category as its kicker and takes the first. Without
// this it takes whichever the article happens to list first, which is how the
// Columns block came to label a column "MEN'S LACROSSE": the article is
// ["Men's Lacrosse", "From the Playbook", "Sports"], it is in Columns because
// of From the Playbook, and it announced itself as a sport. Ordering here
// rather than in the card fixes every surface at once, and it is the only place
// that knows which section was asked for.
//
// A stable partition, so the article's own order still decides between two
// categories that are both in the section.
func orderCategoriesForSection(categories []models.CategorySummary, preferSlugs []string) {
	if len(preferSlugs) == 0 || len(categories) < 2 {
		return
	}
	preferred := make(map[string]struct{}, len(preferSlugs))
	for _, slug := range preferSlugs {
		if normalized := strings.ToLower(strings.TrimSpace(slug)); normalized != "" {
			preferred[normalized] = struct{}{}
		}
	}
	sort.SliceStable(categories, func(i, j int) bool {
		_, iPreferred := preferred[strings.ToLower(categories[i].Slug)]
		_, jPreferred := preferred[strings.ToLower(categories[j].Slug)]
		return iPreferred && !jPreferred
	})
}

// categoryPreferenceSlugs is which slugs explain a listing, for ordering each
// article's categories. A subsection stands on its own: on a subsection page
// the subsection is the reason the article is there, not the parent section it
// also carries. Empty when the listing has no section context at all.
func categoryPreferenceSlugs(params ArticleParams) []string {
	if params.Subsection != "" {
		return []string{params.Subsection}
	}
	return params.SectionMatchSlugs
}

// articleListItems converts articles for a listing. preferSlugs is the section
// and subsections the listing was asked for, empty when there is no section
// context -- an author page or an unfiltered listing -- where no category is
// more relevant than another and the article's own order stands.
func articleListItems(articles []models.Article, excerptWords int, preferSlugs ...string) []models.ArticleListItem {
	items := make([]models.ArticleListItem, 0, len(articles))
	for _, article := range articles {
		categories := make([]models.CategorySummary, 0, len(article.Categories))
		for _, category := range article.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, models.CategorySummary{Name: name, Slug: db.CategoryLinkSlug(name)})
		}
		orderCategoriesForSection(categories, preferSlugs)

		authors := make([]models.AuthorSummary, 0, len(article.Authors))
		for _, author := range article.Authors {
			authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
		}

		item := models.ArticleListItem{
			Title:         article.Title,
			ID:            article.ID,
			Authors:       authors,
			Categories:    categories,
			Excerpt:       truncateWords(article.Excerpt, excerptWords),
			Slug:          article.Slug,
			Status:        article.Status,
			CommentStatus: article.CommentStatus,
			FeaturedImage: article.PhotoURL,
			IsFeatured:    article.IsFeatured,
			BreakingNews:  article.BreakingNews,
		}
		item.PublishedDate = article.PublishedAt
		item.CreationDate = article.CreatedAt
		items = append(items, item)
	}
	return items
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

// authorArchiveCondition honours ?archived only for an identified editor.
// /v1/authors is public, and soft-deleted authors are not public information.
func authorArchiveCondition(q url.Values, isEditor bool) string {
	if _, archivedProvided := q["archived"]; archivedProvided && isEditor {
		archivedRaw := strings.ToLower(strings.TrimSpace(q.Get("archived")))
		switch archivedRaw {
		case "", "1", "true", "yes":
			return "a.`archived_at` IS NOT NULL"
		case "0", "false", "no":
			return "a.`archived_at` IS NULL"
		default:
			return "a.`archived_at` IS NOT NULL"
		}
	}
	return "a.`archived_at` IS NULL"
}

func authorIDsFromOverviews(authors []models.AuthorOverview) []int64 {
	ids := make([]int64, 0, len(authors))
	for _, author := range authors {
		ids = append(ids, author.ID)
	}
	return ids
}

func parseAuthorIDs(raw any) ([]int64, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, strconv.ErrSyntax
	}

	authorIDs := make([]int64, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case float64:
			if v != float64(int64(v)) {
				return nil, strconv.ErrSyntax
			}
			authorIDs = append(authorIDs, int64(v))
		case int64:
			authorIDs = append(authorIDs, v)
		default:
			return nil, strconv.ErrSyntax
		}
	}

	return authorIDs, nil
}

// @Summary List authors
// @Tags authors
// @Produce json
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param article_id query int false "Filter by article ID"
// @Param search query string false "Filter by display name, login, or email (substring match)"
// @Param archived query bool false "When true, return only soft-deleted authors. Ignored for unauthenticated callers."
// @Param sort_by query string false "Sort field (id sorts by creation order)" Enums(display_name,id)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} models.AuthorsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors [get]
func GetAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)
		page := intParam(r, "page", 0)
		if page <= 0 {
			page = 1
			if limit > 0 {
				page = (offset / limit) + 1
			}
		}
		articleID := intParam(r, "article_id", 0)
		sortBy := q.Get("sort_by")
		if _, ok := db.AuthorSortByColumn[sortBy]; !ok {
			sortBy = ""
		}

		var conditions []string
		var args []any

		if articleID > 0 {
			conditions = append(conditions, "a.`id` IN (SELECT `author_id` FROM `articles_authors` WHERE `articles_id` = ?)")
			args = append(args, articleID)
		}

		if search := strings.TrimSpace(q.Get("search")); search != "" {
			like := "%" + search + "%"
			conditions = append(conditions, "(a.`display_name` LIKE ? OR a.`login` LIKE ? OR a.`email` LIKE ?)")
			args = append(args, like, like, like)
		}

		_, isEditor := middleware.UserFromContext(r.Context())
		conditions = append(conditions, authorArchiveCondition(q, isEditor))

		countQuery := "SELECT COUNT(*) FROM `authors` a"
		if len(conditions) > 0 {
			countQuery += " WHERE " + strings.Join(conditions, " AND ")
		}
		var totalCount int
		if err := conn.QueryRowContext(r.Context(), countQuery, args...).Scan(&totalCount); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		query := "SELECT a.`id`, a.`display_name`, a.`login`, a.`email`, COUNT(aa.`articles_id`) AS `article_count`, a.`archived_at` FROM `authors` a LEFT JOIN `articles_authors` aa ON a.`id` = aa.`author_id`"
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY a.`id`, a.`display_name`, a.`login`, a.`email`, a.`archived_at`"
		if sortBy == "" {
			query += " ORDER BY a.`id` DESC"
		}
		query = db.BuildOrderLimit(query, sortBy, q.Get("sort_direction"), db.AuthorSortByColumn, limit, offset)

		rows, err := conn.QueryContext(r.Context(), query, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()

		authors := make([]models.AuthorOverview, 0)
		for rows.Next() {
			a, err := db.ScanAuthorOverview(rows)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authors = append(authors, a)
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := offset+len(authors) < totalCount
		writeJSON(w, http.StatusOK, models.AuthorsResponse{
			Authors:    authors,
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary Create an author
// @Tags authors
// @Accept json
// @Param body body models.AuthorInput true "Author"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors [post]
func PostAuthors(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body models.AuthorInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		slug := strings.TrimSpace(body.Slug)
		if slug == "" {
			writeError(w, http.StatusBadRequest, "slug is required")
			return
		}
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		_, err := db.Insert(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			body.DisplayName, body.FirstName, body.LastName, body.Email, slug,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "author_created", body.DisplayName, "slug", slug)
		w.WriteHeader(http.StatusCreated)
	}
}

// @Summary Get an author by slug
// @Tags authors
// @Produce json
// @Param slug path string true "Author slug"
// @Success 200 {object} models.Author
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors/{slug} [get]
func GetAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		rows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		a, err := db.ScanAuthor(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// @Summary Replace an author
// @Tags authors
// @Accept json
// @Param slug path string true "Author slug"
// @Param body body models.AuthorInput true "Author"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [put]
func PutAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		var body models.AuthorInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		nextSlug := slug
		if body.Slug != "" {
			nextSlug = strings.TrimSpace(body.Slug)
			if !isValidCanonicalSlug(nextSlug) {
				writeError(w, http.StatusBadRequest, "slug must be canonical")
				return
			}
		}
		result, err := db.Update(r.Context(), conn, "authors",
			[]string{"display_name", "first_name", "last_name", "email", "login"},
			"`login` = ? AND `archived_at` IS NULL",
			body.DisplayName, body.FirstName, body.LastName, body.Email, nextSlug, slug,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_updated", body.DisplayName, "old_slug", slug, "new_slug", nextSlug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Partially update an author
// @Tags authors
// @Accept json
// @Param slug path string true "Author slug"
// @Param body body models.AuthorPatch true "Fields to update"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [patch]
func PatchAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var setCols []string
		var setArgs []any
		for _, col := range []string{"display_name", "first_name", "last_name", "email"} {
			if v, ok := body[col]; ok {
				setCols = append(setCols, col)
				setArgs = append(setArgs, v)
			}
		}
		if v, ok := body["slug"]; ok {
			rawSlug, ok := v.(string)
			if !ok {
				writeError(w, http.StatusBadRequest, "slug must be a string")
				return
			}
			trimmed := strings.TrimSpace(rawSlug)
			if !isValidCanonicalSlug(trimmed) {
				writeError(w, http.StatusBadRequest, "slug must be canonical")
				return
			}
			setCols = append(setCols, "login")
			setArgs = append(setArgs, trimmed)
		}
		if len(setCols) == 0 {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		result, err := db.Update(r.Context(), conn, "authors", setCols, "`login` = ? AND `archived_at` IS NULL", append(setArgs, slug)...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		target := slug
		if rawSlug, ok := body["slug"].(string); ok && strings.TrimSpace(rawSlug) != "" {
			target = strings.TrimSpace(rawSlug)
		}
		if displayName, ok := body["display_name"].(string); ok && strings.TrimSpace(displayName) != "" {
			target = strings.TrimSpace(displayName)
		}
		activity.LogRequest(r, "author_updated", target, "slug", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Delete an author
// @Tags authors
// @Param slug path string true "Author slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug} [delete]
func DeleteAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `authors` SET `archived_at` = NOW() WHERE `login` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_deleted", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Restore an archived author
// @Tags authors
// @Param slug path string true "Author slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/authors/{slug}/restore [patch]
func RestoreAuthor(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `authors` SET `archived_at` = NULL WHERE `login` = ? AND `archived_at` IS NOT NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		activity.LogRequest(r, "author_restored", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary List articles by author
// @Tags authors
// @Produce json
// @Param slug path string true "Author slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Param section_slug query string false "Filter by section"
// @Param subsection_slug query string false "Filter by subsection"
// @Success 200 {object} models.AuthorArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/authors/{slug}/articles [get]
func GetAuthorArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		authorRows, err := db.Select(r.Context(), conn, "authors", db.AuthorColumns, "`login` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer authorRows.Close()
		if !authorRows.Next() {
			writeError(w, http.StatusNotFound, "author not found")
			return
		}
		author, err := db.ScanAuthor(authorRows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		params, err := normalizeAndValidateArticleParams(r.Context(), conn, ArticleParams{
			AuthorSlug: slug,
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		page, limit, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		totalCount, err := countArticles(r, conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := db.CollectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(articles) > limit
		if hasMore {
			articles = articles[:limit]
		}

		writeJSON(w, http.StatusOK, models.AuthorArticlesResponse{
			Author: models.AuthorArticlesAuthor{
				ID:          author.ID,
				Slug:        author.Slug,
				DisplayName: author.DisplayName,
			},
			Articles:   articleListItems(articles, excerptWords),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary List articles
// @Tags articles
// @Produce json
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Param author_slug query string false "Filter by author slug"
// @Param author query string false "Filter by author display name or slug (partial match)"
// @Param section_slug query string false "Filter by section slug"
// @Param subsection_slug query string false "Filter by subsection slug"
// @Param status query string false "Filter by status. Ignored for unauthenticated callers, who always get published only." Enums(draft,published)
// @Param archived query bool false "When true, return only soft-deleted articles. Ignored for unauthenticated callers."
// @Param title query string false "Filter by title (partial match)"
// @Param slug query string false "Filter by slug (partial match)"
// @Param sort_by query string false "Sort field" Enums(title,slug,creation_date,published_date,status,comment_status)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} models.ArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles [get]
func GetArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(r.Context(), conn, ArticleParams{
			AuthorSlug:   r.URL.Query().Get("author_slug"),
			AuthorSearch: r.URL.Query().Get("author"),
			Section:      r.URL.Query().Get("section_slug"),
			Subsection:   r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, limit, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		totalCount, err := countArticles(r, conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := db.CollectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(articles) > limit
		if hasMore {
			articles = articles[:limit]
		}

		writeJSON(w, http.StatusOK, models.ArticlesResponse{
			Articles:   articleListItems(articles, excerptWords, categoryPreferenceSlugs(params)...),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary List articles by section
// @Tags articles
// @Produce json
// @Param section_slug path string true "Section slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.SectionArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/sections/{section_slug}/articles [get]
func GetSectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(r.Context(), conn, ArticleParams{
			AuthorSlug: r.URL.Query().Get("author_slug"),
			Section:    r.PathValue("section_slug"),
			Subsection: r.URL.Query().Get("subsection_slug"),
		})
		if err != nil {
			writeError(w, articleParamsStatus(err, errSectionNotFound), err.Error())
			return
		}
		page, limit, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		totalCount, err := countArticles(r, conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := db.CollectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(articles) > limit
		if hasMore {
			articles = articles[:limit]
		}

		sectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "section", params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		subsections, err := subsectionsForSection(r.Context(), conn, params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, models.SectionArticlesResponse{
			Section: models.TaxonomySummary{
				Slug:           params.Section,
				Name:           sectionCanonicalTitle,
				CanonicalTitle: sectionCanonicalTitle,
			},
			Subsections: subsections,
			Articles:    articleListItems(articles, excerptWords, categoryPreferenceSlugs(params)...),
			Pagination:  paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

// @Summary List articles by subsection
// @Tags articles
// @Produce json
// @Param subsection_slug path string true "Subsection slug"
// @Param section_slug query string false "Parent section slug"
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.SubsectionArticlesResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/subsections/{subsection_slug}/articles [get]
func GetSubsectionArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := normalizeAndValidateArticleParams(r.Context(), conn, ArticleParams{
			AuthorSlug: r.URL.Query().Get("author_slug"),
			Section:    r.URL.Query().Get("section_slug"),
			Subsection: r.PathValue("subsection_slug"),
		})
		if err != nil {
			writeError(w, articleParamsStatus(err, errSubsectionNotFound), err.Error())
			return
		}
		page, limit, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		totalCount, err := countArticles(r, conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows, err := queryArticles(r, conn, params, limit+1, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		articles, err := db.CollectArticles(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(articles) > limit
		if hasMore {
			articles = articles[:limit]
		}

		sectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "section", params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		subsectionCanonicalTitle, err := canonicalTitleForTaxonomy(r.Context(), conn, "subsection", params.Subsection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, models.SubsectionArticlesResponse{
			Section: models.TaxonomySummary{
				Slug:           params.Section,
				Name:           sectionCanonicalTitle,
				CanonicalTitle: sectionCanonicalTitle,
			},
			Subsection: models.TaxonomySummary{
				Slug:           params.Subsection,
				Name:           subsectionCanonicalTitle,
				CanonicalTitle: subsectionCanonicalTitle,
			},
			Articles:   articleListItems(articles, excerptWords, categoryPreferenceSlugs(params)...),
			Pagination: paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

type ArticleParams struct {
	AuthorSlug   string
	AuthorSearch string
	Section      string
	Subsection   string
	// SectionMatchSlugs is the section plus its subsections, resolved during
	// validation because the filter builder has no database handle. A section
	// with no matching category of its own is still populated by its children.
	SectionMatchSlugs []string
}

func queryArticles(r *http.Request, conn *sql.DB, params ArticleParams, limit, offset int) (*sql.Rows, error) {
	q := r.URL.Query()
	conditions, args := articleQueryFilters(r, params)
	query := "SELECT `id`, `title`, `slug`, `description`, `text`, `excerpt`, `tags`, `categories`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url`, `focus_keyword`, `meta_description`, `seo_title`, `creation_date`, `scheduled_pub_date`, `canonical_url`, `noindex`, `photo_alt` FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	sortBy := q.Get("sort_by")
	if sortBy == "" {
		query += defaultArticleOrderBy(r)
	}
	if clause := articleOrderByClause(r, sortBy, q.Get("sort_direction")); clause != "" {
		// Editor date sort is handled here; hand BuildOrderLimit an empty sort_by
		// so it only appends LIMIT/OFFSET and can't emit a second ORDER BY.
		query += clause
		sortBy = ""
	}
	query = db.BuildOrderLimit(query, sortBy, q.Get("sort_direction"), db.ArticleSortByColumn, limit, offset)

	return conn.QueryContext(r.Context(), query, args...)
}

// defaultArticleOrderBy is the ORDER BY for a listing that asked for no
// particular sort, which is every public read: the homepage blocks, the section
// pages and an unadorned /v1/articles.
//
// Public callers get newest-published-first. Ordering on `id` made placement
// depend on insert order instead, so an article drafted before an ETL reseed --
// which loads the legacy archive at ids far above anything the CMS has issued --
// sorted below ten thousand archive rows and never reached the homepage despite
// being published that morning. `id` stays on as the tiebreak, because a whole
// issue is published on one timestamp and needs a stable order within it.
//
// Editors keep `id` DESC. Their listing includes drafts, whose `pub_date` is
// NULL and would sort to the very end, burying a new draft on the last page of
// a 10k-article list -- the same reason articleOrderByClause exists for their
// explicit date sorts.
func defaultArticleOrderBy(r *http.Request) string {
	if _, isEditor := middleware.UserFromContext(r.Context()); isEditor {
		return " ORDER BY `id` DESC"
	}
	return " ORDER BY `pub_date` DESC, `id` DESC"
}

// articleOrderByClause returns the editor-only ORDER BY for date sorts, or ""
// to let BuildOrderLimit handle it. Sorting the CMS listing on `pub_date` puts
// drafts (NULL pub_date) at the very end, so a new draft lands on the last page
// of a 9k-article list instead of at the top. Editors sort on the date the row
// actually has — published date, else creation date — which interleaves drafts
// with published articles.
func articleOrderByClause(r *http.Request, sortBy, sortDir string) string {
	if _, isEditor := middleware.UserFromContext(r.Context()); !isEditor {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case string(models.ArticleSortByPublishedAt), string(models.ArticleSortByCreatedAt):
	default:
		return ""
	}

	dir := "ASC"
	if strings.EqualFold(strings.TrimSpace(sortDir), string(models.SortDirectionDescending)) {
		dir = "DESC"
	}
	return " ORDER BY COALESCE(`pub_date`, `creation_date`) " + dir + ", `id` " + dir
}

func countArticles(r *http.Request, conn *sql.DB, params ArticleParams) (int, error) {
	conditions, args := articleQueryFilters(r, params)
	query := "SELECT COUNT(*) FROM `articles`"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var totalCount int
	if err := conn.QueryRowContext(r.Context(), query, args...).Scan(&totalCount); err != nil {
		return 0, err
	}
	return totalCount, nil
}

func articleQueryFilters(r *http.Request, params ArticleParams) ([]string, []any) {
	q := r.URL.Query()
	var conditions []string
	var args []any

	_, isEditor := middleware.UserFromContext(r.Context())

	// Exclude media/import artifacts that have neither authors nor categories.
	// A brand-new CMS draft looks exactly like one of those until an author or a
	// category is attached, so editors keep unpublished rows regardless — without
	// the exemption a draft is invisible in the CMS listing until it is filed.
	artifactFilter := "((TRIM(COALESCE(`authors`, '')) <> '' AND TRIM(`authors`) <> '[]') OR (TRIM(COALESCE(`categories`, '')) <> '' AND TRIM(`categories`) <> '[]'))"
	if isEditor {
		artifactFilter = "(" + artifactFilter + " OR `pub_date` IS NULL)"
	}
	conditions = append(conditions, artifactFilter)

	// /v1/articles is public, so an anonymous caller is pinned to the published,
	// non-archived view and the `archived`/`status` query params are ignored for
	// them. Without this, anyone could enumerate unpublished drafts
	// (?status=draft) or soft-deleted articles (?archived=true) — the listing is
	// excerpt-only, but unpublished headlines still must not leak. Editors are
	// identified by OptionalAuth on the route and keep the full filter set.
	if !isEditor {
		conditions = append(conditions, "`pub_date` IS NOT NULL", "`pub_date` <= UTC_TIMESTAMP()", "`archived_at` IS NULL")
	} else if _, archivedProvided := q["archived"]; archivedProvided {
		archivedRaw := strings.ToLower(strings.TrimSpace(q.Get("archived")))
		switch archivedRaw {
		case "", "1", "true", "yes":
			conditions = append(conditions, "`archived_at` IS NOT NULL")
		case "0", "false", "no":
			conditions = append(conditions, "`archived_at` IS NULL")
		default:
			conditions = append(conditions, "`archived_at` IS NOT NULL")
		}
	} else {
		conditions = append(conditions, "`archived_at` IS NULL")
	}

	if params.AuthorSlug != "" {
		conditions = append(conditions, "`id` IN (SELECT aa.`articles_id` FROM `articles_authors` aa JOIN `authors` au ON au.`id` = aa.`author_id` WHERE au.`login` = ? AND au.`archived_at` IS NULL)")
		args = append(args, params.AuthorSlug)
	} else if params.AuthorSearch != "" {
		like := "%" + strings.ToLower(params.AuthorSearch) + "%"
		conditions = append(conditions, "`id` IN (SELECT aa.`articles_id` FROM `articles_authors` aa JOIN `authors` au ON au.`id` = aa.`author_id` WHERE au.`archived_at` IS NULL AND (LOWER(au.`display_name`) LIKE ? OR LOWER(au.`login`) LIKE ?))")
		args = append(args, like, like)
	}

	// A subsection stands on its own: ANDing it with its parent returns the
	// intersection, which is empty whenever the parent slug is not itself a
	// real category. That is how /special-editions/welcome-week served 0
	// articles while 30 were filed under "Welcome Week".
	if params.Subsection != "" {
		appendCategorySlugCondition(&conditions, &args, params.Subsection)
	} else if params.Section != "" {
		appendCategorySlugCondition(&conditions, &args, params.SectionMatchSlugs...)
	}

	if status := strings.TrimSpace(q.Get("status")); status != "" && isEditor {
		switch strings.ToLower(status) {
		case string(models.ArticleStatusDraft):
			conditions = append(conditions, "`pub_date` IS NULL", "`scheduled_pub_date` IS NULL")
		case string(models.ArticleStatusScheduled):
			conditions = append(conditions, "`pub_date` IS NULL", "`scheduled_pub_date` IS NOT NULL")
		case string(models.ArticleStatusPublished):
			conditions = append(conditions, "`pub_date` IS NOT NULL")
		}
	}

	// Oldest-first on pub_date would otherwise lead with the NULL block. Editors
	// sort on COALESCE(pub_date, creation_date) (see articleOrderByClause), so
	// their drafts are already placed by date and must not be filtered out here.
	if !isEditor &&
		strings.EqualFold(strings.TrimSpace(q.Get("sort_by")), string(models.ArticleSortByPublishedAt)) &&
		strings.EqualFold(strings.TrimSpace(q.Get("sort_direction")), string(models.SortDirectionAscending)) {
		conditions = append(conditions, "`pub_date` IS NOT NULL")
	}

	if title := strings.TrimSpace(q.Get("title")); title != "" {
		conditions = append(conditions, "`title` LIKE ?")
		args = append(args, "%"+title+"%")
	}

	if articleType := strings.TrimSpace(q.Get("type")); articleType != "" {
		appendArticleTypeCondition(&conditions, &args, articleType)
	}
	if excludedType := strings.TrimSpace(q.Get("exclude_type")); excludedType != "" {
		appendArticleTypeCondition(&conditions, &args, excludedType, true)
	}

	if pub_date := db.ParsePublishedAt(q.Get("published_date")); pub_date != nil {
		conditions = append(conditions, "`pub_date` >= ?")
		args = append(args, pub_date.UTC().Format("2006-01-02 15:04:05"))
	}

	if creation_date := db.ParsePublishedAt(q.Get("creation_date")); creation_date != nil {
		conditions = append(conditions, "`creation_date` >= ?")
		args = append(args, creation_date.UTC().Format("2006-01-02 15:04:05"))
	}

	if slug := strings.TrimSpace(q.Get("slug")); slug != "" {
		conditions = append(conditions, "`slug` LIKE ?")
		args = append(args, "%"+slug+"%")
	}

	return conditions, args
}

// appendCategorySlugCondition narrows to articles filed under any of the given
// slugs. Patterns come from db.CategoryMatchPatterns so the listing and the
// taxonomy counts can never drift apart.
func appendCategorySlugCondition(conditions *[]string, args *[]any, slugs ...string) {
	condition, condArgs := db.TaxonomyCountCondition(slugs)
	if condition == "" {
		return
	}
	*conditions = append(*conditions, condition)
	*args = append(*args, condArgs...)
}

func appendArticleTypeCondition(conditions *[]string, args *[]any, rawType string, negate ...bool) {
	normalized := strings.ToLower(strings.TrimSpace(rawType))
	if normalized == "" {
		return
	}

	patterns := make([]string, 0, 4)
	addPattern := func(value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		for _, existing := range patterns {
			if existing == value {
				return
			}
		}
		patterns = append(patterns, value)
	}

	addPattern("%" + normalized + "%")
	addPattern("%" + strings.ReplaceAll(normalized, "-", " ") + "%")
	addPattern("%" + strings.ReplaceAll(normalized, "_", " ") + "%")

	clauseParts := make([]string, 0, len(patterns)*3)
	for _, pattern := range patterns {
		clauseParts = append(clauseParts, "LOWER(`categories`) LIKE ?")
		*args = append(*args, pattern)
		clauseParts = append(clauseParts, "LOWER(`tags`) LIKE ?")
		*args = append(*args, pattern)
		clauseParts = append(clauseParts, "LOWER(COALESCE(`metadata`, '')) LIKE ?")
		*args = append(*args, pattern)
	}

	clause := "(" + strings.Join(clauseParts, " OR ") + ")"
	if len(negate) > 0 && negate[0] {
		clause = "NOT " + clause
	}
	*conditions = append(*conditions, clause)
}

// @Summary Search articles
// @Tags articles
// @Produce json
// @Param q query string true "Search term"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Success 200 {array} models.ArticleListItem
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/search [get]
// QueryEmbedder turns a search query into a vector. It is an interface, and a
// nil one is valid: a deployment without the embedding sidecar serves lexical
// search through the same handler.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
}

func GetSearch(conn *sql.DB, embedder QueryEmbedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusOK, []any{})
			return
		}

		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)

		// The sidecar is on the request path here, so its failures must not be.
		// A timeout, a cold model, or no sidecar at all costs the semantic half of
		// the ranking; it never costs the reader their results.
		var queryVector []float32
		if embedder != nil {
			vector, err := embedder.EmbedQuery(r.Context(), q)
			if err != nil {
				slog.WarnContext(r.Context(), "query embedding unavailable; serving lexical search", "error", err)
			} else {
				queryVector = vector
			}
		}

		articles, err := db.SearchArticlesHybrid(r.Context(), conn, q, queryVector, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.ArticleListItem, 0, len(articles))
		for _, article := range articles {
			categories := make([]models.CategorySummary, 0, len(article.Categories))
			for _, category := range article.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				categories = append(categories, models.CategorySummary{Name: name, Slug: db.CategoryLinkSlug(name)})
			}

			authors := make([]models.AuthorSummary, 0, len(article.Authors))
			for _, author := range article.Authors {
				authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
			}

			item := models.ArticleListItem{
				Title:         article.Title,
				ID:            article.ID,
				Authors:       authors,
				Categories:    categories,
				Excerpt:       article.Excerpt,
				Slug:          article.Slug,
				Status:        article.Status,
				CommentStatus: article.CommentStatus,
				FeaturedImage: article.PhotoURL,
				IsFeatured:    article.IsFeatured,
				BreakingNews:  article.BreakingNews,
			}
			item.PublishedDate = article.PublishedAt
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// articleDetailCondition builds the WHERE clause for a single-article lookup.
// The endpoint is public and returns the FULL article body, so an anonymous
// caller is restricted to live content: knowing or guessing a slug must not
// hand out an unpublished draft or a soft-deleted article. A miss falls through
// to the same 404 as a nonexistent slug, so the response never reveals that the
// article exists. Editors are identified by OptionalAuth on the route and still
// see everything.
func articleDetailCondition(r *http.Request) string {
	if _, isEditor := middleware.UserFromContext(r.Context()); isEditor {
		return "`slug` = ?"
	}
	return "`slug` = ? AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL"
}

// @Summary Get an article by slug
// @Description Public. Unauthenticated callers only see published, non-archived articles; anything else answers 404, the same as an unknown slug. An authenticated editor sees drafts and archived articles too.
// @Tags articles
// @Produce json
// @Param slug path string true "Article slug"
// @Success 200 {object} models.ArticleDetailResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug} [get]
func GetArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		// This endpoint is public and returns the FULL article body, so an
		// anonymous caller is restricted to live content: knowing or guessing a
		// slug must not hand out an unpublished draft or a soft-deleted article.
		// The miss falls through to the same 404 as a nonexistent slug, so the
		// response does not reveal that the article exists at all. Editors are
		// identified by OptionalAuth on the route and still see everything.
		rows, err := db.Select(r.Context(), conn, "articles", db.ArticleColumns, articleDetailCondition(r), slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		if !rows.Next() {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		a, err := db.ScanArticle(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		articles := []models.Article{a}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a = articles[0]
		categories := make([]models.CategorySummary, 0, len(a.Categories))
		for _, category := range a.Categories {
			name := strings.TrimSpace(category)
			if name == "" {
				continue
			}
			categories = append(categories, models.CategorySummary{Name: name, Slug: db.CategoryLinkSlug(name)})
		}

		seoTags := make([]models.CategorySummary, 0, len(a.Tags))
		for _, tag := range a.Tags {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			seoTags = append(seoTags, models.CategorySummary{Name: name, Slug: db.CanonicalizeSlug(name)})
		}

		authors := make([]models.AuthorSummary, 0, len(a.Authors))
		for _, author := range a.Authors {
			authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
		}

		relatedArticles, err := db.GetRelatedArticlesBySlug(r.Context(), conn, a.Slug, 3)
		if err != nil {
			if !db.IsVectorSearchUnavailableError(err) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			relatedArticles = []models.Article{}
		}

		related := make([]models.ArticleListItem, 0, len(relatedArticles))
		for _, relatedArticle := range relatedArticles {
			relatedCategories := make([]models.CategorySummary, 0, len(relatedArticle.Categories))
			for _, category := range relatedArticle.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				relatedCategories = append(relatedCategories, models.CategorySummary{Name: name, Slug: db.CategoryLinkSlug(name)})
			}

			relatedAuthors := make([]models.AuthorSummary, 0, len(relatedArticle.Authors))
			for _, author := range relatedArticle.Authors {
				relatedAuthors = append(relatedAuthors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
			}

			relatedItem := models.ArticleListItem{
				Title:         relatedArticle.Title,
				ID:            relatedArticle.ID,
				Authors:       relatedAuthors,
				Categories:    relatedCategories,
				Excerpt:       relatedArticle.Excerpt,
				Slug:          relatedArticle.Slug,
				Status:        relatedArticle.Status,
				CommentStatus: relatedArticle.CommentStatus,
				FeaturedImage: relatedArticle.PhotoURL,
				IsFeatured:    relatedArticle.IsFeatured,
				BreakingNews:  relatedArticle.BreakingNews,
			}
			relatedItem.PublishedDate = relatedArticle.PublishedAt
			related = append(related, relatedItem)
		}

		resp := models.ArticleDetailResponse{
			ID:               a.ID,
			Title:            a.Title,
			Slug:             a.Slug,
			Content:          a.Content,
			Excerpt:          a.Excerpt,
			Categories:       categories,
			CommentStatus:    a.CommentStatus,
			IsFeatured:       a.IsFeatured,
			BreakingNews:     a.BreakingNews,
			Status:           a.Status,
			FeaturedImage:    a.PhotoURL,
			FeaturedImageAlt: a.PhotoAlt,
			Authors:          authors,
			SEO: models.SEOResponse{
				SEOTitle:        a.SEOTitle,
				MetaDescription: a.MetaDescription,
				FocusKeyword:    a.FocusKeyword,
				CanonicalURL:    a.CanonicalURL,
				NoIndex:         a.NoIndex,
				Tags:            seoTags,
			},
			Related: related,
		}
		resp.PublishedDate = a.PublishedAt
		resp.ModifiedDate = a.ModifiedAt

		writeJSON(w, http.StatusOK, resp)
	}
}

// @Summary List approved comments for an article
// @Tags articles
// @Produce json
// @Param slug path string true "Article slug"
// @Success 200 {object} models.ArticleCommentsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug}/comments [get]
func GetArticleComments(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		exists, err := db.ArticleExistsBySlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}

		comments, err := db.GetApprovedCommentsByArticleSlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.CommentResponse, 0, len(comments))
		for _, comment := range comments {
			resp = append(resp, models.CommentResponse{
				ID:           comment.ID,
				ArticleID:    comment.ArticleID,
				ParentID:     comment.ParentID,
				AuthorName:   comment.AuthorName,
				AuthorURL:    comment.AuthorURL,
				Content:      comment.Content,
				CreatedAt:    comment.CreatedAt,
				CreatedAtGMT: comment.CreatedAtGMT,
				Status:       comment.Status,
				Type:         comment.Type,
			})
		}

		writeJSON(w, http.StatusOK, models.ArticleCommentsResponse{
			ArticleSlug: slug,
			Comments:    resp,
			TotalCount:  len(resp),
		})
	}
}

// @Summary Submit a comment for an article
// @Tags articles
// @Accept json
// @Produce json
// @Param slug path string true "Article slug"
// @Param body body models.CommentInput true "Comment"
// @Success 201 {object} models.CommentResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/articles/{slug}/comments [post]
func PostArticleComment(conn *sql.DB, spamChecker akismet.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
		var body models.CommentInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		body.AuthorName = strings.TrimSpace(body.AuthorName)
		body.AuthorEmail = strings.TrimSpace(body.AuthorEmail)
		body.AuthorURL = strings.TrimSpace(body.AuthorURL)
		body.Content = strings.TrimSpace(body.Content)

		if body.AuthorName == "" {
			writeError(w, http.StatusBadRequest, "author_name is required")
			return
		}
		if len([]rune(body.AuthorName)) > 80 {
			writeError(w, http.StatusBadRequest, "author_name must be 80 characters or fewer")
			return
		}
		if body.Content == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}
		if len([]rune(body.Content)) > 5000 {
			writeError(w, http.StatusBadRequest, "content must be 5000 characters or fewer")
			return
		}
		if body.AuthorEmail != "" && (len(body.AuthorEmail) > 254 || !strings.Contains(body.AuthorEmail, "@")) {
			writeError(w, http.StatusBadRequest, "author_email must be a valid email address")
			return
		}
		if body.AuthorURL != "" {
			parsedURL, err := url.ParseRequestURI(body.AuthorURL)
			if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
				writeError(w, http.StatusBadRequest, "author_url must be an http or https URL")
				return
			}
		}
		if body.ParentID < 0 {
			writeError(w, http.StatusBadRequest, "parent_id must be positive")
			return
		}

		articleID, commentStatus, exists, err := db.GetArticleCommentTargetBySlug(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if articleCommentsClosed(commentStatus) {
			writeError(w, http.StatusForbidden, "comments are closed for this article")
			return
		}

		parentCanAcceptReply, err := db.CommentCanAcceptReply(r.Context(), conn, articleID, body.ParentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !parentCanAcceptReply {
			writeError(w, http.StatusBadRequest, "parent comment not found")
			return
		}

		now := time.Now().UTC()
		status := "pending"
		if spamChecker != nil {
			status = "approved"
			isSpam, err := spamChecker.CheckComment(r.Context(), akismet.Comment{
				UserIP:      clientIP(r),
				UserAgent:   r.UserAgent(),
				Referrer:    r.Referer(),
				Permalink:   articlePermalink(r, slug),
				Type:        "comment",
				Author:      body.AuthorName,
				AuthorEmail: body.AuthorEmail,
				AuthorURL:   body.AuthorURL,
				Content:     body.Content,
				CreatedAt:   now,
			})
			if err != nil {
				status = "pending"
				if akismet.IsConfigError(err) {
					// Nothing gets filtered until an operator fixes the key or
					// blog URL, so this is a fault, not a hiccup.
					slog.Error("akismet is misconfigured; spam filtering is not running", "article_slug", slug, "error", err)
				} else {
					slog.Warn("akismet comment check failed; comment requires moderation", "article_slug", slug, "error", err)
				}
			} else if isSpam {
				status = "spam"
			}
		}

		comment, err := db.CreateComment(r.Context(), conn, db.CreateCommentParams{
			ArticleID:   articleID,
			ParentID:    body.ParentID,
			AuthorName:  body.AuthorName,
			AuthorEmail: body.AuthorEmail,
			AuthorURL:   body.AuthorURL,
			AuthorIP:    clientIP(r),
			Content:     body.Content,
			Status:      status,
			Type:        "comment",
			CreatedAt:   now,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, models.CommentResponse{
			ID:           comment.ID,
			ArticleID:    comment.ArticleID,
			ParentID:     comment.ParentID,
			AuthorName:   comment.AuthorName,
			AuthorURL:    comment.AuthorURL,
			Content:      comment.Content,
			CreatedAt:    comment.CreatedAt,
			CreatedAtGMT: comment.CreatedAtGMT,
			Status:       comment.Status,
			Type:         comment.Type,
		})
	}
}

func articleCommentsClosed(commentStatus string) bool {
	return strings.EqualFold(strings.TrimSpace(commentStatus), "closed")
}

func articlePermalink(r *http.Request, slug string) string {
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
		if scheme == "" {
			scheme = "https"
		}
		return scheme + "://" + forwardedHost + "/article/" + slug
	}

	if r.Host != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + r.Host + "/article/" + slug
	}

	return "/article/" + slug
}

func adminCommentResponse(comment db.AdminComment) models.AdminCommentResponse {
	return models.AdminCommentResponse{
		ID:           comment.ID,
		ArticleID:    comment.ArticleID,
		ArticleTitle: comment.ArticleTitle,
		ArticleSlug:  comment.ArticleSlug,
		ParentID:     comment.ParentID,
		AuthorName:   comment.AuthorName,
		AuthorEmail:  comment.AuthorEmail,
		AuthorURL:    comment.AuthorURL,
		Content:      comment.Content,
		CreatedAt:    comment.CreatedAt,
		CreatedAtGMT: comment.CreatedAtGMT,
		Status:       comment.Status,
		Type:         comment.Type,
	}
}

// @Summary List comments for CMS moderation
// @Tags comments
// @Produce json
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(25)
// @Param offset query int false "Offset"
// @Param status query string false "Filter by status" Enums(all,pending,approved,spam,trash)
// @Param search query string false "Search comment content, author, email, or article"
// @Success 200 {object} models.AdminCommentsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments [get]
func GetComments(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := listParams(r, 25)
		status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		if status != "" && status != "all" && !db.ValidCommentStatuses[status] {
			writeError(w, http.StatusBadRequest, "status must be one of: all, pending, approved, spam, trash")
			return
		}

		comments, totalCount, counts, err := db.ListAdminComments(
			r.Context(),
			conn,
			limit,
			offset,
			status,
			r.URL.Query().Get("search"),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.AdminCommentResponse, 0, len(comments))
		for _, comment := range comments {
			resp = append(resp, adminCommentResponse(comment))
		}

		writeJSON(w, http.StatusOK, models.AdminCommentsResponse{
			Comments:   resp,
			Pagination: paginationResponse(page, limit, offset, offset+len(comments) < totalCount, totalCount),
			Counts:     counts,
		})
	}
}

// @Summary Update a comment status
// @Tags comments
// @Accept json
// @Produce json
// @Param id path int true "Comment ID"
// @Param body body models.CommentStatusPatchRequest true "Status update"
// @Success 200 {object} models.AdminCommentResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments/{id} [patch]
func PatchComment(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		var body models.CommentStatusPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		status := strings.ToLower(strings.TrimSpace(body.Status))
		if !db.ValidCommentStatuses[status] {
			writeError(w, http.StatusBadRequest, "status must be one of: pending, approved, spam, trash")
			return
		}

		comment, err := db.UpdateCommentStatus(r.Context(), conn, id, status)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		activity.LogRequest(r, "comment_status_updated", fmt.Sprintf("Comment #%d", id), "status", status)
		writeJSON(w, http.StatusOK, adminCommentResponse(comment))
	}
}

// @Summary Delete a comment
// @Tags comments
// @Param id path int true "Comment ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/comments/{id} [delete]
func DeleteComment(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		if err := db.DeleteComment(r.Context(), conn, id); err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		activity.LogRequest(r, "comment_deleted", fmt.Sprintf("Comment #%d", id))
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Create an article
// @Tags articles
// @Accept json
// @Param body body models.ArticleInput true "Article"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles [post]
func PostArticles(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireArticleWriteRole(w, r) {
			return
		}
		var body models.ArticleInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if rawSlug := strings.TrimSpace(body.Slug); rawSlug != "" && !isValidCanonicalSlug(rawSlug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if strings.TrimSpace(body.PublishedDate) != "" && db.ParsePublishedAt(body.PublishedDate) == nil {
			writeError(w, http.StatusBadRequest, "published_date has invalid format")
			return
		}
		if _, ok := db.NormalizeCanonicalURL(body.CanonicalURL); !ok {
			writeError(w, http.StatusBadRequest, "canonical_url must be an absolute http(s) URL")
			return
		}
		fields := db.ArticleInputToDBFields(body)
		result, err := db.Insert(r.Context(), conn, "articles",
			[]string{"title", "slug", "description", "text", "excerpt", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url", "tags", "metadata", "focus_keyword", "meta_description", "seo_title", "creation_date", "scheduled_pub_date", "canonical_url", "noindex", "photo_alt"},
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		articleID, err := result.LastInsertId()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// See PatchArticle: featuring is exclusive.
		if body.IsFeatured {
			if err := db.ClearFeaturedExceptID(r.Context(), conn, articleID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := db.ReplaceArticleAuthors(r.Context(), conn, articleID, body.Authors); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := incrementTaxonomyArticleCounts(r.Context(), conn, body.Categories); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		action := "article_created"
		if body.Status == models.ArticleStatusPublished {
			action = "article_published"
		}
		activity.LogRequest(r, action, body.Title, "slug", strings.TrimSpace(body.Slug), "article_id", articleID)
		w.WriteHeader(http.StatusCreated)
	}
}

// @Summary Replace an article
// @Tags articles
// @Accept json
// @Param slug path string true "Article slug"
// @Param body body models.Article true "Article"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [put]
func PutArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		unlock := articleEditLocks.Lock(slug)
		defer unlock()
		if !requireArticleWriteRole(w, r) {
			return
		}
		var body models.Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.Slug) == "" {
			body.Slug = slug
		} else if !isValidCanonicalSlug(body.Slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if _, ok := db.NormalizeCanonicalURL(body.CanonicalURL); !ok {
			writeError(w, http.StatusBadRequest, "canonical_url must be an absolute http(s) URL")
			return
		}
		oldCategories, isActiveArticle, err := loadArticleCategoriesByArchiveState(r.Context(), conn, slug, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		fields := db.ArticleToDBFields(body)
		fields = append(fields, slug)
		result, err := db.Update(r.Context(), conn, "articles",
			[]string{"title", "slug", "excerpt", "text", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url", "focus_keyword", "meta_description", "seo_title", "scheduled_pub_date", "canonical_url", "noindex", "photo_alt"},
			"`slug` = ?",
			fields...,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if rowsAffected == 0 {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		// See PatchArticle: featuring is exclusive, so the previous pick is
		// cleared once this one is safely written.
		if body.IsFeatured {
			if err := db.ClearFeaturedExcept(r.Context(), conn, body.Slug); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := db.ReplaceArticleAuthorsBySlug(r.Context(), conn, body.Slug, authorIDsFromOverviews(body.Authors)); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if isActiveArticle {
			if err := reconcileTaxonomyArticleCounts(r.Context(), conn, oldCategories, body.Categories); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		activity.LogRequest(r, "article_updated", body.Title, "slug", body.Slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Partially update an article
// @Tags articles
// @Accept json
// @Param slug path string true "Article slug"
// @Param body body models.ArticlePatch true "Fields to update"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [patch]
func PatchArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		unlock := articleEditLocks.Lock(slug)
		defer unlock()
		if !requireArticleWriteRole(w, r) {
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var authorIDs *[]int64
		oldCategories, isActiveArticle, err := loadArticleCategoriesByArchiveState(r.Context(), conn, slug, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		currentPublishedAt, lastPublishedAt, err := loadArticlePublishDate(r.Context(), conn, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nextCategories := oldCategories
		if rawAuthors, ok := body["authors"]; ok {
			parsedIDs, err := parseAuthorIDs(rawAuthors)
			if err != nil {
				writeError(w, http.StatusBadRequest, "authors must be an array of IDs")
				return
			}
			authorIDs = &parsedIDs
		}
		var setCols []string
		var setArgs []any
		columnByJSONField := map[string]string{
			"title":            "title",
			"slug":             "slug",
			"excerpt":          "excerpt",
			"content":          "text",
			"categories":       "categories",
			"tags":             "tags",
			"published_date":   "pub_date",
			"is_featured":      "priority",
			"breaking_news":    "breaking_news",
			"status":           "pub_date",
			"comment_status":   "comment_status",
			"photo_url":        "photo_url",
			"photo_alt":        "photo_alt",
			"focus_keyword":    "focus_keyword",
			"meta_description": "meta_description",
			"seo_title":        "seo_title",
			"canonical_url":    "canonical_url",
			"noindex":          "noindex",
		}
		var publishedDateValue any
		var scheduledDateValue any
		featuring := false
		publishedDateSet := false
		var statusValue models.ArticleStatus
		statusSet := false
		for jsonField, column := range columnByJSONField {
			v, ok := body[jsonField]
			if !ok {
				continue
			}
			switch jsonField {
			case "categories", "tags":
				arr, ok := v.([]any)
				if !ok {
					writeError(w, http.StatusBadRequest, jsonField+" must be an array of strings")
					return
				}
				values := make([]string, 0, len(arr))
				for _, raw := range arr {
					s, ok := raw.(string)
					if !ok {
						writeError(w, http.StatusBadRequest, jsonField+" must be an array of strings")
						return
					}
					values = append(values, s)
				}
				setCols = append(setCols, column)
				formatted := db.FormatTags(values)
				if jsonField == "tags" && formatted == "" {
					formatted = "[]"
				}
				setArgs = append(setArgs, formatted)
				if jsonField == "categories" {
					nextCategories = values
				}
			case "published_date":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "published_date must be an RFC3339 string")
					return
				}
				t := db.ParsePublishedAt(s)
				if t == nil {
					writeError(w, http.StatusBadRequest, "published_date has invalid format")
					return
				}
				if t.After(time.Now().UTC()) {
					publishedDateValue = nil
					scheduledDateValue = t.UTC().Format("2006-01-02 15:04:05")
				} else {
					publishedDateValue = t.UTC().Format("2006-01-02 15:04:05")
					scheduledDateValue = nil
				}
				publishedDateSet = true
			case "status":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "status must be a string")
					return
				}
				status := models.ArticleStatus(strings.TrimSpace(s))
				switch status {
				case models.ArticleStatusDraft:
					statusValue = status
					statusSet = true
				case models.ArticleStatusPublished:
					statusValue = status
					statusSet = true
				default:
					writeError(w, http.StatusBadRequest, "status must be draft or published")
					return
				}
			case "comment_status":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "comment_status must be a string")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, strings.TrimSpace(s))
			case "photo_alt":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "photo_alt must be a string")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, strings.TrimSpace(s))
			case "canonical_url":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "canonical_url must be a string")
					return
				}
				normalized, valid := db.NormalizeCanonicalURL(s)
				if !valid {
					writeError(w, http.StatusBadRequest, "canonical_url must be an absolute http(s) URL")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, normalized)
			case "noindex":
				b, ok := v.(bool)
				if !ok {
					writeError(w, http.StatusBadRequest, "noindex must be a boolean")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, b)
			case "is_featured":
				b, ok := v.(bool)
				if !ok {
					writeError(w, http.StatusBadRequest, "is_featured must be a boolean")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, b)
				featuring = b
			case "slug":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "slug must be a string")
					return
				}
				trimmed := strings.TrimSpace(s)
				if !isValidCanonicalSlug(trimmed) {
					writeError(w, http.StatusBadRequest, "slug must be canonical")
					return
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, trimmed)
			default:
				setCols = append(setCols, column)
				setArgs = append(setArgs, v)
			}
		}
		if dateCols, dateArgs := articlePatchDateColumns(statusSet, statusValue, publishedDateSet, publishedDateValue, scheduledDateValue, currentPublishedAt, lastPublishedAt, time.Now().UTC()); len(dateCols) > 0 {
			setCols = append(setCols, dateCols...)
			setArgs = append(setArgs, dateArgs...)
		}
		if len(setCols) == 0 && authorIDs == nil {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		targetSlug := slug
		setCols = append(setCols, "mod_date")
		setArgs = append(setArgs, time.Now().UTC().Format("2006-01-02 15:04:05"))
		if len(setCols) > 0 {
			result, err := db.Update(r.Context(), conn, "articles", setCols, "`slug` = ?", append(setArgs, slug)...)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if rowsAffected == 0 {
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			if newSlug, ok := body["slug"].(string); ok && strings.TrimSpace(newSlug) != "" {
				targetSlug = strings.TrimSpace(newSlug)
			}
		}
		// Exactly one article is featured at a time: the homepage has one lead
		// card, and leaving the old pick flagged would make the tiebreak, not
		// the editor, decide which story runs.
		if featuring {
			if err := db.ClearFeaturedExcept(r.Context(), conn, targetSlug); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if authorIDs != nil {
			if err := db.ReplaceArticleAuthorsBySlug(r.Context(), conn, targetSlug, *authorIDs); err != nil {
				if err == sql.ErrNoRows {
					writeError(w, http.StatusNotFound, "article not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if isActiveArticle {
			if err := reconcileTaxonomyArticleCounts(r.Context(), conn, oldCategories, nextCategories); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		target := targetSlug
		if rawTitle, ok := body["title"].(string); ok && strings.TrimSpace(rawTitle) != "" {
			target = strings.TrimSpace(rawTitle)
		}
		activity.LogRequest(r, "article_updated", target, "slug", targetSlug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Delete an article
// @Tags articles
// @Param slug path string true "Article slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug} [delete]
func DeleteArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		unlock := articleEditLocks.Lock(slug)
		defer unlock()
		if !requireArticleWriteRole(w, r) {
			return
		}
		categories, found, err := loadArticleCategoriesByArchiveState(r.Context(), conn, slug, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `articles` SET `archived_at` = NOW() WHERE `slug` = ? AND `archived_at` IS NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if err := decrementTaxonomyArticleCounts(r.Context(), conn, categories); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "article_deleted", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// @Summary Restore an archived article
// @Tags articles
// @Param slug path string true "Article slug"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/articles/{slug}/restore [patch]
func RestoreArticle(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.PathValue("slug"))
		if !isValidCanonicalSlug(slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		unlock := articleEditLocks.Lock(slug)
		defer unlock()
		if !requireArticleWriteRole(w, r) {
			return
		}
		categories, found, err := loadArticleCategoriesByArchiveState(r.Context(), conn, slug, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		result, err := conn.ExecContext(r.Context(), "UPDATE `articles` SET `archived_at` = NULL WHERE `slug` = ? AND `archived_at` IS NOT NULL", slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := result.RowsAffected(); n == 0 {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		if err := incrementTaxonomyArticleCounts(r.Context(), conn, categories); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "article_restored", slug)
		w.WriteHeader(http.StatusNoContent)
	}
}

// homepageCacheControl bounds how long a featured-article change can take to
// reach readers. 60s is short enough that an editor swapping the lead sees it
// on the next reload rather than wondering whether the save worked, and long
// enough that the homepage is not re-rendered per visitor.
const homepageCacheControl = "public, max-age=60, stale-while-revalidate=300"

// leadWithFeaturedArticle moves the featured article to the front of the
// homepage news block. It is a no-op when nothing is featured, so the default
// homepage stays newest-first.
//
// The featured article keeps its place in its own section block: a featured
// sports story leads the homepage and still appears in the sports rundown,
// which is what a lead story does in print. Only the news block dedupes, so a
// featured news story is promoted rather than duplicated.
func leadWithFeaturedArticle(r *http.Request, conn *sql.DB, homepage *models.HomepageResponse, excerptWords, newsLimit int, newsMatchSlugs []string) error {
	featured, err := db.GetFeaturedArticle(r.Context(), conn)
	if err != nil || featured == nil {
		return err
	}

	articles := []models.Article{*featured}
	if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
		return err
	}
	items := articleListItems(articles, excerptWords, newsMatchSlugs...)
	if len(items) == 0 {
		return nil
	}
	homepage.News = spliceFeaturedLead(homepage.News, items[0], newsLimit)
	return nil
}

// spliceFeaturedLead puts the featured article at the head of the news block,
// dropping the copy already in the list so a featured news story is promoted
// rather than printed twice. The list is re-trimmed to limit because splicing in
// a story from another section would otherwise push the block one card past the
// layout it was sized for.
func spliceFeaturedLead(news []models.ArticleListItem, featured models.ArticleListItem, limit int) []models.ArticleListItem {
	out := make([]models.ArticleListItem, 0, len(news)+1)
	out = append(out, featured)
	for _, item := range news {
		if item.ID != featured.ID {
			out = append(out, item)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// @Summary Get homepage data
// @Tags homepage
// @Produce json
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.HomepageResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/homepage [get]
func GetHomepage(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sections := [...]struct {
			slug  string
			key   string
			limit int
		}{
			{slug: "news", key: "news", limit: 13},
			{slug: "opinion", key: "opinion", limit: 5},
			{slug: "sports", key: "sports", limit: 6},
			{slug: "entertainment", key: "entertainment", limit: 8},
			{slug: "comics-puzzles", key: "candp", limit: 6},
			{slug: "columns", key: "columns", limit: 5},
		}
		_, _, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		breakingNews, err := db.GetBreakingNews(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		carousel, err := db.GetHomepageCarousel(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		storyTitles, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		developingStories := make([]models.HomepageDevelopingStory, 0, len(storyTitles))
		for idx, title := range storyTitles {
			slug := db.CanonicalizeSlug(title)
			if slug == "" {
				slug = fmt.Sprintf("developing-story-%d", idx+1)
			}
			developingStories = append(developingStories, models.HomepageDevelopingStory{
				Slug:       slug,
				Link:       slug,
				Title:      title,
				Excerpt:    "",
				ShowInNews: false,
				Label:      []models.HomepageLabel{},
			})
		}

		sectionArticles := models.HomepageResponse{
			BreakingNews:      breakingNews,
			Carousel:          db.PublishedHomepageCarousel(carousel),
			DevelopingStories: developingStories,
		}

		// Captured from the news pass so the featured article, which may come
		// from any section, gets the same category ordering as the block it is
		// being spliced into.
		var newsMatchSlugs []string
		newsLimit := 0

		for _, section := range sections {
			if err := func(section struct {
				slug  string
				key   string
				limit int
			}) error {
				// Resolved rather than literal so a homepage block for a
				// container section (one with no category of its own) still
				// shows its subsections' articles.
				matchSlugs, err := sectionMatchSlugs(r.Context(), conn, section.slug)
				if err != nil {
					return err
				}
				params := ArticleParams{Section: section.slug, SectionMatchSlugs: matchSlugs}
				limit := section.limit
				rows, err := queryArticles(r, conn, params, limit+1, offset)
				if err != nil {
					return err
				}
				defer rows.Close()

				articles, err := db.CollectArticles(rows)
				if err != nil {
					return err
				}
				if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
					return err
				}
				hasMore := len(articles) > limit
				if hasMore {
					articles = articles[:limit]
				}
				switch section.key {
				case "news":
					newsMatchSlugs = matchSlugs
					newsLimit = limit
					sectionArticles.News = articleListItems(articles, excerptWords, matchSlugs...)
				case "opinion":
					sectionArticles.Opinion = articleListItems(articles, excerptWords, matchSlugs...)
				case "sports":
					sectionArticles.Sports = articleListItems(articles, excerptWords, matchSlugs...)
				case "entertainment":
					sectionArticles.Entertainment = articleListItems(articles, excerptWords, matchSlugs...)
				case "candp":
					sectionArticles.CAndP = articleListItems(articles, excerptWords, matchSlugs...)
				case "columns":
					sectionArticles.Columns = articleListItems(articles, excerptWords, matchSlugs...)
				}
				return nil
			}(section); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// The homepage lead is the first entry of the news block (Scalene's
		// "3-6-3" layout renders news[0] as the big centre card), so featuring
		// an article means moving it to the front of that list. It is spliced in
		// rather than sorted into the news query because the featured article
		// may be filed under any section -- a featured sports story still takes
		// the lead card, which a news-scoped ORDER BY could never do.
		if offset == 0 {
			if err := leadWithFeaturedArticle(r, conn, &sectionArticles, excerptWords, newsLimit, newsMatchSlugs); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// The homepage carries the editor's live decisions -- the featured lead,
		// the breaking-news banner, the carousel -- so it needs a short, stated
		// freshness bound. Without a Cache-Control header every intermediary
		// picks its own heuristic, and Scalene's fetch cache has no expiry to
		// respect at all, so "featured" could take an unbounded time to appear.
		// stale-while-revalidate keeps that bound cheap: a spike is still served
		// from cache while one request refreshes it behind the scenes.
		w.Header().Set("Cache-Control", homepageCacheControl)
		writeJSON(w, http.StatusOK, sectionArticles)
	}
}
