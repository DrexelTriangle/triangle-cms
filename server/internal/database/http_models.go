package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"server/internal/models"
)

var AuthorSortByColumn = map[string]string{
	string(models.AuthorSortByDisplayName): "display_name",
}

var ArticleSortByColumn = map[string]string{
	string(models.ArticleSortByTitle):         "title",
	string(models.ArticleSortBySlug):          "slug",
	string(models.ArticleSortByCreatedAt):     "created_at",
	string(models.ArticleSortByPublishedAt):   "pub_date",
	string(models.ArticleSortByStatus):        "pub_date",
	string(models.ArticleSortByCommentStatus): "comment_status",
}

var AuthorColumns = []string{"id", "display_name", "first_name", "last_name", "email"}

var ArticleColumns = []string{
	"id", "title", "description", "text", "tags",
	"pub_date", "mod_date", "priority", "breaking_news",
	"comment_status", "photo_url",
}

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
	err := rows.Scan(&a.ID, &displayName, &firstName, &lastName, &email)
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
	return a, nil
}

func ScanAuthorOverview(rows *sql.Rows) (models.AuthorOverview, error) {
	var a models.AuthorOverview
	var displayName sql.NullString
	err := rows.Scan(&a.ID, &displayName)
	if err != nil {
		return models.AuthorOverview{}, err
	}
	if displayName.Valid {
		a.DisplayName = displayName.String
	}
	return a, nil
}

func ScanArticle(rows *sql.Rows) (models.Article, error) {
	var (
		a             models.Article
		description   sql.NullString
		text          sql.NullString
		tags          sql.NullString
		pubDate       sql.NullTime
		priority      sql.NullBool
		commentStatus sql.NullString
		photoURL      sql.NullString
		ignoredMod    sql.NullTime
		ignoredBreak  sql.NullBool
	)
	err := rows.Scan(
		&a.ID, &a.Title, &description, &text, &tags,
		&pubDate, &ignoredMod, &priority, &ignoredBreak,
		&commentStatus, &photoURL,
	)
	if err != nil {
		return models.Article{}, err
	}
	if text.Valid {
		a.Content = text.String
	}
	if description.Valid {
		a.Excerpt = description.String
	}
	if tags.Valid && strings.TrimSpace(tags.String) != "" {
		if err := json.Unmarshal([]byte(tags.String), &a.Categories); err != nil {
			a.Categories = strings.Split(tags.String, ",")
		}
	}
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
		a.PhotoURL = photoURL.String
	}
	return a, nil
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

	query := "SELECT aa.articles_id, a.id, a.display_name " +
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

		if err := rows.Scan(&articleID, &author.ID, &displayName); err != nil {
			return nil, err
		}
		if displayName.Valid {
			author.DisplayName = displayName.String
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
		}
	}

	return tx.Commit()
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

func ArticleInputToDBFields(body models.ArticleInput) []any {
	var publishedAt any
	if body.Status == models.ArticleStatusPublished {
		publishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	}

	return []any{
		body.Title,
		nil,
		body.Content,
		FormatTags(body.Categories),
		publishedAt,
		nil,
		body.IsFeatured,
		false,
		normalizeCommentStatus(body.CommentStatus),
		body.PhotoURL,
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
		body.Excerpt,
		body.Content,
		FormatTags(body.Categories),
		publishedAt,
		nil,
		body.IsFeatured,
		false,
		normalizeCommentStatus(body.CommentStatus),
		body.PhotoURL,
	}
}
