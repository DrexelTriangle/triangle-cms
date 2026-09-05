package handlers

import (
	"database/sql"
	"net/http"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"sort"
	"strings"
)

// orderCategoriesForSection stably moves matching categories first so the card's
// single category label reflects the requested section.
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
		return params.subsectionMatch()
	}
	return params.SectionMatchSlugs
}

// articleListItems converts articles for a listing. preferSlugs is the section
// and subsections the listing was asked for, empty when there is no section
// context (an author page or an unfiltered listing) where no category is
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

		setPublicReadCache(w, r)
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
		subsections, err := visibleSubsectionsOf(r.Context(), conn, params.Section)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		setPublicReadCache(w, r)
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
		children, err := visibleSubsectionsOf(r.Context(), conn, params.Subsection)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		setPublicReadCache(w, r)
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
			Subsections: children,
			Articles:    articleListItems(articles, excerptWords, categoryPreferenceSlugs(params)...),
			Pagination:  paginationResponse(page, limit, offset, hasMore, totalCount),
		})
	}
}

type ArticleParams struct {
	AuthorSlug   string
	AuthorSearch string
	Section      string
	Subsection   string
	// SectionMatchSlugs is the section plus every subsection below it, resolved
	// during validation because the filter builder has no database handle. A
	// section with no matching category of its own is still populated by its
	// children.
	SectionMatchSlugs []string
	// SubsectionMatchSlugs is the same for a subsection that has children of its
	// own. Food is a container in exactly the way a section can be: its articles
	// are filed under Beer Reviews and Restaurant Reviews, so matching "food"
	// alone renders its page empty. Holds just the subsection itself for a leaf.
	SubsectionMatchSlugs []string
}

// subsectionMatch is the slug set a subsection listing filters on, falling back
// to the subsection itself when nothing resolved it.
//
// The fallback is not cosmetic. An empty slug list makes
// appendCategorySlugCondition add no condition at all, so a filter that failed
// to resolve would not narrow the listing: it would return every article on
// the site under a subsection's name. Callers that build ArticleParams without
// going through normalizeAndValidateArticleParams reach exactly that state.
func (p ArticleParams) subsectionMatch() []string {
	if len(p.SubsectionMatchSlugs) > 0 {
		return p.SubsectionMatchSlugs
	}
	if strings.TrimSpace(p.Subsection) == "" {
		return nil
	}
	return []string{p.Subsection}
}

func queryArticles(r *http.Request, conn *sql.DB, params ArticleParams, limit, offset int) (*sql.Rows, error) {
	q := r.URL.Query()
	conditions, args := articleQueryFilters(r, params)
	query := "SELECT " + db.ArticleSummarySelectList + " FROM `articles`"
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

// defaultArticleOrderBy sorts public listings by publication date, then id.
// Editors use id DESC so new drafts with NULL publication dates stay visible.
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

	// Exclude import artifacts with neither authors nor categories, but retain
	// unpublished editor drafts. Test source columns, not potentially stale derived indexes.
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
		appendCategorySlugCondition(&conditions, &args, params.subsectionMatch()...)
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
