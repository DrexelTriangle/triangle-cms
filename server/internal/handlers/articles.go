package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"strings"
	"time"
)

func requireArticleWriteRole(w http.ResponseWriter, r *http.Request) bool {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || (user.Role != models.RoleAdmin && user.Role != models.RoleEditor) {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
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

func loadArticleCategoriesByArchiveState(ctx context.Context, conn *sql.DB, target articleTarget, archived bool) ([]string, bool, error) {
	where, args := target.articleWhereArchived(archived)
	query := "SELECT categories FROM articles WHERE " + where + " LIMIT 1"

	var raw sql.NullString
	err := conn.QueryRowContext(ctx, query, args...).Scan(&raw)
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
func loadArticlePublishDate(ctx context.Context, conn *sql.DB, target articleTarget) (publishedAt, lastPublishedAt sql.NullTime, err error) {
	where, args := target.articleWhereArchived(false)
	err = conn.QueryRowContext(ctx,
		"SELECT pub_date, last_pub_date FROM articles WHERE "+where+" LIMIT 1",
		args...,
	).Scan(&publishedAt, &lastPublishedAt)
	if err == sql.ErrNoRows {
		return sql.NullTime{}, sql.NullTime{}, nil
	}
	return publishedAt, lastPublishedAt, err
}

// Saving an article must never move its publish date on its own: the editor
// sends the whole form on every save and every autosave, so any date the handler
// invents here would silently overwrite the real one.
//   - An explicit published_date always wins.
//   - Going to draft clears the live date but parks it in last_pub_date.
//   - Publishing without a date reuses the article's own date, current first
//     and then the parked one, and only falls back to now for something that has
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

// scanOneArticle reads the first row of a single-article query and releases the
// connection before returning, so the caller is free to make its follow-up
// queries. It takes the (rows, err) pair straight from the query call.
func scanOneArticle(rows *sql.Rows, queryErr error) (models.Article, bool, error) {
	if queryErr != nil {
		return models.Article{}, false, queryErr
	}
	defer rows.Close()
	if !rows.Next() {
		return models.Article{}, false, rows.Err()
	}
	a, err := db.ScanArticle(rows)
	if err != nil {
		return models.Article{}, false, err
	}
	return a, true, rows.Close()
}

// articleDetailCondition restricts anonymous reads to published, unarchived rows;
// misses return the same 404 as unknown slugs. Editors can read all statuses.
// Duplicate slugs resolve to the lowest id, matching editor route resolution.
func articleDetailCondition(r *http.Request) string {
	if _, isEditor := middleware.UserFromContext(r.Context()); isEditor {
		return "`slug` = ? ORDER BY `id` LIMIT 1"
	}
	return "`slug` = ? AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL ORDER BY `id` LIMIT 1"
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// This endpoint is public and returns the FULL article body, so an
		// anonymous caller is restricted to live content: knowing or guessing a
		// slug must not hand out an unpublished draft or a soft-deleted article.
		// The miss falls through to the same 404 as a nonexistent slug, so the
		// response does not reveal that the article exists at all. Editors are
		// identified by OptionalAuth on the route and still see everything.
		where := articleDetailCondition(r)
		args := []any{target.slug}
		if _, isEditor := middleware.UserFromContext(r.Context()); isEditor {
			// Resolve even without an ?id=: an editor who followed a legacy
			// /articles/:slug link must load the row a later save will write,
			// and "whichever duplicate the scan reached first" is not that row.
			target, err = resolveArticleTarget(r.Context(), conn, target, false)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if target.hasID {
				where, args = target.articleWhere()
				where += " LIMIT 1"
			}
		}
		// Scanned and closed in one step, before anything else queries. An open
		// *sql.Rows holds its pooled connection, and the author lookup below
		// wants a second one. A handler that kept both would be waiting on a
		// pool it is itself holding, which stalls outright once the pool is the
		// single connection the integration tests use.
		a, found, err := scanOneArticle(db.Select(r.Context(), conn, "articles", db.ArticleColumns, where, args...))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "article not found")
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

		setPublicReadCache(w, r)
		writeJSON(w, http.StatusOK, resp)
	}
}

// @Summary Create an article
// @Tags articles
// @Accept json
// @Description The slug is deduplicated on the way in: a title or slug that another article already uses is stored with a numeric suffix. The response carries the id and the slug that were actually written.
// @Param body body models.ArticleInput true "Article"
// @Success 201 {object} models.ArticleCreateResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
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
		// Two editors filing "Letter from the editor" on the same afternoon is
		// the ordinary case, not the rare one, so the slug is deduped rather
		// than rejected. body.Slug comes back carrying whatever was reserved.
		articleID, err := insertArticleWithUniqueSlug(r.Context(), conn, &body)
		if err != nil {
			if errors.Is(err, errArticleSlugLockBusy) {
				writeError(w, http.StatusServiceUnavailable, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.ReplaceArticleAuthors(r.Context(), conn, articleID, body.Authors); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.ReplaceArticleCategories(r.Context(), conn, articleID, body.Categories); err != nil {
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
		activity.LogRequest(r, action, body.Title, "slug", body.Slug, "article_id", articleID)
		writeJSON(w, http.StatusCreated, models.ArticleCreateResponse{ID: articleID, Slug: body.Slug})
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !requireArticleWriteRole(w, r) {
			return
		}
		// Resolve before locking: the lock has to name the row, not the slug,
		// or a request arriving on a legacy /articles/:slug URL and one on the
		// id-qualified route would take different keys for the same article.
		target, err = resolveArticleTarget(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		unlock := articleEditLocks.Lock(target.lockKey())
		defer unlock()
		var body models.Article
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.Slug) == "" {
			body.Slug = target.slug
		} else if !isValidCanonicalSlug(body.Slug) {
			writeError(w, http.StatusBadRequest, "slug must be canonical")
			return
		}
		if _, ok := db.NormalizeCanonicalURL(body.CanonicalURL); !ok {
			writeError(w, http.StatusBadRequest, "canonical_url must be an absolute http(s) URL")
			return
		}
		if rejectTakenSlug(w, r, conn, target, strings.TrimSpace(body.Slug)) {
			return
		}
		oldCategories, isActiveArticle, err := loadArticleCategoriesByArchiveState(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		fields := db.ArticleToDBFields(body)
		where, whereArgs := target.articleWhere()
		fields = append(fields, whereArgs...)
		result, err := db.Update(r.Context(), conn, "articles",
			[]string{"title", "slug", "excerpt", "text", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url", "focus_keyword", "meta_description", "seo_title", "scheduled_pub_date", "canonical_url", "noindex", "photo_alt"},
			where,
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
		if target.hasID {
			err = db.ReplaceArticleAuthors(r.Context(), conn, target.id, authorIDsFromOverviews(body.Authors))
		} else {
			err = db.ReplaceArticleAuthorsBySlug(r.Context(), conn, body.Slug, authorIDsFromOverviews(body.Authors))
		}
		if err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "article not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Keyed on the new slug, because the UPDATE above may have renamed it.
		// Unconditional, unlike the taxonomy counts: the index describes where
		// the article is filed, which is true of archived rows too: they are
		// excluded by the listing's own archived_at predicate, not by being
		// missing from here.
		if target.hasID {
			err = db.ReplaceArticleCategories(r.Context(), conn, target.id, body.Categories)
		} else {
			err = db.ReplaceArticleCategoriesBySlug(r.Context(), conn, body.Slug, body.Categories)
		}
		if err != nil {
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !requireArticleWriteRole(w, r) {
			return
		}
		// See PutArticle: the lock names the resolved row, so every route that
		// reaches this article contends on the same key.
		target, err = resolveArticleTarget(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		unlock := articleEditLocks.Lock(target.lockKey())
		defer unlock()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var authorIDs *[]int64
		oldCategories, isActiveArticle, err := loadArticleCategoriesByArchiveState(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		currentPublishedAt, lastPublishedAt, err := loadArticlePublishDate(r.Context(), conn, target)
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
		renamedSlug := ""
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
			case "excerpt":
				s, ok := v.(string)
				if !ok {
					writeError(w, http.StatusBadRequest, "excerpt must be a string")
					return
				}
				// The editor sends the excerpt field on every save, blank or not,
				// so a patch that clears it is the ordinary case of an author who
				// never filled it in, not a request for an article with no
				// summary anywhere it is listed. Derive one from the body, the
				// same fallback POST applies. The patch's own content wins if it
				// carries one; the map is iterated in random order, so read it
				// from the body rather than from whatever the loop has processed.
				content, _ := body["content"].(string)
				if strings.TrimSpace(content) == "" {
					// By id where there is one: a slug lookup can read a
					// different article's body than the one being written.
					var stored string
					var err error
					if target.hasID {
						stored, err = db.GetArticleContentByID(r.Context(), conn, target.id)
					} else {
						stored, err = db.GetArticleContentBySlug(r.Context(), conn, target.slug)
					}
					if err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
					content = stored
				}
				setCols = append(setCols, column)
				setArgs = append(setArgs, db.ExcerptOrDerived(s, content))
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
				renamedSlug = trimmed
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
		if rejectTakenSlug(w, r, conn, target, renamedSlug) {
			return
		}
		targetSlug := target.slug
		setCols = append(setCols, "mod_date")
		setArgs = append(setArgs, time.Now().UTC().Format("2006-01-02 15:04:05"))
		if len(setCols) > 0 {
			where, whereArgs := target.articleWhere()
			result, err := db.Update(r.Context(), conn, "articles", setCols, where, append(setArgs, whereArgs...)...)
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
		if authorIDs != nil {
			if target.hasID {
				err = db.ReplaceArticleAuthors(r.Context(), conn, target.id, *authorIDs)
			} else {
				err = db.ReplaceArticleAuthorsBySlug(r.Context(), conn, targetSlug, *authorIDs)
			}
			if err != nil {
				if err == sql.ErrNoRows {
					writeError(w, http.StatusNotFound, "article not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// nextCategories is oldCategories unless this patch carried a categories
		// field, so an unrelated patch rewrites the index to what it already
		// held rather than clearing it.
		if target.hasID {
			err = db.ReplaceArticleCategories(r.Context(), conn, target.id, nextCategories)
		} else {
			err = db.ReplaceArticleCategoriesBySlug(r.Context(), conn, targetSlug, nextCategories)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if isActiveArticle {
			if err := reconcileTaxonomyArticleCounts(r.Context(), conn, oldCategories, nextCategories); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		logTarget := targetSlug
		if rawTitle, ok := body["title"].(string); ok && strings.TrimSpace(rawTitle) != "" {
			logTarget = strings.TrimSpace(rawTitle)
		}
		activity.LogRequest(r, "article_updated", logTarget, "slug", targetSlug)
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !requireArticleWriteRole(w, r) {
			return
		}
		// See PutArticle. The archive state matters here: a slug shared by an
		// archived row and a live one has to resolve to the one this handler
		// can actually act on.
		target, err = resolveArticleTarget(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		unlock := articleEditLocks.Lock(target.lockKey())
		defer unlock()
		categories, found, err := loadArticleCategoriesByArchiveState(r.Context(), conn, target, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		where, args := target.articleWhereArchived(false)
		result, err := conn.ExecContext(r.Context(), "UPDATE `articles` SET `archived_at` = NOW() WHERE "+where, args...)
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
		activity.LogRequest(r, "article_deleted", target.slug, "article_id", target.id)
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
		target, err := articleTargetFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !requireArticleWriteRole(w, r) {
			return
		}
		// See PutArticle. The archive state matters here: a slug shared by an
		// archived row and a live one has to resolve to the one this handler
		// can actually act on.
		target, err = resolveArticleTarget(r.Context(), conn, target, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		unlock := articleEditLocks.Lock(target.lockKey())
		defer unlock()
		categories, found, err := loadArticleCategoriesByArchiveState(r.Context(), conn, target, true)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "article not found")
			return
		}
		where, args := target.articleWhereArchived(true)
		result, err := conn.ExecContext(r.Context(), "UPDATE `articles` SET `archived_at` = NULL WHERE "+where, args...)
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
		activity.LogRequest(r, "article_restored", target.slug, "article_id", target.id)
		w.WriteHeader(http.StatusNoContent)
	}
}
