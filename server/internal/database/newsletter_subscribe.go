package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// SubscribeOutcome says what a public subscribe did. The endpoint answers the
// same for all of them, so the form never tells a stranger whether an address
// is on the list.
type SubscribeOutcome int

const (
	// Subscribed: a new subscriber, confirmed at once (single opt-in, as the
	// WordPress plugin was).
	Subscribed SubscribeOutcome = iota
	// AlreadySubscribed: confirmed already; any new lists were added.
	AlreadySubscribed
	// NotResubscribed: the address unsubscribed or bounced, and stays that
	// way. Anyone can type an address into the form, so it must not undo
	// someone's unsubscribe; the reader's own link (the resubscribe button on
	// the unsubscribe page) can.
	NotResubscribed
)

// IsNewsletterList reports whether id is one of the lists 1-7.
func IsNewsletterList(id int) bool {
	for _, list := range newsletterLists {
		if list.ID == id {
			return true
		}
	}
	return false
}

// subscribeAttempts bounds the retries of a subscribe InnoDB picked as a
// deadlock victim. A subscribe only ever adds, so running it again is safe.
const subscribeAttempts = 3

// SubscribeNewsletter adds an address from the public form. lists must already
// be valid list ids.
func SubscribeNewsletter(ctx context.Context, conn *sql.DB, email string, lists []int) (SubscribeOutcome, error) {
	var (
		outcome SubscribeOutcome
		err     error
	)
	for range subscribeAttempts {
		outcome, err = subscribeNewsletterOnce(ctx, conn, email, lists)
		if !isDeadlock(err) {
			break
		}
	}
	return outcome, err
}

func subscribeNewsletterOnce(ctx context.Context, conn *sql.DB, email string, lists []int) (SubscribeOutcome, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Insert first, rather than SELECT ... FOR UPDATE and insert if missing:
	// a locking read of an absent row takes a gap lock that concurrent
	// submits of the same new address all share, and their inserts then
	// deadlock on each other. A duplicate email leaves the existing row as it
	// is (id = id changes nothing).
	//
	// RowsAffected can't say which happened: the connection sets
	// clientFoundRows (see buildDSN), under which an unchanged duplicate
	// reports 1, the same as an insert. So the row is read back, now a lock on
	// a row that exists, and the token says whose it is: a fresh random token
	// only matches if this insert wrote it.
	token, err := NewSubscriberToken()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = id
	`, email, models.SubscriberStatusConfirmed, token, now, now, now); err != nil {
		return 0, err
	}

	// The email column's collation ignores case, so A@x.org finds a@x.org.
	var (
		id          int64
		status      string
		storedToken string
	)
	if err := tx.QueryRowContext(ctx,
		"SELECT id, status, token FROM newsletter_subscribers WHERE email = ? FOR UPDATE", email,
	).Scan(&id, &status, &storedToken); err != nil {
		return 0, err
	}
	if status != models.SubscriberStatusConfirmed {
		return NotResubscribed, nil
	}
	if err := addSubscriberLists(ctx, tx, id, lists); err != nil {
		return 0, err
	}
	outcome := AlreadySubscribed
	if storedToken == token {
		outcome = Subscribed
	}
	return outcome, tx.Commit()
}

// addSubscriberLists only ever adds: a form submit can't take anyone off a
// list they chose.
func addSubscriberLists(ctx context.Context, tx *sql.Tx, subscriberID int64, lists []int) error {
	for _, list := range lists {
		if _, err := tx.ExecContext(ctx,
			"INSERT IGNORE INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (?, ?)", subscriberID, list,
		); err != nil {
			return err
		}
	}
	return nil
}

// isDeadlock reports InnoDB's ER_LOCK_DEADLOCK: the transaction was rolled
// back to break a deadlock and can simply be run again.
func isDeadlock(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1213
}
