package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"server/internal/models"
)

// The WordPress Newsletter plugin's lists, kept under the same ids so an
// import from its list_N columns maps across one to one. Lists 8-40 were never
// used there.
var newsletterLists = []struct {
	ID   int
	Name string
}{
	{1, "Drexel Students"},
	{2, "Alumni"},
	{3, "Parents"},
	{4, "Administrators/Directors/Trustees"},
	{5, "Faculty"},
	{6, "Advertisers"},
	{7, "Other"},
}

var ValidSubscriberStatuses = map[string]bool{
	models.SubscriberStatusConfirmed:    true,
	models.SubscriberStatusUnsubscribed: true,
	models.SubscriberStatusBounced:      true,
}

// ErrNewsletterLocked is returned when an editor tries to change an issue the
// sender has already picked up.
var ErrNewsletterLocked = errors.New("newsletter has already been sent")

const newsletterColumns = "id, subject, preheader, status, sponsor_image_url, sponsor_link_url, sponsor_alt, body_html, email_html, scheduled_for, sent_at, recipient_count, open_count, click_count, created_at, updated_at"

// EnsureNewsletterTables creates the newsletter tables, parents first for the
// foreign keys, and makes sure lists 1-7 exist. INSERT IGNORE leaves a list an
// admin has since renamed alone.
func EnsureNewsletterTables(ctx context.Context, conn *sql.DB) error {
	for _, table := range []string{"newsletters", "newsletter_lists", "newsletter_subscribers", "newsletter_subscriber_lists",
		"newsletter_deliveries", "newsletter_links"} {
		if _, err := conn.ExecContext(ctx, TableSchema(table)); err != nil {
			return err
		}
	}
	// Added after the tables first shipped to the laptop sandbox.
	for _, alter := range []string{
		"ALTER TABLE newsletters ADD COLUMN IF NOT EXISTS click_count INT UNSIGNED NOT NULL DEFAULT 0 AFTER open_count",
		"ALTER TABLE newsletters ADD COLUMN IF NOT EXISTS send_lease_owner VARCHAR(128) NULL AFTER click_count",
		"ALTER TABLE newsletters ADD COLUMN IF NOT EXISTS send_lease_until DATETIME NULL AFTER send_lease_owner",
		"ALTER TABLE newsletter_deliveries ADD COLUMN IF NOT EXISTS attempts INT UNSIGNED NOT NULL DEFAULT 0 AFTER click_count",
		"ALTER TABLE newsletter_deliveries ADD COLUMN IF NOT EXISTS error VARCHAR(255) NULL AFTER attempts",
		"ALTER TABLE newsletter_deliveries ADD INDEX IF NOT EXISTS idx_newsletter_deliveries_issue_status (newsletter_id, status)",
		"ALTER TABLE newsletter_deliveries ADD INDEX IF NOT EXISTS idx_newsletter_deliveries_sent_at (sent_at)",
	} {
		if _, err := conn.ExecContext(ctx, alter); err != nil {
			return err
		}
	}
	for _, list := range newsletterLists {
		if _, err := conn.ExecContext(ctx, "INSERT IGNORE INTO newsletter_lists (id, name) VALUES (?, ?)", list.ID, list.Name); err != nil {
			return err
		}
	}
	return nil
}

func newsletterLocked(status string) bool {
	return status == models.NewsletterStatusSending || status == models.NewsletterStatusSent
}

// sponsorColumns flattens the optional sponsor into its three nullable columns.
// A sponsor with no image is no sponsor.
func sponsorColumns(s *models.NewsletterSponsor) (image, link, alt any) {
	if s == nil || strings.TrimSpace(s.ImageURL) == "" {
		return nil, nil, nil
	}
	return s.ImageURL, s.LinkURL, s.Alt
}

func InsertNewsletter(ctx context.Context, conn *sql.DB, in models.NewsletterInput, by string) (models.Newsletter, error) {
	now := time.Now().UTC().Truncate(time.Second)
	image, link, alt := sponsorColumns(in.Sponsor)
	res, err := conn.ExecContext(ctx, `
		INSERT INTO newsletters (subject, preheader, status, sponsor_image_url, sponsor_link_url, sponsor_alt,
			body_html, email_html, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.Subject, in.Preheader, models.NewsletterStatusDraft, image, link, alt, in.BodyHTML, in.EmailHTML, by, by, now, now)
	if err != nil {
		return models.Newsletter{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return models.Newsletter{}, err
	}

	// Built from what was written rather than read back: behind MaxScale a
	// read straight after the insert can land on a replica that has not seen it.
	created := models.Newsletter{
		ID:        id,
		Subject:   in.Subject,
		Preheader: in.Preheader,
		Status:    models.NewsletterStatusDraft,
		BodyHTML:  in.BodyHTML,
		EmailHTML: in.EmailHTML,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if image != nil {
		created.Sponsor = &models.NewsletterSponsor{ImageURL: in.Sponsor.ImageURL, LinkURL: in.Sponsor.LinkURL, Alt: in.Sponsor.Alt}
	}
	return created, nil
}

func GetNewsletter(ctx context.Context, conn *sql.DB, id int64) (models.Newsletter, error) {
	return scanNewsletter(conn.QueryRowContext(ctx, "SELECT "+newsletterColumns+" FROM newsletters WHERE id = ?", id))
}

// LatestSentNewsletter returns the most recently sent issue, or sql.ErrNoRows.
func LatestSentNewsletter(ctx context.Context, conn *sql.DB) (models.Newsletter, error) {
	return scanNewsletter(conn.QueryRowContext(ctx,
		"SELECT "+newsletterColumns+" FROM newsletters WHERE status = ? ORDER BY sent_at DESC, id DESC LIMIT 1",
		models.NewsletterStatusSent))
}

// ListNewsletters returns every issue, most recently edited first.
func ListNewsletters(ctx context.Context, conn *sql.DB) ([]models.Newsletter, error) {
	rows, err := conn.QueryContext(ctx, "SELECT "+newsletterColumns+" FROM newsletters ORDER BY updated_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]models.Newsletter, 0)
	for rows.Next() {
		item, err := scanNewsletter(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// withLockedNewsletter runs fn in a transaction holding a row lock on the
// issue, and refuses with ErrNewsletterLocked once the sender has it. The
// transaction is what makes the status check trustworthy: MaxScale sends the
// whole of it to the primary, so the check, the write and the read-back all
// see the same row, and the sender's own status change has to wait its turn.
func withLockedNewsletter(ctx context.Context, conn *sql.DB, id int64, fn func(tx *sql.Tx, current models.Newsletter) error) (models.Newsletter, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Newsletter{}, err
	}
	defer tx.Rollback()

	current, err := scanNewsletter(tx.QueryRowContext(ctx, "SELECT "+newsletterColumns+" FROM newsletters WHERE id = ? FOR UPDATE", id))
	if err != nil {
		return models.Newsletter{}, err
	}
	if newsletterLocked(current.Status) {
		return models.Newsletter{}, ErrNewsletterLocked
	}
	if err := fn(tx, current); err != nil {
		return models.Newsletter{}, err
	}

	updated, err := scanNewsletter(tx.QueryRowContext(ctx, "SELECT "+newsletterColumns+" FROM newsletters WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		// fn deleted it; there is nothing to read back.
		return models.Newsletter{}, tx.Commit()
	}
	if err != nil {
		return models.Newsletter{}, err
	}
	return updated, tx.Commit()
}

// UpdateNewsletter replaces an issue's content. A scheduled issue stays
// scheduled: the editor is fixing a typo, not calling the send off.
func UpdateNewsletter(ctx context.Context, conn *sql.DB, id int64, in models.NewsletterInput, by string) (models.Newsletter, error) {
	return withLockedNewsletter(ctx, conn, id, func(tx *sql.Tx, _ models.Newsletter) error {
		image, link, alt := sponsorColumns(in.Sponsor)
		_, err := tx.ExecContext(ctx, `
			UPDATE newsletters
			SET subject = ?, preheader = ?, sponsor_image_url = ?, sponsor_link_url = ?, sponsor_alt = ?,
				body_html = ?, email_html = ?, updated_by = ?, updated_at = ?
			WHERE id = ?
		`, in.Subject, in.Preheader, image, link, alt, in.BodyHTML, in.EmailHTML, by, time.Now().UTC(), id)
		return err
	})
}

func DeleteNewsletter(ctx context.Context, conn *sql.DB, id int64) error {
	_, err := withLockedNewsletter(ctx, conn, id, func(tx *sql.Tx, _ models.Newsletter) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM newsletters WHERE id = ?", id)
		return err
	})
	return err
}

// ScheduleNewsletter queues an issue (or moves its send time). The send worker
// picks it up once the time comes (see newsletter_send.go).
func ScheduleNewsletter(ctx context.Context, conn *sql.DB, id int64, sendAt time.Time, by string) (models.Newsletter, error) {
	return withLockedNewsletter(ctx, conn, id, func(tx *sql.Tx, _ models.Newsletter) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE newsletters SET status = ?, scheduled_for = ?, updated_by = ?, updated_at = ? WHERE id = ?
		`, models.NewsletterStatusScheduled, sendAt.UTC(), by, time.Now().UTC(), id)
		return err
	})
}

func UnscheduleNewsletter(ctx context.Context, conn *sql.DB, id int64, by string) (models.Newsletter, error) {
	return withLockedNewsletter(ctx, conn, id, func(tx *sql.Tx, _ models.Newsletter) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE newsletters SET status = ?, scheduled_for = NULL, updated_by = ?, updated_at = ? WHERE id = ?
		`, models.NewsletterStatusDraft, by, time.Now().UTC(), id)
		return err
	})
}

// GetNewsletterStats computes the dashboard tiles. The open and click rates are
// the mean of each sent issue's own rate, so one huge issue does not drown out
// the rest.
func GetNewsletterStats(ctx context.Context, conn *sql.DB) (models.NewsletterStats, error) {
	var stats models.NewsletterStats
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM newsletter_subscribers WHERE status = ?", models.SubscriberStatusConfirmed,
	).Scan(&stats.SubscriberCount); err != nil {
		return stats, err
	}

	var openRate, clickRate sql.NullFloat64
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(recipient_count), 0),
			AVG(CASE WHEN recipient_count > 0 THEN open_count / recipient_count END),
			AVG(CASE WHEN recipient_count > 0 THEN click_count / recipient_count END)
		FROM newsletters WHERE status = ?
	`, models.NewsletterStatusSent).Scan(&stats.SentCount, &stats.RecipientsReached, &openRate, &clickRate); err != nil {
		return stats, err
	}
	if openRate.Valid {
		rate := openRate.Float64
		stats.AvgOpenRate = &rate
	}
	if clickRate.Valid {
		rate := clickRate.Float64
		stats.AvgClickRate = &rate
	}
	return stats, nil
}

func scanNewsletter(row interface{ Scan(dest ...any) error }) (models.Newsletter, error) {
	var (
		n                  models.Newsletter
		image, link, alt   sql.NullString
		scheduledFor, sent sql.NullTime
	)
	if err := row.Scan(&n.ID, &n.Subject, &n.Preheader, &n.Status, &image, &link, &alt, &n.BodyHTML, &n.EmailHTML,
		&scheduledFor, &sent, &n.RecipientCount, &n.OpenCount, &n.ClickCount, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return models.Newsletter{}, err
	}
	if image.Valid && image.String != "" {
		n.Sponsor = &models.NewsletterSponsor{ImageURL: image.String, LinkURL: link.String, Alt: alt.String}
	}
	if scheduledFor.Valid {
		t := scheduledFor.Time.UTC()
		n.ScheduledFor = &t
	}
	if sent.Valid {
		t := sent.Time.UTC()
		n.SentAt = &t
	}
	n.CreatedAt = n.CreatedAt.UTC()
	n.UpdatedAt = n.UpdatedAt.UTC()
	return n, nil
}

// NewSubscriberToken returns the random half of a subscriber's unsubscribe key.
func NewSubscriberToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ListNewsletterSubscribers returns subscribers newest first, filtered to one
// status unless status is empty.
func ListNewsletterSubscribers(ctx context.Context, conn *sql.DB, status string) ([]models.NewsletterSubscriber, error) {
	query := `
		SELECT s.id, s.email, s.name, s.status, s.subscribed_at, s.unsubscribed_at,
			COALESCE(GROUP_CONCAT(l.list_id ORDER BY l.list_id), '')
		FROM newsletter_subscribers s
		LEFT JOIN newsletter_subscriber_lists l ON l.subscriber_id = s.id`
	args := []any{}
	if status != "" {
		query += " WHERE s.status = ?"
		args = append(args, status)
	}
	query += " GROUP BY s.id ORDER BY s.subscribed_at DESC, s.id DESC"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]models.NewsletterSubscriber, 0)
	for rows.Next() {
		var (
			s            models.NewsletterSubscriber
			unsubscribed sql.NullTime
			lists        string
		)
		if err := rows.Scan(&s.ID, &s.Email, &s.Name, &s.Status, &s.SubscribedAt, &unsubscribed, &lists); err != nil {
			return nil, err
		}
		s.SubscribedAt = s.SubscribedAt.UTC()
		if unsubscribed.Valid {
			t := unsubscribed.Time.UTC()
			s.UnsubscribedAt = &t
		}
		s.Lists = parseListIDs(lists)
		items = append(items, s)
	}
	return items, rows.Err()
}

func parseListIDs(csv string) []int {
	ids := make([]int, 0)
	for part := range strings.SplitSeq(csv, ",") {
		if id, err := strconv.Atoi(part); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// DeleteNewsletterSubscriber erases a subscriber outright (list memberships go
// with it via the foreign key). It reports whether the row existed.
func DeleteNewsletterSubscriber(ctx context.Context, conn *sql.DB, id int64) (bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var found int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM newsletter_subscribers WHERE id = ? FOR UPDATE", id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM newsletter_subscribers WHERE id = ?", id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
