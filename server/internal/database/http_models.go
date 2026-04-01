package database

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"server/internal/models"
)

var AuthorSortByColumn = map[string]string{
	string(models.AuthorSortByDisplayName): "display_name",
	string(models.AuthorSortByCreatedAt):   "created_at",
	string(models.AuthorSortByUpdatedAt):   "updated_at",
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
	var firstName sql.NullString
	var lastName sql.NullString
	var email sql.NullString
	err := rows.Scan(&a.ID, &a.DisplayName, &firstName, &lastName, &email)
	if err != nil {
		return models.Author{}, err
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
