package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"server/internal/models"
)

// Sending an issue is driven by its delivery rows. When an issue comes due it
// moves scheduled → sending and gets one pending delivery per confirmed
// subscriber, in the same transaction. A sender then works through the pending
// rows one at a time, and once none are left the issue is marked sent.
//
// Both blue and green slots run a sender, so an issue being sent carries a
// lease (send_lease_owner/until): only the holder may claim its deliveries,
// and it renews the lease with every claim. A sender that dies stops renewing,
// and the lease runs out for another slot to take over.
//
// A delivery is claimed (pending → sending) before the message goes out and
// finished (sent/failed, or back to pending on a transient error) after. A row
// still in sending when a new holder takes the lease belonged to a sender that
// died mid-message: it may or may not have been delivered, and it is marked
// failed rather than retried, because a missing copy is better than a
// duplicate in a reader's inbox.

const (
	DeliveryStatusSending = "sending"
	// DeliveryStatusSkipped is a subscriber who unsubscribed, bounced or was
	// erased between the issue starting and their copy coming up.
	DeliveryStatusSkipped = "skipped"
)

// MaxDeliveryAttempts bounds retries of a delivery that keeps hitting
// transient errors.
const MaxDeliveryAttempts = 3

// MaxNewsletterLateness is how overdue a scheduled issue may be and still go
// out on its own. An issue that has sat unsent longer than this (the sender
// was down, or not configured) is news gone stale; an editor has to schedule
// it again.
const MaxNewsletterLateness = 12 * time.Hour

const deliveryInsertBatch = 200

// StartDueNewsletter moves the earliest due scheduled issue to sending and
// queues a delivery for every confirmed subscriber. It returns the issue's id,
// or 0 when nothing is due.
func StartDueNewsletter(ctx context.Context, conn *sql.DB) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM newsletters
		WHERE status = ? AND scheduled_for <= UTC_TIMESTAMP() AND scheduled_for >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? SECOND)
		ORDER BY scheduled_for, id
		LIMIT 1
		FOR UPDATE
	`, models.NewsletterStatusScheduled, int64(MaxNewsletterLateness/time.Second)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	rows, err := tx.QueryContext(ctx, "SELECT id FROM newsletter_subscribers WHERE status = ? ORDER BY id", models.SubscriberStatusConfirmed)
	if err != nil {
		return 0, err
	}
	var subscribers []int64
	for rows.Next() {
		var sid int64
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return 0, err
		}
		subscribers = append(subscribers, sid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	for start := 0; start < len(subscribers); start += deliveryInsertBatch {
		batch := subscribers[start:min(start+deliveryInsertBatch, len(subscribers))]
		args := make([]any, 0, len(batch)*5)
		for _, sid := range batch {
			token, err := NewSubscriberToken()
			if err != nil {
				return 0, err
			}
			args = append(args, id, sid, token, DeliveryStatusPending, now)
		}
		// IGNORE because (newsletter_id, subscriber_id) is unique: an issue
		// that was sending once already keeps the deliveries it had.
		if _, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO newsletter_deliveries (newsletter_id, subscriber_id, token, status, created_at)
			VALUES `+strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?), ", len(batch)), ", "), args...); err != nil {
			return 0, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletters SET status = ?, send_lease_owner = NULL, send_lease_until = NULL WHERE id = ?
	`, models.NewsletterStatusSending, id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// OverdueScheduledNewsletters lists scheduled issues past
// MaxNewsletterLateness, which StartDueNewsletter will not start.
func OverdueScheduledNewsletters(ctx context.Context, conn *sql.DB) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT id FROM newsletters WHERE status = ? AND scheduled_for < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? SECOND) ORDER BY id
	`, models.NewsletterStatusScheduled, int64(MaxNewsletterLateness/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AcquireNewsletterSend takes the lease on an issue that is sending and not
// held by a live sender, and returns it. ok is false when there is none. Any
// delivery the previous holder left mid-message is marked failed.
func AcquireNewsletterSend(ctx context.Context, conn *sql.DB, owner string, lease time.Duration) (models.Newsletter, bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.Newsletter{}, false, err
	}
	defer tx.Rollback()

	issue, err := scanNewsletter(tx.QueryRowContext(ctx, `
		SELECT `+newsletterColumns+` FROM newsletters
		WHERE status = ? AND (send_lease_until IS NULL OR send_lease_until < UTC_TIMESTAMP() OR send_lease_owner = ?)
		ORDER BY id
		LIMIT 1
		FOR UPDATE
	`, models.NewsletterStatusSending, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return models.Newsletter{}, false, nil
	}
	if err != nil {
		return models.Newsletter{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletter_deliveries SET status = ?, error = ?
		WHERE newsletter_id = ? AND status = ?
	`, DeliveryStatusFailed, "interrupted mid-send; may or may not have been delivered", issue.ID, DeliveryStatusSending); err != nil {
		return models.Newsletter{}, false, err
	}
	if err := renewNewsletterLease(ctx, tx, issue.ID, owner, lease); err != nil {
		return models.Newsletter{}, false, err
	}
	return issue, true, tx.Commit()
}

func renewNewsletterLease(ctx context.Context, tx *sql.Tx, id int64, owner string, lease time.Duration) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE newsletters SET send_lease_owner = ?, send_lease_until = DATE_ADD(UTC_TIMESTAMP(), INTERVAL ? SECOND) WHERE id = ?
	`, owner, int64(lease/time.Second), id)
	return err
}

// holdsNewsletterLease locks the issue row and reports whether owner still
// holds a live lease on it while it is sending.
func holdsNewsletterLease(ctx context.Context, tx *sql.Tx, id int64, owner string) (bool, error) {
	var held bool
	err := tx.QueryRowContext(ctx, `
		SELECT status = ? AND send_lease_owner = ? AND send_lease_until >= UTC_TIMESTAMP()
		FROM newsletters WHERE id = ? FOR UPDATE
	`, models.NewsletterStatusSending, owner, id).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return held, err
}

// ReleaseNewsletterSend gives the lease up so another slot can carry on
// straight away instead of waiting for it to run out.
func ReleaseNewsletterSend(ctx context.Context, conn *sql.DB, id int64, owner string) error {
	_, err := conn.ExecContext(ctx, `
		UPDATE newsletters SET send_lease_owner = NULL, send_lease_until = NULL WHERE id = ? AND send_lease_owner = ?
	`, id, owner)
	return err
}

// ClaimOutcome says what ClaimNewsletterDelivery found.
type ClaimOutcome int

const (
	// DeliveryClaimed: the returned delivery is the caller's to send.
	DeliveryClaimed ClaimOutcome = iota
	// DeliveriesDone: nothing is left to send; the issue is now marked sent.
	DeliveriesDone
	// DailyCapReached: the rolling 24-hour count has hit the cap. Try later.
	DailyCapReached
	// LeaseLost: another sender holds the issue now, or it is no longer
	// sending. Stop.
	LeaseLost
)

// ClaimedDelivery is one copy to send.
type ClaimedDelivery struct {
	ID              int64
	Token           string
	Email           string
	Name            string
	SubscriberToken string
	Attempts        int
}

// ClaimNewsletterDelivery renews the lease and claims the next pending
// delivery of the issue, provided fewer than dailyCap copies (of any issue,
// tests included: the mailbox's limit counts them all) went out in the last
// 24 hours. Copies for subscribers who are no longer confirmed are skipped on
// the way. With nothing left, it marks the issue sent.
func ClaimNewsletterDelivery(ctx context.Context, conn *sql.DB, issueID int64, owner string, lease time.Duration, dailyCap int) (ClaimedDelivery, ClaimOutcome, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}
	defer tx.Rollback()

	held, err := holdsNewsletterLease(ctx, tx, issueID, owner)
	if err != nil || !held {
		return ClaimedDelivery{}, LeaseLost, err
	}
	if err := renewNewsletterLease(ctx, tx, issueID, owner, lease); err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletter_deliveries d
		LEFT JOIN newsletter_subscribers s ON s.id = d.subscriber_id
		SET d.status = ?
		WHERE d.newsletter_id = ? AND d.is_test = 0 AND d.status = ? AND (s.id IS NULL OR s.status <> ?)
	`, DeliveryStatusSkipped, issueID, DeliveryStatusPending, models.SubscriberStatusConfirmed); err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}

	var d ClaimedDelivery
	err = tx.QueryRowContext(ctx, `
		SELECT d.id, d.token, d.attempts, s.email, s.name, s.token
		FROM newsletter_deliveries d
		JOIN newsletter_subscribers s ON s.id = d.subscriber_id
		WHERE d.newsletter_id = ? AND d.is_test = 0 AND d.status = ?
		ORDER BY d.id
		LIMIT 1
		FOR UPDATE
	`, issueID, DeliveryStatusPending).Scan(&d.ID, &d.Token, &d.Attempts, &d.Email, &d.Name, &d.SubscriberToken)
	if errors.Is(err, sql.ErrNoRows) {
		if err := completeNewsletterSend(ctx, tx, issueID); err != nil {
			return ClaimedDelivery{}, LeaseLost, err
		}
		return ClaimedDelivery{}, DeliveriesDone, tx.Commit()
	}
	if err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}

	var sentToday int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM newsletter_deliveries
		WHERE sent_at >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL 1 DAY) AND status = ?
	`, DeliveryStatusSent).Scan(&sentToday); err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}
	if sentToday >= dailyCap {
		// Commit the lease renewal and skips; the claim itself waits.
		return ClaimedDelivery{}, DailyCapReached, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE newsletter_deliveries SET status = ?, attempts = attempts + 1 WHERE id = ?
	`, DeliveryStatusSending, d.ID); err != nil {
		return ClaimedDelivery{}, LeaseLost, err
	}
	d.Attempts++
	return d, DeliveryClaimed, tx.Commit()
}

// completeNewsletterSend marks the issue sent, unless a delivery is still
// out. recipient_count is the copies that actually went.
func completeNewsletterSend(ctx context.Context, tx *sql.Tx, issueID int64) error {
	var inFlight int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM newsletter_deliveries WHERE newsletter_id = ? AND is_test = 0 AND status IN (?, ?)
	`, issueID, DeliveryStatusPending, DeliveryStatusSending).Scan(&inFlight); err != nil {
		return err
	}
	if inFlight > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE newsletters
		SET status = ?, sent_at = UTC_TIMESTAMP(), send_lease_owner = NULL, send_lease_until = NULL,
			recipient_count = (SELECT COUNT(*) FROM newsletter_deliveries WHERE newsletter_id = ? AND is_test = 0 AND status = ?)
		WHERE id = ?
	`, models.NewsletterStatusSent, issueID, DeliveryStatusSent, issueID)
	return err
}

// DeliveryResult is how one attempt at a claimed delivery went.
type DeliveryResult int

const (
	DeliverySent DeliveryResult = iota
	// DeliveryRejected: the sender refused the message for good (a bad
	// address, say). It is not retried.
	DeliveryRejected
	// DeliveryDeferred: a transient failure. The delivery goes back to
	// pending until it runs out of attempts.
	DeliveryDeferred
)

// FinishNewsletterDelivery records the outcome of sending a claimed delivery.
// errMsg is kept (truncated) for a failure.
func FinishNewsletterDelivery(ctx context.Context, conn *sql.DB, d ClaimedDelivery, result DeliveryResult, errMsg string) error {
	// The column holds 255 characters; cut on a rune, not a byte, so the
	// value stays valid UTF-8.
	if runes := []rune(errMsg); len(runes) > 255 {
		errMsg = string(runes[:255])
	}
	var err error
	switch {
	case result == DeliverySent:
		_, err = conn.ExecContext(ctx, `
			UPDATE newsletter_deliveries SET status = ?, sent_at = UTC_TIMESTAMP(), error = NULL WHERE id = ?
		`, DeliveryStatusSent, d.ID)
	case result == DeliveryDeferred && d.Attempts < MaxDeliveryAttempts:
		_, err = conn.ExecContext(ctx, `
			UPDATE newsletter_deliveries SET status = ?, error = ? WHERE id = ?
		`, DeliveryStatusPending, errMsg, d.ID)
	default:
		_, err = conn.ExecContext(ctx, `
			UPDATE newsletter_deliveries SET status = ?, error = ? WHERE id = ?
		`, DeliveryStatusFailed, errMsg, d.ID)
	}
	return err
}
