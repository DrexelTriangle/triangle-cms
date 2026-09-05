package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

var AuthorSortByColumn = map[string]string{
	string(models.AuthorSortByDisplayName): "display_name",
	// authors has no timestamp columns; id is auto-increment, so it doubles as creation order.
	string(models.AuthorSortByID): "id",
}

var ArticleSortByColumn = map[string]string{
	string(models.ArticleSortByTitle):         "title",
	string(models.ArticleSortBySlug):          "slug",
	string(models.ArticleSortByCreatedAt):     "creation_date",
	string(models.ArticleSortByPublishedAt):   "pub_date",
	string(models.ArticleSortByStatus):        "pub_date",
	string(models.ArticleSortByCommentStatus): "comment_status",
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var AuthorColumns = []string{"id", "display_name", "first_name", "last_name", "email", "login", "archived_at"}

// articleRow is the landing zone for a scanned article: every column at its
// nullable database type, before toArticle turns it into the model.
//
// It exists so that the column list and the scan targets come from one place.
// They used to be four hand-maintained lists that a positional Scan had to
// agree with, which meant adding a column in the wrong position silently
// shifted every value after it into the wrong field.
type articleRow struct {
	id              int64
	title           string
	slug            sql.NullString
	description     sql.NullString
	text            sql.NullString
	excerpt         sql.NullString
	tags            sql.NullString
	categories      sql.NullString
	pubDate         sql.NullTime
	modDate         sql.NullTime
	priority        sql.NullBool
	breakingNews    sql.NullBool
	commentStatus   sql.NullString
	photoURL        sql.NullString
	focusKeyword    sql.NullString
	metaDescription sql.NullString
	seoTitle        sql.NullString
	creationDate    sql.NullTime
	scheduledDate   sql.NullTime
	canonicalURL    sql.NullString
	noIndex         sql.NullBool
	photoAlt        sql.NullString
}

// articleColumn pairs a column name with the field it scans into, so a query's
// SELECT list and its Scan targets are generated from the same ordered slice and
// cannot drift out of step.
type articleColumn struct {
	name   string
	target func(*articleRow) any
}

// articleColumnSet is the full set, in the order every article query selects
// them. Adding a column here is the only edit needed: the lists below and the
// scanners all derive from it.
var articleColumnSet = []articleColumn{
	{"id", func(r *articleRow) any { return &r.id }},
	{"title", func(r *articleRow) any { return &r.title }},
	{"slug", func(r *articleRow) any { return &r.slug }},
	{"description", func(r *articleRow) any { return &r.description }},
	{"text", func(r *articleRow) any { return &r.text }},
	{"excerpt", func(r *articleRow) any { return &r.excerpt }},
	{"tags", func(r *articleRow) any { return &r.tags }},
	{"categories", func(r *articleRow) any { return &r.categories }},
	{"pub_date", func(r *articleRow) any { return &r.pubDate }},
	{"mod_date", func(r *articleRow) any { return &r.modDate }},
	{"priority", func(r *articleRow) any { return &r.priority }},
	{"breaking_news", func(r *articleRow) any { return &r.breakingNews }},
	{"comment_status", func(r *articleRow) any { return &r.commentStatus }},
	{"photo_url", func(r *articleRow) any { return &r.photoURL }},
	{"focus_keyword", func(r *articleRow) any { return &r.focusKeyword }},
	{"meta_description", func(r *articleRow) any { return &r.metaDescription }},
	{"seo_title", func(r *articleRow) any { return &r.seoTitle }},
	{"creation_date", func(r *articleRow) any { return &r.creationDate }},
	{"scheduled_pub_date", func(r *articleRow) any { return &r.scheduledDate }},
	{"canonical_url", func(r *articleRow) any { return &r.canonicalURL }},
	{"noindex", func(r *articleRow) any { return &r.noIndex }},
	{"photo_alt", func(r *articleRow) any { return &r.photoAlt }},
}

// summaryOmittedColumns are the columns a listing does not read.
//
// `text` is the whole article body, averaging ~5KB across the migrated corpus.
// A twenty-item listing was fetching around 95KB of it per request and using
// none: list responses carry an excerpt, which comes from `excerpt` (falling
// back to `description`), and is derived from the body only at write time. On
// the homepage, which builds six section blocks, the same waste was paid six
// times over.
var summaryOmittedColumns = map[string]bool{"text": true}

func articleColumnNames(full bool) []string {
	names := make([]string, 0, len(articleColumnSet))
	for _, col := range articleColumnSet {
		if !full && summaryOmittedColumns[col.name] {
			continue
		}
		names = append(names, col.name)
	}
	return names
}

// articleSelectList renders the SELECT list. qualifier prefixes each column for
// queries that join (""=unqualified, "a"=`a`.`col`).
func articleSelectList(full bool, qualifier string) string {
	prefix := ""
	if qualifier != "" {
		prefix = qualifier + "."
	}
	var b strings.Builder
	for i, name := range articleColumnNames(full) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(prefix + "`" + name + "`")
	}
	return b.String()
}

func (r *articleRow) scanTargets(full bool) []any {
	targets := make([]any, 0, len(articleColumnSet))
	for _, col := range articleColumnSet {
		if !full && summaryOmittedColumns[col.name] {
			continue
		}
		targets = append(targets, col.target(r))
	}
	return targets
}

// ArticleColumns is the full column list, for the article detail endpoint --
// the one read that actually renders the body.
var ArticleColumns = articleColumnNames(true)

// ArticleSummaryColumns is the listing column list. Everything that produces
// models.ArticleListItem uses it.
var ArticleSummaryColumns = articleColumnNames(false)

// ArticleSummarySelectList is ArticleSummaryColumns rendered as a quoted SELECT
// list, for the handlers that assemble their query as a string rather than
// through Select.
var ArticleSummarySelectList = articleSelectList(false, "")

// Image/photo URLs are canonicalized upstream in the WordPress ETL (see
// wordpress-etl Utils/MediaURL) so `photo_url` and inline body images are stored
// as final, servable URLs. The CMS stores and returns them verbatim.

func BuildOrderLimit(query, sortBy, sortDir string, sortColumnMap map[string]string, limit, offset int) string {
	if col, ok := sortColumnMap[sortBy]; ok && sortBy != "" {
		dir := "ASC"
		if strings.EqualFold(sortDir, string(models.SortDirectionDescending)) {
			dir = "DESC"
		}
		query += " ORDER BY `" + col + "` " + dir
	}
	if limit > 0 {
		query += " LIMIT " + strconv.Itoa(limit)
	}
	if offset > 0 {
		query += " OFFSET " + strconv.Itoa(offset)
	}
	return query
}

func ScanAuthor(rows *sql.Rows) (models.Author, error) {
	var a models.Author
	var displayName sql.NullString
	var firstName sql.NullString
	var lastName sql.NullString
	var email sql.NullString
	var login sql.NullString
	var archivedAt sql.NullTime
	err := rows.Scan(&a.ID, &displayName, &firstName, &lastName, &email, &login, &archivedAt)
	if err != nil {
		return models.Author{}, err
	}
	if displayName.Valid {
		a.DisplayName = displayName.String
	}
	if firstName.Valid {
		a.FirstName = firstName.String
	}
	if lastName.Valid {
		a.LastName = lastName.String
	}
	if email.Valid {
		a.Email = email.String
	}
	if login.Valid && strings.TrimSpace(login.String) != "" {
		a.Slug = normalizeSlug(login.String)
	} else {
		a.Slug = normalizeSlug(a.DisplayName)
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		a.ArchivedAt = &t
	}
	return a, nil
}

func ScanAuthorOverview(rows *sql.Rows) (models.AuthorOverview, error) {
	var a models.AuthorOverview
	var displayName sql.NullString
	var login sql.NullString
	var email sql.NullString
	var archivedAt sql.NullTime
	err := rows.Scan(&a.ID, &displayName, &login, &email, &a.ArticleCount, &archivedAt)
	if err != nil {
		return models.AuthorOverview{}, err
	}

	if displayName.Valid {
		a.DisplayName = displayName.String
	}
	if login.Valid && strings.TrimSpace(login.String) != "" {
		a.Slug = normalizeSlug(login.String)
	} else {
		a.Slug = normalizeSlug(a.DisplayName)
	}
	if email.Valid {
		a.Email = email.String
	}
	a.Archived = archivedAt.Valid
	return a, nil
}

// ScanArticle reads a row selected with ArticleColumns: the full set,
// including the article body.
func ScanArticle(rows *sql.Rows) (models.Article, error) {
	return scanArticleRow(rows, true)
}

// ScanArticleSummary reads a row selected with ArticleSummaryColumns. The
// returned Article has an empty Content, which is correct for every caller that
// renders a listing: none of them read the body.
func ScanArticleSummary(rows *sql.Rows) (models.Article, error) {
	return scanArticleRow(rows, false)
}

func scanArticleRow(rows *sql.Rows, full bool) (models.Article, error) {
	var row articleRow
	if err := rows.Scan(row.scanTargets(full)...); err != nil {
		return models.Article{}, err
	}
	return row.toArticle(), nil
}

// toArticle maps a scanned row onto the model. A column the query did not
// select is simply invalid here and leaves its field at the zero value, which is
// what makes a summary scan a strict subset of a full one rather than a
// different shape.
func (row articleRow) toArticle() models.Article {
	a := models.Article{ID: row.id, Title: row.title}

	if row.modDate.Valid {
		t := row.modDate.Time
		a.ModifiedAt = &t
	}
	if row.canonicalURL.Valid {
		a.CanonicalURL = strings.TrimSpace(row.canonicalURL.String)
	}
	if row.noIndex.Valid {
		a.NoIndex = row.noIndex.Bool
	}
	if row.focusKeyword.Valid {
		a.FocusKeyword = row.focusKeyword.String
	}
	if row.metaDescription.Valid {
		a.MetaDescription = row.metaDescription.String
	}
	if row.seoTitle.Valid {
		a.SEOTitle = row.seoTitle.String
	}
	if row.text.Valid {
		a.Content = row.text.String
	}
	if row.excerpt.Valid {
		a.Excerpt = row.excerpt.String
	} else if row.description.Valid {
		a.Excerpt = row.description.String
	}
	if row.slug.Valid {
		a.Slug = row.slug.String
	}
	a.Tags = parseStringListField(row.tags)
	a.Categories = parseStringListField(row.categories)
	if row.creationDate.Valid {
		t := row.creationDate.Time
		a.CreatedAt = &t
	}
	if row.pubDate.Valid {
		t := row.pubDate.Time
		a.PublishedAt = &t
		a.Status = models.ArticleStatusPublished
	} else if row.scheduledDate.Valid {
		t := row.scheduledDate.Time
		a.PublishedAt = &t
		a.ScheduledAt = &t
		a.Status = models.ArticleStatusScheduled
	} else {
		a.Status = models.ArticleStatusDraft
	}
	if row.priority.Valid {
		a.IsFeatured = row.priority.Bool
	}
	if row.breakingNews.Valid {
		a.BreakingNews = row.breakingNews.Bool
	}
	if row.commentStatus.Valid {
		a.CommentStatus = normalizeCommentStatus(row.commentStatus.String)
	}
	if row.photoURL.Valid {
		a.PhotoURL = row.photoURL.String
	}
	if row.photoAlt.Valid {
		a.PhotoAlt = row.photoAlt.String
	}
	return a
}

// NormalizeCanonicalURL trims a canonical-URL override and reports whether it is
// usable. An empty value is valid and means "no override": the public site
// falls back to its own /article/<slug> URL. A non-empty value must be an
// absolute http(s) URL with a host: a relative or scheme-less string in a
// <link rel="canonical"> is worse than no override at all, because it silently
// resolves against the wrong origin.
func NormalizeCanonicalURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	if parsed.Host == "" {
		return "", false
	}
	return trimmed, true
}

func parseStringListField(value sql.NullString) []string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(value.String), &parsed); err != nil {
		return strings.Split(value.String, ",")
	}
	return parsed
}

// CollectArticles drains rows selected with ArticleSummaryColumns.
//
// It is the listing path, and there is deliberately no full-column counterpart:
// every caller that collects more than one article is building a list of
// excerpts, and the single read that needs the body (the article detail
// endpoint) scans its one row with ScanArticle directly.
func CollectArticles(rows *sql.Rows) ([]models.Article, error) {
	var articles []models.Article
	for rows.Next() {
		a, err := ScanArticleSummary(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

func LoadAuthorsByArticleIDs(ctx context.Context, conn *sql.DB, articleIDs []int64) (map[int64][]models.AuthorOverview, error) {
	authorsByArticle := make(map[int64][]models.AuthorOverview, len(articleIDs))
	if len(articleIDs) == 0 {
		return authorsByArticle, nil
	}

	for _, articleID := range articleIDs {
		authorsByArticle[articleID] = []models.AuthorOverview{}
	}

	placeholders := make([]string, len(articleIDs))
	args := make([]any, len(articleIDs))
	for i, id := range articleIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "SELECT aa.articles_id, a.id, a.display_name, a.login " +
		"FROM articles_authors aa " +
		"JOIN authors a ON a.id = aa.author_id " +
		"WHERE aa.articles_id IN (" + strings.Join(placeholders, ",") + ") " +
		"ORDER BY aa.articles_id ASC, aa.id ASC"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var articleID int64
		var author models.AuthorOverview
		var displayName sql.NullString
		var login sql.NullString

		if err := rows.Scan(&articleID, &author.ID, &displayName, &login); err != nil {
			return nil, err
		}
		if displayName.Valid {
			author.DisplayName = displayName.String
		}
		if login.Valid && strings.TrimSpace(login.String) != "" {
			author.Slug = normalizeSlug(login.String)
		} else {
			author.Slug = normalizeSlug(author.DisplayName)
		}

		authorsByArticle[articleID] = append(authorsByArticle[articleID], author)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return authorsByArticle, nil
}

func PopulateArticleAuthors(ctx context.Context, conn *sql.DB, articles []models.Article) error {
	if len(articles) == 0 {
		return nil
	}

	articleIDs := make([]int64, 0, len(articles))
	for _, article := range articles {
		articleIDs = append(articleIDs, article.ID)
	}

	authorsByArticle, err := LoadAuthorsByArticleIDs(ctx, conn, articleIDs)
	if err != nil {
		return err
	}

	for i := range articles {
		authors := authorsByArticle[articles[i].ID]
		if authors == nil {
			articles[i].Authors = []models.AuthorOverview{}
			continue
		}
		articles[i].Authors = authors
	}

	return nil
}

// GetRelatedArticlesBySlug returns the k nearest neighbor articles for a given slug.
//
// This expects an `article_embeddings` table with:
// - article_id BIGINT (FK to articles.id)
// - embedding VECTOR(...)
// and an HNSW index on `embedding`.
func GetRelatedArticlesBySlug(ctx context.Context, conn *sql.DB, slug string, k int) ([]models.Article, error) {
	trimmedSlug := strings.TrimSpace(slug)
	if trimmedSlug == "" {
		return nil, fmt.Errorf("slug is required")
	}
	if k <= 0 {
		return nil, fmt.Errorf("k must be greater than 0")
	}

	query := "SELECT " + articleSelectList(false, "a") + " " +
		"FROM articles AS src " +
		"JOIN article_embeddings AS src_vec ON src_vec.article_id = src.id " +
		"JOIN article_embeddings AS cand_vec ON cand_vec.article_id <> src.id " +
		"JOIN articles AS a ON a.id = cand_vec.article_id " +
		// "Related reading" is surfaced on the public article page, so a
		// candidate must be live content regardless of who is asking: an
		// unpublished or soft-deleted article is not something to link to from
		// anywhere, including the CMS preview.
		"WHERE src.slug = ? AND a.pub_date IS NOT NULL AND a.pub_date <= UTC_TIMESTAMP() AND a.archived_at IS NULL " +
		"ORDER BY VEC_DISTANCE_EUCLIDEAN(cand_vec.embedding, src_vec.embedding), a.id DESC " +
		"LIMIT ?"

	rows, err := conn.QueryContext(ctx, query, trimmedSlug, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles, err := CollectArticles(rows)
	if err != nil {
		return nil, err
	}

	if err := PopulateArticleAuthors(ctx, conn, articles); err != nil {
		return nil, err
	}

	return articles, nil
}

// SearchArticles and its FULLTEXT/LIKE query paths live in article_search.go.

// IsVectorSearchUnavailableError reports errors that indicate vector search
// infrastructure is not available in the current DB setup.
func IsVectorSearchUnavailableError(err error) bool {
	if err == nil {
		return false
	}

	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1054, 1064, 1146, 1305:
			return true
		}
	}

	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "article_embeddings") && strings.Contains(lowered, "doesn't exist")
}

func nextArticleAuthorLinkIDTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var nextID int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) + 1 FROM articles_authors").Scan(&nextID); err != nil {
		return 0, err
	}
	return nextID, nil
}

func ReplaceArticleAuthors(ctx context.Context, conn *sql.DB, articleID int64, authorIDs []int64) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, "DELETE FROM articles_authors WHERE articles_id = ?", articleID); err != nil {
		return err
	}

	authorNames := make([]string, 0, len(authorIDs))
	if len(authorIDs) > 0 {
		nextID, err := nextArticleAuthorLinkIDTx(ctx, tx)
		if err != nil {
			return err
		}

		for i, authorID := range authorIDs {
			linkID := nextID + int64(i)
			if _, err = tx.ExecContext(
				ctx,
				"INSERT INTO articles_authors (id, author_id, articles_id) VALUES (?, ?, ?)",
				linkID,
				authorID,
				articleID,
			); err != nil {
				return fmt.Errorf("insert article author relation %s: %w", strconv.FormatInt(authorID, 10), err)
			}

			var displayName string
			if err = tx.QueryRowContext(ctx, "SELECT `display_name` FROM `authors` WHERE `id` = ?", authorID).Scan(&displayName); err != nil {
				return fmt.Errorf("lookup author %s: %w", strconv.FormatInt(authorID, 10), err)
			}
			authorNames = append(authorNames, displayName)
		}
	}

	// Keep the denormalized `authors`/`author_ids` columns in sync with the join
	// table; article list/filter queries read these columns directly.
	idsForJSON := authorIDs
	if idsForJSON == nil {
		idsForJSON = []int64{}
	}
	authorIDsJSON, err := json.Marshal(idsForJSON)
	if err != nil {
		return fmt.Errorf("marshal author ids: %w", err)
	}
	if _, err = tx.ExecContext(
		ctx,
		"UPDATE articles SET `authors` = ?, `author_ids` = ? WHERE id = ?",
		FormatTags(authorNames),
		string(authorIDsJSON),
		articleID,
	); err != nil {
		return fmt.Errorf("sync denormalized authors: %w", err)
	}

	return tx.Commit()
}

func ReplaceArticleAuthorsBySlug(ctx context.Context, conn *sql.DB, slug string, authorIDs []int64) error {
	trimmedSlug := strings.TrimSpace(slug)
	if trimmedSlug == "" {
		return sql.ErrNoRows
	}

	var articleID int64
	if err := conn.QueryRowContext(ctx, "SELECT `id` FROM `articles` WHERE `slug` = ?", trimmedSlug).Scan(&articleID); err != nil {
		return err
	}

	return ReplaceArticleAuthors(ctx, conn, articleID, authorIDs)
}

// GetArticleContentBySlug reads just the body, for a patch that has to derive
// an excerpt from a body it did not carry. A missing article is "" and no
// error: the UPDATE that follows reports the 404 on its own row count.
func GetArticleContentBySlug(ctx context.Context, conn *sql.DB, slug string) (string, error) {
	var text sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT `text` FROM `articles` WHERE `slug` = ?", strings.TrimSpace(slug)).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return text.String, nil
}

// GetArticleContentByID is GetArticleContentBySlug for a request that knows
// which row it is patching. Slugs are not unique, so deriving an excerpt from a
// slug lookup can read a different article's body than the one being written.
func GetArticleContentByID(ctx context.Context, conn *sql.DB, id int64) (string, error) {
	var text sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT `text` FROM `articles` WHERE `id` = ?", id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return text.String, nil
}

func ParsePublishedAt(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return &t
		}
	}
	return nil
}

// MarshalCategoryJSON encodes category text as JSON WITHOUT HTML-escaping.
//
// Use this for anything holding a category title. json.Marshal escapes "&" into
// a backslash-u escape, and category matching compares against the plain
// character the ETL writes, so an escaped value stops matching its own section:
// saving an article in the CMS used to rewrite "Comics & Puzzles" and quietly
// drop the article out of Comics & Puzzles. Both the articles column and
// site_taxonomy aliases go through here so the two can never disagree.
func MarshalCategoryJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	// Encode appends a newline that the column should not carry.
	return strings.TrimRight(buf.String(), "\n"), nil
}

// FormatTags encodes categories as the JSON array the `categories` column
// stores.
func FormatTags(categories []string) string {
	if len(categories) == 0 {
		return ""
	}
	encoded, err := MarshalCategoryJSON(categories)
	if err != nil {
		return strings.Join(categories, ",")
	}
	return encoded
}

func defaultCommentStatus() string {
	return "open"
}

// normalizeCommentStatus forces the value to the canonical open/closed enum,
// matching the ETL pipeline (ArticleTranslator._normalizeCommentStatus) and the
// CMS editor dropdown. WordPress exports and ad-hoc API writes carry mixed
// casing/whitespace ("Open", " closed"); anything that isn't an explicit
// "closed" falls back to "open" (the WordPress default). Applied on both read
// and write so all three layers agree on a single representation.
func normalizeCommentStatus(commentStatus string) string {
	if strings.EqualFold(strings.TrimSpace(commentStatus), "closed") {
		return "closed"
	}
	return defaultCommentStatus()
}

var slugDelimiterPattern = regexp.MustCompile(`[^a-z0-9]+`)

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// Image captions are markup, not article text. An article normally opens with
// its featured image, so stripping tags blindly made the caption, a photo
// credit line, the first thing the derived excerpt picked up. This drops the
// caption-bearing markup first: what the editor writes (trixHtmlToArticle emits
// <figure class="wp-caption"><figcaption>) and both WordPress import shapes
// (block: <figure><figcaption>; classic: <div class="wp-caption">…<p
// class="wp-caption-text">, where removing the caption element is enough since
// the surrounding div holds only the image).
var captionMarkupPattern = regexp.MustCompile(
	`(?is)<figure\b[^>]*>.*?</figure\s*>` +
		`|<figcaption\b[^>]*>.*?</figcaption\s*>` +
		`|<[a-z][a-z0-9]*\b[^>]*wp-caption-text[^>]*>.*?</[a-z][a-z0-9]*\s*>`)

// WordPress shortcodes are markup too, but bracketed rather than angled, so
// tag-stripping leaves them intact: a crossword post whose body is a single
// [puzzleme ...] shortcode derived an excerpt that was the whole shortcode,
// attributes and embed ids and all, and the public site printed it under the
// headline. Two shapes are removed, both narrow enough to leave editorial
// brackets ("[sic]", "[Editor's note]") alone: the shortcodes this corpus
// actually carries, named explicitly; and any bracketed token that carries
// attributes, which is what makes it a shortcode rather than prose. Applied
// before tag-stripping because attribute values hold HTML of their own (the
// puzzleme attribution is a link), which would otherwise be torn out from
// inside the brackets first.
var shortcodePattern = regexp.MustCompile(
	`(?is)\[/?(?:puzzleme|caption|gallery|embed|playlist|audio|video)\b[^\]]*\]` +
		`|\[[a-z][a-z0-9_-]*\s+[^\]]*=[^\]]*\]`)

// deriveExcerpt builds a plain-text excerpt from HTML article content by
// dropping image captions and shortcodes, stripping tags, collapsing
// whitespace, and truncating to a word limit.
func deriveExcerpt(content string) string {
	text := captionMarkupPattern.ReplaceAllString(content, " ")
	text = shortcodePattern.ReplaceAllString(text, " ")
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	const maxWords = 50
	if len(words) > maxWords {
		return strings.Join(words[:maxWords], " ") + "…"
	}
	return strings.Join(words, " ")
}

// ExcerptOrDerived is what every write that sets the `excerpt` column stores.
// An excerpt is optional in the editor, so a blank one means "derive it from
// the body", never "store nothing": listings render `excerpt` and nothing
// else, since the body is not selected for them, so a stored empty string is an
// article that appears on every section page and the homepage with no summary
// under it.
func ExcerptOrDerived(excerpt, content string) string {
	if trimmed := strings.TrimSpace(excerpt); trimmed != "" {
		return trimmed
	}
	return deriveExcerpt(content)
}

func normalizeSlug(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	if s == "" {
		return ""
	}
	s = slugDelimiterPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func CanonicalizeSlug(value string) string {
	return normalizeSlug(value)
}

// ArticleSlugBase is the stem every generated slug is built from: the caller's
// slug if it gave one, otherwise the title, otherwise a constant. Creation paths
// must all derive it the same way: the base names the lock that keeps two
// concurrent creates from picking the same candidate.
func ArticleSlugBase(requested, title string) string {
	base := normalizeSlug(requested)
	if base == "" {
		base = normalizeSlug(title)
	}
	if base == "" {
		base = "article"
	}
	return base
}

// ArticleSlugExists reports whether any article already carries the slug,
// archived rows included: an archived article still owns its permalink, and
// restoring it must not collide with something created in the meantime.
func ArticleSlugExists(ctx context.Context, conn queryRower, slug string) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx, "SELECT 1 FROM `articles` WHERE `slug` = ? LIMIT 1", slug).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ArticleSlugTakenByOther is ArticleSlugExists for the rename paths, which must
// ignore the row doing the renaming: saving an article without touching its slug
// would otherwise report the article's own slug as a collision. An excludeID of
// 0 excludes nothing, which is the right answer for a slug that resolves to no
// row at all.
func ArticleSlugTakenByOther(ctx context.Context, conn queryRower, slug string, excludeID int64) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx,
		"SELECT 1 FROM `articles` WHERE `slug` = ? AND `id` <> ? LIMIT 1", slug, excludeID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ResolveArticleIDBySlug picks the row a slug-only request addresses. Slugs are
// not unique (the index is deliberately non-UNIQUE, see EnsureArticlesSlugIndex)
// so "the article with this slug" has to be a rule rather than an assumption:
// prefer the archive state the caller is working in, then take the lowest id.
// Every path that resolves a slug uses this one function, so the row a request
// locks is the row it reads and the row it writes.
//
// A slug that matches nothing returns (0, nil): the caller's own query reports
// that as a 404 on its row count, and failing here would turn one missing
// article into a 500.
func ResolveArticleIDBySlug(ctx context.Context, conn queryRower, slug string, archived bool) (int64, error) {
	state := "IS NULL"
	if archived {
		state = "IS NOT NULL"
	}
	var id int64
	err := conn.QueryRowContext(ctx,
		"SELECT `id` FROM `articles` WHERE `slug` = ? AND `archived_at` "+state+" ORDER BY `id` LIMIT 1", slug,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = conn.QueryRowContext(ctx,
		"SELECT `id` FROM `articles` WHERE `slug` = ? ORDER BY `id` LIMIT 1", slug,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

func IsCanonicalSlug(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return trimmed == normalizeSlug(trimmed)
}

func ArticleInputToDBFields(body models.ArticleInput) []any {
	var publishedAt any
	var scheduledAt any
	if body.Status == models.ArticleStatusPublished {
		publishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
		if supplied := ParsePublishedAt(body.PublishedDate); supplied != nil {
			if supplied.After(time.Now().UTC()) {
				publishedAt = nil
				scheduledAt = supplied.UTC().Format("2006-01-02 15:04:05")
			} else {
				publishedAt = supplied.UTC().Format("2006-01-02 15:04:05")
			}
		}
	}
	slug := normalizeSlug(body.Slug)
	if slug == "" {
		slug = normalizeSlug(body.Title)
	}

	excerpt := ExcerptOrDerived(body.Excerpt, body.Content)

	tags := FormatTags(body.Tags)
	if tags == "" {
		tags = "[]"
	}

	// Handlers reject a malformed override before reaching here; normalizing
	// again keeps a bad value out of the column rather than storing a canonical
	// URL the public site would emit verbatim.
	canonicalURL, _ := NormalizeCanonicalURL(body.CanonicalURL)

	return []any{
		body.Title,
		slug,
		nil,
		body.Content,
		excerpt,
		FormatTags(body.Categories),
		publishedAt,
		nil,
		body.IsFeatured,
		body.BreakingNews,
		normalizeCommentStatus(body.CommentStatus),
		body.PhotoURL,
		// Default tags/metadata to empty JSON so list filters that don't
		// COALESCE these columns (e.g. exclude_type) don't drop new articles
		// on a NULL comparison.
		tags,
		"{}",
		strings.TrimSpace(body.FocusKeyword),
		strings.TrimSpace(body.MetaDescription),
		strings.TrimSpace(body.SEOTitle),
		// Stamp creation_date so a draft has a date of its own: it is what the
		// CMS listing sorts unpublished rows by (pub_date is NULL until publish).
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		scheduledAt,
		canonicalURL,
		body.NoIndex,
		strings.TrimSpace(body.PhotoAlt),
	}
}

func ArticleToDBFields(body models.Article) []any {
	var publishedAt any
	var scheduledAt any
	if body.Status == models.ArticleStatusDraft {
		// Draft wins over any stale published_date in a full replacement payload.
	} else if body.Status == models.ArticleStatusScheduled && body.PublishedAt != nil {
		scheduledAt = body.PublishedAt.UTC().Format("2006-01-02 15:04:05")
	} else if body.PublishedAt != nil {
		if body.PublishedAt.After(time.Now().UTC()) {
			scheduledAt = body.PublishedAt.UTC().Format("2006-01-02 15:04:05")
		} else {
			publishedAt = body.PublishedAt.UTC().Format("2006-01-02 15:04:05")
		}
	} else if body.Status == models.ArticleStatusPublished {
		publishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	canonicalURL, _ := NormalizeCanonicalURL(body.CanonicalURL)
	return []any{
		body.Title,
		normalizeSlug(body.Slug),
		ExcerptOrDerived(body.Excerpt, body.Content),
		body.Content,
		FormatTags(body.Categories),
		publishedAt,
		// mod_date. This used to write NULL, which made a full replacement the one
		// edit the CMS could not see it had made: the public sitemap's lastmod is
		// COALESCE(mod_date, pub_date), so a PUT silently reset the article's
		// last-modified date to its publish date, and the embedding reconciler --
		// which finds stale vectors by comparing mod_date to when the article was
		// last embedded, could never tell a PUT-edited article had changed.
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		body.IsFeatured,
		body.BreakingNews,
		normalizeCommentStatus(body.CommentStatus),
		body.PhotoURL,
		strings.TrimSpace(body.FocusKeyword),
		strings.TrimSpace(body.MetaDescription),
		strings.TrimSpace(body.SEOTitle),
		scheduledAt,
		canonicalURL,
		body.NoIndex,
		strings.TrimSpace(body.PhotoAlt),
	}
}
