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

// errSubscribeRace is a concurrent submit of the same new address inserting it
// first.
var errSubscribeRace = errors.New("subscriber inserted concurrently")

// SubscribeNewsletter adds an address from the public form. lists must already
// be valid list ids.
func SubscribeNewsletter(ctx context.Context, conn *sql.DB, email string, lists []int) (SubscribeOutcome, error) {
	outcome, err := subscribeNewsletterOnce(ctx, conn, email, lists)
	if errors.Is(err, errSubscribeRace) {
		// The row exists now, so the second go finds it and adds this
		// submit's lists to it rather than dropping them.
		return subscribeNewsletterOnce(ctx, conn, email, lists)
	}
	return outcome, err
}

func subscribeNewsletterOnce(ctx context.Context, conn *sql.DB, email string, lists []int) (SubscribeOutcome, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// The email column's collation ignores case, so A@x.org finds a@x.org.
	var (
		id     int64
		status string
	)
	err = tx.QueryRowContext(ctx, "SELECT id, status FROM newsletter_subscribers WHERE email = ? FOR UPDATE", email).Scan(&id, &status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		token, err := NewSubscriberToken()
		if err != nil {
			return 0, err
		}
		now := time.Now().UTC()
		res, err := tx.ExecContext(ctx, `
			INSERT INTO newsletter_subscribers (email, status, token, subscribed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, email, models.SubscriberStatusConfirmed, token, now, now, now)
		if isDuplicateKey(err) {
			return 0, errSubscribeRace
		}
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
		if err := addSubscriberLists(ctx, tx, id, lists); err != nil {
			return 0, err
		}
		return Subscribed, tx.Commit()
	case err != nil:
		return 0, err
	case status != models.SubscriberStatusConfirmed:
		return NotResubscribed, nil
	}
	if err := addSubscriberLists(ctx, tx, id, lists); err != nil {
		return 0, err
	}
	return AlreadySubscribed, tx.Commit()
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

func isDuplicateKey(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
