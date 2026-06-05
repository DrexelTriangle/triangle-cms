package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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

var AuthorColumns = []string{"id", "display_name", "first_name", "last_name", "email", "login"}

var ArticleColumns = []string{
	"id", "title", "slug", "description", "text", "excerpt", "tags", "categories",
	"pub_date", "mod_date", "priority", "breaking_news",
	"comment_status", "photo_url",
}

const articleSelectColumnsQualified = "a.`id`, a.`title`, a.`slug`, a.`description`, a.`text`, a.`excerpt`, a.`tags`, a.`categories`, a.`pub_date`, a.`mod_date`, a.`priority`, a.`breaking_news`, a.`comment_status`, a.`photo_url`"
const wordpressImageBaseURL = "https://www.thetriangle.org"

// wordpressImageProxyBaseURL is where legacy WordPress uploads are actually
// served from. The bare /wp-content/ paths 404; they must be routed through the
// site's /proxy/ handler.
const wordpressImageProxyBaseURL = wordpressImageBaseURL + "/proxy"

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
	err := rows.Scan(&a.ID, &displayName, &firstName, &lastName, &email, &login)
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
	return a, nil
}

func ScanAuthorOverview(rows *sql.Rows) (models.AuthorOverview, error) {
	var a models.AuthorOverview
	var displayName sql.NullString
	var login sql.NullString
	var email sql.NullString
	err := rows.Scan(&a.ID, &displayName, &login, &email, &a.ArticleCount)
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
	return a, nil
}

func ScanArticle(rows *sql.Rows) (models.Article, error) {
	var (
		a             models.Article
		slug          sql.NullString
		description   sql.NullString
		text          sql.NullString
		excerpt       sql.NullString
		tags          sql.NullString
		categories    sql.NullString
		pubDate       sql.NullTime
		priority      sql.NullBool
		commentStatus sql.NullString
		photoURL      sql.NullString
		ignoredMod    sql.NullTime
		ignoredBreak  sql.NullBool
	)
	err := rows.Scan(
		&a.ID, &a.Title, &slug, &description, &text, &excerpt, &tags, &categories,
		&pubDate, &ignoredMod, &priority, &ignoredBreak,
		&commentStatus, &photoURL,
	)
	if err != nil {
		return models.Article{}, err
	}
	if text.Valid {
		a.Content = text.String
	}
	if excerpt.Valid {
		a.Excerpt = excerpt.String
	} else if description.Valid {
		a.Excerpt = description.String
	}
	if slug.Valid {
		a.Slug = slug.String
	}
	a.Tags = parseStringListField(tags)
	a.Categories = parseStringListField(categories)
	if pubDate.Valid {
		t := pubDate.Time
		a.PublishedAt = &t
		a.Status = models.ArticleStatusPublished
	} else {
		a.Status = models.ArticleStatusDraft
	}
	if priority.Valid {
		a.IsFeatured = priority.Bool
	}
	if commentStatus.Valid {
		a.CommentStatus = strings.TrimSpace(commentStatus.String)
	}
	if photoURL.Valid {
		a.PhotoURL = normalizePhotoURL(photoURL.String)
	}
	return a, nil
}

func normalizePhotoURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	lowered := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lowered, "http://"), strings.HasPrefix(lowered, "https://"):
		return proxyTriangleWPContent(trimmed)
	case strings.HasPrefix(trimmed, "//"):
		return proxyTriangleWPContent("https:" + trimmed)
	case strings.HasPrefix(lowered, "www.thetriangle.org/"):
		return proxyTriangleWPContent("https://" + trimmed)
	case strings.HasPrefix(lowered, "wp-content/"):
		return wordpressImageProxyBaseURL + "/" + trimmed
	case strings.HasPrefix(trimmed, "/wp-content/"):
		return wordpressImageProxyBaseURL + trimmed
	default:
		return trimmed
	}
}

// proxyTriangleWPContent rewrites Triangle-hosted wp-content URLs to route
// through the image proxy (…/wp-content/… → …/proxy/wp-content/…) where the
// legacy uploads are actually served. URLs that already target /proxy/ or point
// elsewhere are returned unchanged.
func proxyTriangleWPContent(url string) string {
	const bareWPContent = wordpressImageBaseURL + "/wp-content/"
	if strings.HasPrefix(url, bareWPContent) {
		return wordpressImageProxyBaseURL + strings.TrimPrefix(url, wordpressImageBaseURL)
	}
	return url
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

func CollectArticles(rows *sql.Rows) ([]models.Article, error) {
	var articles []models.Article
	for rows.Next() {
		a, err := ScanArticle(rows)
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

	query := "SELECT " + articleSelectColumnsQualified + " " +
		"FROM articles AS src " +
		"JOIN article_embeddings AS src_vec ON src_vec.article_id = src.id " +
		"JOIN article_embeddings AS cand_vec ON cand_vec.article_id <> src.id " +
		"JOIN articles AS a ON a.id = cand_vec.article_id " +
		"WHERE src.slug = ? " +
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

func SearchArticles(ctx context.Context, conn *sql.DB, term string, limit, offset int) ([]models.Article, error) {
	trimmedTerm := strings.TrimSpace(term)
	if trimmedTerm == "" {
		return []models.Article{}, nil
	}

	like := "%" + trimmedTerm + "%"
	query := "SELECT `id`, `title`, `slug`, `description`, `text`, `excerpt`, `tags`, `categories`, `pub_date`, `mod_date`, `priority`, `breaking_news`, `comment_status`, `photo_url` FROM `articles` " +
		"WHERE `pub_date` IS NOT NULL AND (`title` LIKE ? OR `tags` LIKE ? OR `text` LIKE ?) " +
		"ORDER BY CASE " +
		"WHEN `title` LIKE ? THEN 1 " +
		"WHEN `tags` LIKE ? THEN 2 " +
		"WHEN `text` LIKE ? THEN 3 " +
		"ELSE 4 END, `pub_date` DESC, `id` DESC"
	query = BuildOrderLimit(query, "", "", ArticleSortByColumn, limit, offset)

	rows, err := conn.QueryContext(ctx, query, like, like, like, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return CollectArticles(rows)
}

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

func FormatTags(categories []string) string {
	if len(categories) == 0 {
		return ""
	}
	buf, err := json.Marshal(categories)
	if err != nil {
		return strings.Join(categories, ",")
	}
	return string(buf)
}

func defaultCommentStatus() string {
	return "open"
}

func normalizeCommentStatus(commentStatus string) string {
	if v := strings.TrimSpace(commentStatus); v != "" {
		return v
	}
	return defaultCommentStatus()
}

var slugDelimiterPattern = regexp.MustCompile(`[^a-z0-9]+`)

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// deriveExcerpt builds a plain-text excerpt from HTML article content by
// stripping tags, collapsing whitespace, and truncating to a word limit.
func deriveExcerpt(content string) string {
	text := htmlTagPattern.ReplaceAllString(content, " ")
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

func IsCanonicalSlug(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return trimmed == normalizeSlug(trimmed)
}

func ArticleInputToDBFields(body models.ArticleInput) []any {
	var publishedAt any
	if body.Status == models.ArticleStatusPublished {
		publishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	slug := normalizeSlug(body.Slug)
	if slug == "" {
		slug = normalizeSlug(body.Title)
	}

	excerpt := strings.TrimSpace(body.Excerpt)
	if excerpt == "" {
		excerpt = deriveExcerpt(body.Content)
	}

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
		false,
		normalizeCommentStatus(body.CommentStatus),
		normalizePhotoURL(body.PhotoURL),
		// Default tags/metadata to empty JSON so list filters that don't
		// COALESCE these columns (e.g. exclude_type) don't drop new articles
		// on a NULL comparison.
		"[]",
		"{}",
	}
}

func ArticleToDBFields(body models.Article) []any {
	var publishedAt any
	if body.PublishedAt != nil {
		publishedAt = body.PublishedAt.UTC().Format("2006-01-02 15:04:05")
	} else if body.Status == models.ArticleStatusPublished {
		publishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	return []any{
		body.Title,
		normalizeSlug(body.Slug),
		body.Excerpt,
		body.Content,
		FormatTags(body.Categories),
		publishedAt,
		nil,
		body.IsFeatured,
		false,
		normalizeCommentStatus(body.CommentStatus),
		normalizePhotoURL(body.PhotoURL),
	}
}
