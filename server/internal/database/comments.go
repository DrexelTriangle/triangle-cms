package database

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ValidCommentStatuses = map[string]bool{
	"pending":  true,
	"approved": true,
	"spam":     true,
	"trash":    true,
}

func EnsureCommentsTable(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, TableSchema("comments")); err != nil {
		return err
	}

	// Expand-only migration for databases that already have the table: CREATE
	// TABLE above is a no-op there, so a column added to schema/comments.sql
	// reaches them only through this block. Keep the two in step.
	if _, err := conn.ExecContext(ctx, `
		ALTER TABLE comments
		ADD COLUMN IF NOT EXISTS article_id BIGINT NULL,
		ADD COLUMN IF NOT EXISTS wp_post_id BIGINT NULL,
		ADD COLUMN IF NOT EXISTS parent_id BIGINT NULL,
		ADD COLUMN IF NOT EXISTS author_name LONGTEXT,
		ADD COLUMN IF NOT EXISTS author_email LONGTEXT,
		ADD COLUMN IF NOT EXISTS author_url LONGTEXT,
		ADD COLUMN IF NOT EXISTS author_ip VARCHAR(255),
		ADD COLUMN IF NOT EXISTS author_user_id BIGINT,
		ADD COLUMN IF NOT EXISTS content LONGTEXT,
		ADD COLUMN IF NOT EXISTS created_at DATETIME,
		ADD COLUMN IF NOT EXISTS created_at_gmt DATETIME,
		ADD COLUMN IF NOT EXISTS status VARCHAR(32),
		ADD COLUMN IF NOT EXISTS `+"`type`"+` VARCHAR(32)
	`); err != nil {
		return err
	}

	_, err := conn.ExecContext(ctx, "ALTER TABLE comments MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT")
	return err
}

func ScanComment(rows *sql.Rows) (Comment, error) {
	var (
		comment      Comment
		articleID    sql.NullInt64
		parentID     sql.NullInt64
		authorName   sql.NullString
		authorURL    sql.NullString
		content      sql.NullString
		createdAt    sql.NullTime
		createdAtGMT sql.NullTime
		status       sql.NullString
		commentType  sql.NullString
	)

	err := rows.Scan(
		&comment.ID,
		&articleID,
		&parentID,
		&authorName,
		&authorURL,
		&content,
		&createdAt,
		&createdAtGMT,
		&status,
		&commentType,
	)
	if err != nil {
		return Comment{}, err
	}

	if articleID.Valid {
		comment.ArticleID = articleID.Int64
	}
	if parentID.Valid {
		comment.ParentID = parentID.Int64
	}
	if authorName.Valid {
		comment.AuthorName = authorName.String
	}
	if authorURL.Valid {
		comment.AuthorURL = authorURL.String
	}
	if content.Valid {
		comment.Content = content.String
	}
	if createdAt.Valid {
		t := createdAt.Time
		comment.CreatedAt = &t
	}
	if createdAtGMT.Valid {
		t := createdAtGMT.Time
		comment.CreatedAtGMT = &t
	}
	if status.Valid {
		comment.Status = status.String
	}
	if commentType.Valid {
		comment.Type = commentType.String
	}

	return comment, nil
}

// GetApprovedCommentsByArticleSlug returns the reader comments shown under an
// article. Pingbacks and trackbacks are excluded: WordPress stored them in the
// same table, but they are automated link notifications rather than anything a
// reader wrote, and on the imported data they are almost entirely SEO spam.
// This matches the predicate adminCommentConditions already applies, which is
// what left the public endpoint as the one place they still surfaced. Rows stay
// in the table; nothing renders them. A NULL or empty type means a comment --
// that is what WordPress wrote for ordinary rows.
func GetApprovedCommentsByArticleSlug(ctx context.Context, conn *sql.DB, slug string) ([]Comment, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT
			c.id,
			c.article_id,
			c.parent_id,
			c.author_name,
			c.author_url,
			c.content,
			c.created_at,
			c.created_at_gmt,
			c.status,
			c.`+"`type`"+`
		FROM comments c
		JOIN articles a ON a.id = c.article_id
		WHERE a.slug = ? AND c.status = 'approved'
		  AND (c.`+"`type`"+` IS NULL OR c.`+"`type`"+` = '' OR c.`+"`type`"+` = 'comment')
		ORDER BY COALESCE(c.created_at_gmt, c.created_at), c.id
	`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	comments := make([]Comment, 0)
	for rows.Next() {
		comment, err := ScanComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return comments, nil
}

func GetArticleCommentTargetBySlug(ctx context.Context, conn *sql.DB, slug string) (articleID int64, commentStatus string, exists bool, err error) {
	err = conn.QueryRowContext(ctx, `
		SELECT id, COALESCE(comment_status, '')
		FROM articles
		WHERE slug = ?
		  AND pub_date IS NOT NULL
		  AND pub_date <= UTC_TIMESTAMP()
		  AND archived_at IS NULL
		LIMIT 1
	`, slug).Scan(&articleID, &commentStatus)
	if err == nil {
		return articleID, commentStatus, true, nil
	}
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	return 0, "", false, fmt.Errorf("get article for comments: %w", err)
}

func CommentCanAcceptReply(ctx context.Context, conn *sql.DB, articleID, commentID int64) (bool, error) {
	if commentID <= 0 {
		return true, nil
	}

	var exists int
	err := conn.QueryRowContext(ctx, `
		SELECT 1
		FROM comments
		WHERE id = ? AND article_id = ?
		  AND status = 'approved'
		  AND (`+"`type`"+` IS NULL OR `+"`type`"+` = '' OR `+"`type`"+` = 'comment')
		LIMIT 1
	`, commentID, articleID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check comment parent can accept reply: %w", err)
}

func CreateComment(ctx context.Context, conn *sql.DB, params CreateCommentParams) (Comment, error) {
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = "approved"
	}
	commentType := strings.TrimSpace(params.Type)
	if commentType == "" {
		commentType = "comment"
	}
	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	result, err := conn.ExecContext(ctx, `
		INSERT INTO comments (
			article_id,
			wp_post_id,
			parent_id,
			author_name,
			author_email,
			author_url,
			author_ip,
			author_user_id,
			content,
			created_at,
			created_at_gmt,
			status,
			`+"`type`"+`
		) VALUES (?, NULL, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), 0, ?, ?, ?, ?, ?)
	`,
		params.ArticleID,
		params.ParentID,
		params.AuthorName,
		params.AuthorEmail,
		params.AuthorURL,
		params.AuthorIP,
		params.Content,
		createdAt,
		createdAt,
		status,
		commentType,
	)
	if err != nil {
		return Comment{}, fmt.Errorf("insert comment: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Comment{}, fmt.Errorf("read inserted comment id: %w", err)
	}

	return GetCommentByID(ctx, conn, id)
}

func GetCommentByID(ctx context.Context, conn *sql.DB, id int64) (Comment, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id,
			article_id,
			parent_id,
			author_name,
			author_url,
			content,
			created_at,
			created_at_gmt,
			status,
			`+"`type`"+`
		FROM comments
		WHERE id = ?
	`, id)
	if err != nil {
		return Comment{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		return Comment{}, sql.ErrNoRows
	}
	comment, err := ScanComment(rows)
	if err != nil {
		return Comment{}, err
	}
	if err := rows.Err(); err != nil {
		return Comment{}, err
	}
	return comment, nil
}

func ArticleExistsBySlug(ctx context.Context, conn *sql.DB, slug string) (bool, error) {
	var exists int
	err := conn.QueryRowContext(ctx, "SELECT 1 FROM articles WHERE slug = ? AND pub_date IS NOT NULL AND pub_date <= UTC_TIMESTAMP() AND archived_at IS NULL LIMIT 1", slug).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check article exists: %w", err)
}

func adminCommentConditions(status, search string) ([]string, []any) {
	conditions := []string{"(c.`type` IS NULL OR c.`type` = '' OR c.`type` = 'comment')"}
	args := make([]any, 0)

	trimmedStatus := strings.ToLower(strings.TrimSpace(status))
	if trimmedStatus != "" && trimmedStatus != "all" {
		conditions = append(conditions, "c.`status` = ?")
		args = append(args, trimmedStatus)
	}

	trimmedSearch := strings.TrimSpace(search)
	if trimmedSearch != "" {
		like := "%" + trimmedSearch + "%"
		conditions = append(conditions, "(c.`author_name` LIKE ? OR c.`author_email` LIKE ? OR c.`content` LIKE ? OR a.`title` LIKE ? OR a.`slug` LIKE ?)")
		args = append(args, like, like, like, like, like)
	}

	return conditions, args
}

func scanAdminComment(rows *sql.Rows) (AdminComment, error) {
	var (
		comment      AdminComment
		articleID    sql.NullInt64
		parentID     sql.NullInt64
		authorName   sql.NullString
		authorEmail  sql.NullString
		authorURL    sql.NullString
		content      sql.NullString
		createdAt    sql.NullTime
		createdAtGMT sql.NullTime
		status       sql.NullString
		commentType  sql.NullString
		articleTitle sql.NullString
		articleSlug  sql.NullString
	)

	err := rows.Scan(
		&comment.ID,
		&articleID,
		&parentID,
		&authorName,
		&authorEmail,
		&authorURL,
		&content,
		&createdAt,
		&createdAtGMT,
		&status,
		&commentType,
		&articleTitle,
		&articleSlug,
	)
	if err != nil {
		return AdminComment{}, err
	}

	if articleID.Valid {
		comment.ArticleID = articleID.Int64
	}
	if parentID.Valid {
		comment.ParentID = parentID.Int64
	}
	if authorName.Valid {
		comment.AuthorName = authorName.String
	}
	if authorEmail.Valid {
		comment.AuthorEmail = authorEmail.String
	}
	if authorURL.Valid {
		comment.AuthorURL = authorURL.String
	}
	if content.Valid {
		comment.Content = content.String
	}
	if createdAt.Valid {
		t := createdAt.Time
		comment.CreatedAt = &t
	}
	if createdAtGMT.Valid {
		t := createdAtGMT.Time
		comment.CreatedAtGMT = &t
	}
	if status.Valid {
		comment.Status = status.String
	}
	if commentType.Valid {
		comment.Type = commentType.String
	}
	if articleTitle.Valid {
		comment.ArticleTitle = articleTitle.String
	}
	if articleSlug.Valid {
		comment.ArticleSlug = articleSlug.String
	}

	return comment, nil
}

func ListAdminComments(ctx context.Context, conn *sql.DB, limit, offset int, status, search string) ([]AdminComment, int, map[string]int, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	conditions, args := adminCommentConditions(status, search)
	whereClause := " WHERE " + strings.Join(conditions, " AND ")

	var totalCount int
	countQuery := "SELECT COUNT(*) FROM comments c LEFT JOIN articles a ON a.`id` = c.`article_id`" + whereClause
	if err := conn.QueryRowContext(ctx, countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, nil, fmt.Errorf("count comments: %w", err)
	}

	countConditions, countArgs := adminCommentConditions("all", search)
	countsQuery := `
		SELECT COALESCE(NULLIF(c.` + "`status`" + `, ''), 'pending') AS status, COUNT(*)
		FROM comments c
		LEFT JOIN articles a ON a.` + "`id`" + ` = c.` + "`article_id`" + `
		WHERE ` + strings.Join(countConditions, " AND ") + `
		GROUP BY COALESCE(NULLIF(c.` + "`status`" + `, ''), 'pending')
	`
	rows, err := conn.QueryContext(ctx, countsQuery, countArgs...)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("count comments by status: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{"all": 0, "pending": 0, "approved": 0, "spam": 0, "trash": 0}
	for rows.Next() {
		var statusLabel string
		var count int
		if err := rows.Scan(&statusLabel, &count); err != nil {
			return nil, 0, nil, err
		}
		normalizedStatus := strings.ToLower(strings.TrimSpace(statusLabel))
		if normalizedStatus == "" {
			normalizedStatus = "pending"
		}
		counts[normalizedStatus] = count
		counts["all"] += count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, err
	}

	query := `
		SELECT
			c.` + "`id`" + `,
			c.` + "`article_id`" + `,
			c.` + "`parent_id`" + `,
			c.` + "`author_name`" + `,
			c.` + "`author_email`" + `,
			c.` + "`author_url`" + `,
			c.` + "`content`" + `,
			c.` + "`created_at`" + `,
			c.` + "`created_at_gmt`" + `,
			c.` + "`status`" + `,
			c.` + "`type`" + `,
			a.` + "`title`" + `,
			a.` + "`slug`" + `
		FROM comments c
		LEFT JOIN articles a ON a.` + "`id`" + ` = c.` + "`article_id`" + `
	` + whereClause + `
		ORDER BY COALESCE(c.` + "`created_at_gmt`" + `, c.` + "`created_at`" + `) DESC, c.` + "`id`" + ` DESC
		LIMIT ` + strconv.Itoa(limit) + ` OFFSET ` + strconv.Itoa(offset)

	rows, err = conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	comments := make([]AdminComment, 0)
	for rows.Next() {
		comment, err := scanAdminComment(rows)
		if err != nil {
			return nil, 0, nil, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, nil, err
	}

	return comments, totalCount, counts, nil
}

func GetAdminCommentByID(ctx context.Context, conn *sql.DB, id int64) (AdminComment, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT
			c.`+"`id`"+`,
			c.`+"`article_id`"+`,
			c.`+"`parent_id`"+`,
			c.`+"`author_name`"+`,
			c.`+"`author_email`"+`,
			c.`+"`author_url`"+`,
			c.`+"`content`"+`,
			c.`+"`created_at`"+`,
			c.`+"`created_at_gmt`"+`,
			c.`+"`status`"+`,
			c.`+"`type`"+`,
			a.`+"`title`"+`,
			a.`+"`slug`"+`
		FROM comments c
		LEFT JOIN articles a ON a.`+"`id`"+` = c.`+"`article_id`"+`
		WHERE c.`+"`id`"+` = ?
	`, id)
	if err != nil {
		return AdminComment{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		return AdminComment{}, sql.ErrNoRows
	}
	comment, err := scanAdminComment(rows)
	if err != nil {
		return AdminComment{}, err
	}
	if err := rows.Err(); err != nil {
		return AdminComment{}, err
	}
	return comment, nil
}

func UpdateCommentStatus(ctx context.Context, conn *sql.DB, id int64, status string) (AdminComment, error) {
	normalizedStatus := strings.ToLower(strings.TrimSpace(status))
	if !ValidCommentStatuses[normalizedStatus] {
		return AdminComment{}, fmt.Errorf("invalid comment status")
	}

	result, err := conn.ExecContext(ctx, "UPDATE comments SET `status` = ? WHERE `id` = ?", normalizedStatus, id)
	if err != nil {
		return AdminComment{}, fmt.Errorf("update comment status: %w", err)
	}
	if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
		return AdminComment{}, sql.ErrNoRows
	}

	return GetAdminCommentByID(ctx, conn, id)
}

func DeleteComment(ctx context.Context, conn *sql.DB, id int64) error {
	result, err := conn.ExecContext(ctx, "DELETE FROM comments WHERE `id` = ?", id)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
