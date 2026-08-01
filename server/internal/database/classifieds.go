package database

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"server/internal/models"
)

// Classified statuses. A submission lands as pending and is moved by an editor,
// either from the CMS queue or by clicking a button on the Slack notification.
var ValidClassifiedStatuses = map[string]bool{
	models.ClassifiedStatusPending:  true,
	models.ClassifiedStatusApproved: true,
	models.ClassifiedStatusRejected: true,
}

const classifiedColumns = "id, contact_name, contact_email, label, message, end_date, status, decided_at, decided_by, decided_via, created_at"

func EnsureClassifiedsTable(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, TableSchema("classifieds")); err != nil {
		return err
	}

	// Expand-only migration for databases that already have the table: the
	// CREATE above is a no-op there, so a column added to
	// schema/classifieds.sql reaches them only through this block. Keep the two
	// in step.
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE classifieds
		ADD COLUMN IF NOT EXISTS contact_name VARCHAR(255) NOT NULL,
		ADD COLUMN IF NOT EXISTS contact_email VARCHAR(255) NOT NULL,
		ADD COLUMN IF NOT EXISTS label VARCHAR(64) NOT NULL,
		ADD COLUMN IF NOT EXISTS message LONGTEXT NOT NULL,
		ADD COLUMN IF NOT EXISTS end_date DATE NULL,
		ADD COLUMN IF NOT EXISTS status VARCHAR(32) NOT NULL DEFAULT 'pending',
		ADD COLUMN IF NOT EXISTS submitter_ip VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS decided_at DATETIME NULL,
		ADD COLUMN IF NOT EXISTS decided_by VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS decided_via VARCHAR(32) NULL,
		ADD COLUMN IF NOT EXISTS created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	`)
	return err
}

// InsertClassified stores a new submission as pending and returns it.
func InsertClassified(ctx context.Context, conn *sql.DB, c models.ClassifiedSubmitRequest, ip string) (models.Classified, error) {
	var endDate any
	if parsed := ParseClassifiedEndDate(c.EndDate); parsed != nil {
		endDate = parsed.Format("2006-01-02")
	}

	res, err := conn.ExecContext(ctx, `
		INSERT INTO classifieds (contact_name, contact_email, label, message, end_date, status, submitter_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.Name, c.Email, c.Label, c.Message, endDate, models.ClassifiedStatusPending, ip)
	if err != nil {
		return models.Classified{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return models.Classified{}, err
	}
	return GetClassified(ctx, conn, id)
}

// GetClassified reads a single classified by id.
func GetClassified(ctx context.Context, conn *sql.DB, id int64) (models.Classified, error) {
	row := conn.QueryRowContext(ctx, "SELECT "+classifiedColumns+" FROM classifieds WHERE id = ? LIMIT 1", id)
	return scanClassifiedRow(row)
}

// ListPublicClassifieds returns the classifieds the public site shows: approved
// and not past their end date. A row with no end date never expires.
func ListPublicClassifieds(ctx context.Context, conn *sql.DB, limit, offset int) ([]models.Classified, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT `+classifiedColumns+` FROM classifieds
		WHERE status = ? AND (end_date IS NULL OR end_date >= CURDATE())
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, models.ClassifiedStatusApproved, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectClassifieds(rows)
}

// ListClassifieds is the editor-facing listing: every status, newest first,
// optionally filtered to one status.
func ListClassifieds(ctx context.Context, conn *sql.DB, status string, limit, offset int) ([]models.Classified, int, error) {
	where := ""
	args := []any{}
	if status != "" {
		where = " WHERE status = ?"
		args = append(args, status)
	}

	var totalCount int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM classifieds"+where, args...).Scan(&totalCount); err != nil {
		return nil, 0, err
	}

	rows, err := conn.QueryContext(ctx, "SELECT "+classifiedColumns+" FROM classifieds"+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items, err := collectClassifieds(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, totalCount, nil
}

// CountClassifiedsByStatus powers the filter tabs in the moderation queue.
func CountClassifiedsByStatus(ctx context.Context, conn *sql.DB) (map[string]int, error) {
	rows, err := conn.QueryContext(ctx, "SELECT status, COUNT(*) FROM classifieds GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	total := 0
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
		total += count
	}
	counts["all"] = total
	return counts, rows.Err()
}

// SetClassifiedStatus records a moderation decision. decidedVia distinguishes a
// click in the CMS queue from one on the Slack notification, which is worth
// keeping: the two paths are authenticated completely differently.
func SetClassifiedStatus(ctx context.Context, conn *sql.DB, id int64, status, decidedBy, decidedVia string) error {
	_, err := conn.ExecContext(ctx, `
		UPDATE classifieds
		SET status = ?, decided_at = ?, decided_by = ?, decided_via = ?
		WHERE id = ?
	`, status, time.Now().UTC(), decidedBy, decidedVia, id)
	return err
}

func DeleteClassified(ctx context.Context, conn *sql.DB, id int64) (bool, error) {
	res, err := conn.ExecContext(ctx, "DELETE FROM classifieds WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// ParseClassifiedEndDate accepts the date formats the public submission form
// can produce. An unparseable or empty value means "no expiry" rather than an
// error, so a malformed date never costs the submission.
func ParseClassifiedEndDate(raw string) *time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05", "01/02/2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}

type classifiedScanner interface {
	Scan(dest ...any) error
}

func scanClassifiedRow(row classifiedScanner) (models.Classified, error) {
	var (
		c          models.Classified
		endDate    sql.NullTime
		decidedAt  sql.NullTime
		decidedBy  sql.NullString
		decidedVia sql.NullString
		createdAt  sql.NullTime
	)

	if err := row.Scan(&c.ID, &c.Name, &c.Email, &c.Label, &c.Message, &endDate, &c.Status, &decidedAt, &decidedBy, &decidedVia, &createdAt); err != nil {
		return models.Classified{}, err
	}

	if endDate.Valid {
		c.EndDate = endDate.Time.Format("2006-01-02")
	}
	if decidedAt.Valid {
		decided := decidedAt.Time.UTC()
		c.DecidedAt = &decided
	}
	c.DecidedBy = decidedBy.String
	c.DecidedVia = decidedVia.String
	if createdAt.Valid {
		created := createdAt.Time.UTC()
		c.CreatedAt = &created
	}
	return c, nil
}

func collectClassifieds(rows *sql.Rows) ([]models.Classified, error) {
	items := make([]models.Classified, 0)
	for rows.Next() {
		item, err := scanClassifiedRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
