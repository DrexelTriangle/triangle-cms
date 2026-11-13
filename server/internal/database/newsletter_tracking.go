package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"
)

// A delivery is one copy of an issue sent to one address: the unit opens and
// clicks are counted against, and what the send worker will walk through.
// Test sends get deliveries too (is_test, no subscriber), so tracking can be
// tried out on a draft, but they never count towards an issue's numbers.

const (
	DeliveryStatusPending = "pending"
	DeliveryStatusSent    = "sent"
	DeliveryStatusFailed  = "failed"
)

// MaxTrackedURLLength matches newsletter_links.url; a longer link is left
// untracked rather than truncated.
const MaxTrackedURLLength = 2048

// CreateTestDelivery opens a pending delivery for a test send and returns its
// id and token.
func CreateTestDelivery(ctx context.Context, conn *sql.DB, newsletterID int64) (int64, string, error) {
	token, err := NewSubscriberToken()
	if err != nil {
		return 0, "", err
	}
	res, err := conn.ExecContext(ctx, `
		INSERT INTO newsletter_deliveries (newsletter_id, subscriber_id, is_test, token, status, created_at)
		VALUES (?, NULL, 1, ?, ?, ?)
	`, newsletterID, token, DeliveryStatusPending, time.Now().UTC())
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	return id, token, err
}

// FinishDelivery records how a send went.
func FinishDelivery(ctx context.Context, conn *sql.DB, id int64, sent bool) error {
	status, sentAt := DeliveryStatusFailed, any(nil)
	if sent {
		status, sentAt = DeliveryStatusSent, time.Now().UTC()
	}
	_, err := conn.ExecContext(ctx, "UPDATE newsletter_deliveries SET status = ?, sent_at = ? WHERE id = ?", status, sentAt, id)
	return err
}

func urlHash(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// EnsureNewsletterLinks registers an issue's links and returns their ids. A
// click is redirected only to a link registered for the delivery's issue, so
// the click endpoint can't be used to send anyone anywhere else.
func EnsureNewsletterLinks(ctx context.Context, conn *sql.DB, newsletterID int64, urls []string) (map[string]int64, error) {
	ids := make(map[string]int64, len(urls))
	if len(urls) == 0 {
		return ids, nil
	}
	// One transaction, so the read-back below is on the primary that just
	// took the inserts rather than a replica that may not have them yet.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, url := range urls {
		if len(url) > MaxTrackedURLLength {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO newsletter_links (newsletter_id, url, url_hash, created_at) VALUES (?, ?, ?, ?)
		`, newsletterID, url, urlHash(url), now); err != nil {
			return nil, err
		}
		var id int64
		if err := tx.QueryRowContext(ctx,
			"SELECT id FROM newsletter_links WHERE newsletter_id = ? AND url_hash = ?", newsletterID, urlHash(url),
		).Scan(&id); err != nil {
			return nil, err
		}
		ids[url] = id
	}
	return ids, tx.Commit()
}

type trackedDelivery struct {
	id, newsletterID int64
	isTest           bool
	opened, clicked  bool
}

func lockDelivery(ctx context.Context, tx *sql.Tx, token string) (trackedDelivery, error) {
	var d trackedDelivery
	var firstOpened, firstClicked sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT id, newsletter_id, is_test, first_opened_at, first_clicked_at
		FROM newsletter_deliveries WHERE token = ? FOR UPDATE
	`, token).Scan(&d.id, &d.newsletterID, &d.isTest, &firstOpened, &firstClicked)
	d.opened, d.clicked = firstOpened.Valid, firstClicked.Valid
	return d, err
}

// markOpened counts an open on the delivery, and on its issue the first time
// a real delivery is opened.
func markOpened(ctx context.Context, tx *sql.Tx, d trackedDelivery, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletter_deliveries SET open_count = open_count + 1, first_opened_at = COALESCE(first_opened_at, ?) WHERE id = ?
	`, now, d.id); err != nil {
		return err
	}
	if !d.opened && !d.isTest {
		_, err := tx.ExecContext(ctx, "UPDATE newsletters SET open_count = open_count + 1 WHERE id = ?", d.newsletterID)
		return err
	}
	return nil
}

// RecordNewsletterOpen counts a load of the tracking pixel. sql.ErrNoRows
// means the token matches no delivery.
func RecordNewsletterOpen(ctx context.Context, conn *sql.DB, token string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := lockDelivery(ctx, tx, token)
	if err != nil {
		return err
	}
	if err := markOpened(ctx, tx, d, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordNewsletterClick counts a click and returns where it goes. A click is
// also an open: images are often blocked, so the pixel misses readers who
// clicked. sql.ErrNoRows means the token or the link is unknown, or the link
// belongs to another issue.
func RecordNewsletterClick(ctx context.Context, conn *sql.DB, token string, linkID int64) (string, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	d, err := lockDelivery(ctx, tx, token)
	if err != nil {
		return "", err
	}
	// Rows are locked in one order, the delivery, then the link, then the
	// issue (as RecordNewsletterOpen does), so two readers clicking the same
	// link can't deadlock. The link's X lock is taken here, before the
	// newsletter_delivery_links insert, whose foreign key would otherwise
	// take a shared lock on it that two clicks then both try to upgrade. It
	// must be a locking read: a plain SELECT opens a snapshot, and the
	// click_count UPDATE below then fails with Error 1020 when another click
	// committed to the row since (innodb_snapshot_isolation).
	var url string
	if err := tx.QueryRowContext(ctx,
		"SELECT url FROM newsletter_links WHERE id = ? AND newsletter_id = ? FOR UPDATE", linkID, d.newsletterID,
	).Scan(&url); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if !d.isTest {
		if _, err := tx.ExecContext(ctx, "UPDATE newsletter_links SET click_count = click_count + 1 WHERE id = ?", linkID); err != nil {
			return "", err
		}
	}
	// The reader's own count for this link, so unique clickers per link are
	// exact. The delivery's lock makes this insert-or-bump race-free.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO newsletter_delivery_links (delivery_id, link_id, click_count, first_clicked_at, last_clicked_at)
		VALUES (?, ?, 1, ?, ?)
		ON DUPLICATE KEY UPDATE click_count = click_count + 1, last_clicked_at = VALUES(last_clicked_at)
	`, d.id, linkID, now, now); err != nil {
		return "", err
	}
	if !d.opened {
		if err := markOpened(ctx, tx, d, now); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletter_deliveries SET click_count = click_count + 1, first_clicked_at = COALESCE(first_clicked_at, ?) WHERE id = ?
	`, now, d.id); err != nil {
		return "", err
	}
	if !d.isTest {
		if !d.clicked {
			if _, err := tx.ExecContext(ctx, "UPDATE newsletters SET click_count = click_count + 1 WHERE id = ?", d.newsletterID); err != nil {
				return "", err
			}
		}
	}
	return url, tx.Commit()
}
