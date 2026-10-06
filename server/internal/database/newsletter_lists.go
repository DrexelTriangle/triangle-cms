package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// ErrNewsletterDuplicate is a unique-key collision: a list name or a
// subscriber email that already exists.
var ErrNewsletterDuplicate = errors.New("newsletter: duplicate")

func isDuplicateKey(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

const newsletterListSelect = `
	SELECT l.id, l.name, COALESCE(l.description, ''), l.is_public, l.created_at,
	       (SELECT COUNT(*) FROM newsletter_subscriber_lists sl
	          JOIN newsletter_subscribers s ON s.id = sl.subscriber_id
	         WHERE sl.list_id = l.id AND s.status = 'subscribed')
	  FROM newsletter_lists l`

// ListNewsletterLists returns every list in id order, each with the number of
// members who are currently subscribed.
func ListNewsletterLists(ctx context.Context, conn *sql.DB) ([]models.NewsletterList, error) {
	rows, err := conn.QueryContext(ctx, newsletterListSelect+" ORDER BY l.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lists := make([]models.NewsletterList, 0)
	for rows.Next() {
		l, err := scanNewsletterList(rows)
		if err != nil {
			return nil, err
		}
		lists = append(lists, l)
	}
	return lists, rows.Err()
}

func getNewsletterListByName(ctx context.Context, conn *sql.DB, name string) (models.NewsletterList, error) {
	return scanNewsletterList(conn.QueryRowContext(ctx, newsletterListSelect+" WHERE l.name = ?", name))
}

func getNewsletterList(ctx context.Context, conn *sql.DB, id int64) (models.NewsletterList, error) {
	return scanNewsletterList(conn.QueryRowContext(ctx, newsletterListSelect+" WHERE l.id = ?", id))
}

// CreateNewsletterList adds a list. Its id comes from AUTO_INCREMENT, which
// starts at 1000 so the WordPress port can keep ids 1..7.
func CreateNewsletterList(ctx context.Context, conn *sql.DB, name, description string, isPublic bool) (models.NewsletterList, error) {
	_, err := conn.ExecContext(ctx,
		"INSERT INTO newsletter_lists (name, description, is_public) VALUES (?, NULLIF(?, ''), ?)",
		name, description, isPublic)
	if isDuplicateKey(err) {
		return models.NewsletterList{}, ErrNewsletterDuplicate
	}
	if err != nil {
		return models.NewsletterList{}, err
	}
	// Re-read by the unique name rather than trusting LastInsertId, which
	// MaxScale can misreport after a write.
	return getNewsletterListByName(ctx, conn, name)
}

// UpdateNewsletterList applies a partial update. sql.ErrNoRows if the list
// does not exist.
func UpdateNewsletterList(ctx context.Context, conn *sql.DB, id int64, req models.NewsletterListPatchRequest) (models.NewsletterList, error) {
	if _, err := getNewsletterList(ctx, conn, id); err != nil {
		return models.NewsletterList{}, err
	}
	sets := []string{}
	args := []any{}
	if req.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *req.Name)
	}
	if req.Description != nil {
		sets = append(sets, "description = NULLIF(?, '')")
		args = append(args, *req.Description)
	}
	if req.IsPublic != nil {
		sets = append(sets, "is_public = ?")
		args = append(args, *req.IsPublic)
	}
	if len(sets) > 0 {
		_, err := conn.ExecContext(ctx, "UPDATE newsletter_lists SET "+strings.Join(sets, ", ")+" WHERE id = ?", append(args, id)...)
		if isDuplicateKey(err) {
			return models.NewsletterList{}, ErrNewsletterDuplicate
		}
		if err != nil {
			return models.NewsletterList{}, err
		}
	}
	return getNewsletterList(ctx, conn, id)
}

// CountExistingNewsletterLists reports how many of ids name a real list, or a
// real public list when publicOnly is set. Callers compare it to len(ids)
// after de-duplicating.
func CountExistingNewsletterLists(ctx context.Context, conn *sql.DB, ids []int64, publicOnly bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	query := "SELECT COUNT(*) FROM newsletter_lists WHERE id IN (" + placeholders(len(ids)) + ")"
	if publicOnly {
		query += " AND is_public = 1"
	}
	var n int
	err := conn.QueryRowContext(ctx, query, int64Args(ids)...).Scan(&n)
	return n, err
}

func scanNewsletterList(row interface{ Scan(...any) error }) (models.NewsletterList, error) {
	var (
		l         models.NewsletterList
		createdAt sql.NullTime
	)
	if err := row.Scan(&l.ID, &l.Name, &l.Description, &l.IsPublic, &createdAt, &l.SubscribedCount); err != nil {
		return models.NewsletterList{}, err
	}
	if createdAt.Valid {
		created := createdAt.Time.UTC()
		l.CreatedAt = &created
	}
	return l, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}
