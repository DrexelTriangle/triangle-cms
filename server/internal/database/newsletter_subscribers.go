package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// NewSubscriberToken returns 32 random bytes as unpadded base64url: 43
// characters, the width of newsletter_subscribers.token. The token is set once
// on insert and never rotated, so an unsubscribe link keeps working forever.
func NewSubscriberToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// PublicSubscription is a validated submission from the public form. Email is
// already normalized (trimmed, lowercased) by the caller.
type PublicSubscription struct {
	Email   string
	Name    string
	ListIDs []int64
	IP      string
}

// SubscribeOutcome says which state rule a public subscribe hit. It is for
// logs and tests only: the public response is identical for all of them.
type SubscribeOutcome string

const (
	SubscribeCreated      SubscribeOutcome = "created"
	SubscribeUpdated      SubscribeOutcome = "updated"
	SubscribeResubscribed SubscribeOutcome = "resubscribed"
)

// SubscriberFilter narrows the editor-facing subscriber listing.
type SubscriberFilter struct {
	Status string
	ListID int64
	Query  string
}

// How many times a public subscribe retries an InnoDB deadlock. Concurrent
// first subscribes of one address contend on the unique email index.
const subscribeDeadlockRetries = 3

// SubscribePublic applies the state rules for a public subscribe in one
// transaction:
//   - a new address is inserted as subscribed with a fresh token;
//   - an already-subscribed address gains the submitted lists (union);
//   - an unsubscribed address is resubscribed on the same row, keeping its
//     token, with its lists replaced so stale memberships do not come back.
//
// The insert is an upsert, not select-then-insert, so two first subscribes
// racing on one address end in one row rather than a duplicate-key error.
func SubscribePublic(ctx context.Context, conn *sql.DB, p PublicSubscription) (SubscribeOutcome, error) {
	var (
		outcome SubscribeOutcome
		err     error
	)
	for attempt := 0; attempt <= subscribeDeadlockRetries; attempt++ {
		outcome, err = subscribePublicOnce(ctx, conn, p)
		if !isDeadlock(err) {
			return outcome, err
		}
	}
	return outcome, err
}

func subscribePublicOnce(ctx context.Context, conn *sql.DB, p PublicSubscription) (SubscribeOutcome, error) {
	token, err := NewSubscriberToken()
	if err != nil {
		return "", err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO newsletter_subscribers (email, name, status, token, source, signup_ip, subscribed_at)
		VALUES (?, NULLIF(?, ''), 'subscribed', ?, ?, NULLIF(?, ''), UTC_TIMESTAMP())
		ON DUPLICATE KEY UPDATE id = id`,
		p.Email, p.Name, token, models.SubscriberSourcePublicForm, p.IP); err != nil {
		return "", err
	}

	var (
		id          int64
		status      string
		storedToken string
	)
	if err := tx.QueryRowContext(ctx,
		"SELECT id, status, token FROM newsletter_subscribers WHERE email = ? FOR UPDATE", p.Email,
	).Scan(&id, &status, &storedToken); err != nil {
		return "", err
	}

	var outcome SubscribeOutcome
	switch {
	case storedToken == token:
		// Our insert won: the row is brand new.
		outcome = SubscribeCreated
	case status == models.NewsletterStatusUnsubscribed:
		outcome = SubscribeResubscribed
		if _, err := tx.ExecContext(ctx, `
			UPDATE newsletter_subscribers
			   SET status = 'subscribed', subscribed_at = UTC_TIMESTAMP(), unsubscribed_at = NULL,
			       source = ?, signup_ip = NULLIF(?, ''), name = NULLIF(?, '')
			 WHERE id = ?`, models.SubscriberSourcePublicForm, p.IP, p.Name, id); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM newsletter_subscriber_lists WHERE subscriber_id = ?", id); err != nil {
			return "", err
		}
	default:
		outcome = SubscribeUpdated
		if p.Name != "" {
			if _, err := tx.ExecContext(ctx, "UPDATE newsletter_subscribers SET name = ? WHERE id = ?", p.Name, id); err != nil {
				return "", err
			}
		}
	}

	if err := addMemberships(ctx, tx, id, p.ListIDs); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return outcome, nil
}

// CreateNewsletterSubscriber adds a subscriber from the CMS. It refuses an
// address that already exists (ErrNewsletterDuplicate): editing an existing
// subscriber is a PATCH, not a silent merge.
func CreateNewsletterSubscriber(ctx context.Context, conn *sql.DB, email, name string, listIDs []int64) (models.NewsletterSubscriber, error) {
	token, err := NewSubscriberToken()
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO newsletter_subscribers (email, name, status, token, source, subscribed_at)
		VALUES (?, NULLIF(?, ''), 'subscribed', ?, ?, UTC_TIMESTAMP())`,
		email, name, token, models.SubscriberSourceAdmin)
	if isDuplicateKey(err) {
		return models.NewsletterSubscriber{}, ErrNewsletterDuplicate
	}
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM newsletter_subscribers WHERE email = ?", email).Scan(&id); err != nil {
		return models.NewsletterSubscriber{}, err
	}
	if err := addMemberships(ctx, tx, id, listIDs); err != nil {
		return models.NewsletterSubscriber{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.NewsletterSubscriber{}, err
	}
	return GetNewsletterSubscriber(ctx, conn, id)
}

const newsletterSubscriberColumns = "s.id, s.email, COALESCE(s.name, ''), s.status, s.source, s.subscribed_at, s.unsubscribed_at, s.created_at"

// GetNewsletterSubscriber reads one subscriber with its list ids.
// sql.ErrNoRows if it does not exist.
func GetNewsletterSubscriber(ctx context.Context, conn *sql.DB, id int64) (models.NewsletterSubscriber, error) {
	s, err := scanNewsletterSubscriber(conn.QueryRowContext(ctx,
		"SELECT "+newsletterSubscriberColumns+" FROM newsletter_subscribers s WHERE s.id = ?", id))
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	lists, err := subscriberListIDs(ctx, conn, []int64{id})
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	s.ListIDs = lists[id]
	return s, nil
}

// ListNewsletterSubscribers is the editor-facing listing, most recently
// (re)subscribed first. The total honours the filter.
func ListNewsletterSubscribers(ctx context.Context, conn *sql.DB, f SubscriberFilter, limit, offset int) ([]models.NewsletterSubscriber, int, error) {
	where := []string{}
	args := []any{}
	if f.Status != "" {
		where = append(where, "s.status = ?")
		args = append(args, f.Status)
	}
	if f.ListID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM newsletter_subscriber_lists sl WHERE sl.subscriber_id = s.id AND sl.list_id = ?)")
		args = append(args, f.ListID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		pattern := "%" + escapeLike(q) + "%"
		where = append(where, `(s.email LIKE ? ESCAPE '\\' OR s.name LIKE ? ESCAPE '\\')`)
		args = append(args, pattern, pattern)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM newsletter_subscribers s"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := conn.QueryContext(ctx,
		"SELECT "+newsletterSubscriberColumns+" FROM newsletter_subscribers s"+clause+
			" ORDER BY s.subscribed_at DESC, s.id DESC LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	subs := make([]models.NewsletterSubscriber, 0)
	ids := []int64{}
	for rows.Next() {
		s, err := scanNewsletterSubscriber(rows)
		if err != nil {
			return nil, 0, err
		}
		subs = append(subs, s)
		ids = append(ids, s.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	lists, err := subscriberListIDs(ctx, conn, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range subs {
		subs[i].ListIDs = lists[subs[i].ID]
	}
	return subs, total, nil
}

// CountNewsletterSubscribersByStatus always returns the subscribed,
// unsubscribed and all keys, zero when empty.
func CountNewsletterSubscribersByStatus(ctx context.Context, conn *sql.DB) (map[string]int, error) {
	counts := map[string]int{
		models.NewsletterStatusSubscribed:   0,
		models.NewsletterStatusUnsubscribed: 0,
		"all":                               0,
	}
	rows, err := conn.QueryContext(ctx, "SELECT status, COUNT(*) FROM newsletter_subscribers GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
		counts["all"] += n
	}
	return counts, rows.Err()
}

// UpdateNewsletterSubscriber applies a partial update from the CMS. A status
// change follows the state rules: unsubscribing stamps unsubscribed_at and
// keeps the row, token and memberships; resubscribing stamps subscribed_at and
// clears unsubscribed_at. ListIDs, when given, replaces the memberships.
func UpdateNewsletterSubscriber(ctx context.Context, conn *sql.DB, id int64, req models.NewsletterSubscriberPatchRequest) (models.NewsletterSubscriber, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.NewsletterSubscriber{}, err
	}
	defer tx.Rollback()

	var current string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM newsletter_subscribers WHERE id = ? FOR UPDATE", id).Scan(&current); err != nil {
		return models.NewsletterSubscriber{}, err
	}

	if req.Name != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE newsletter_subscribers SET name = NULLIF(?, '') WHERE id = ?", *req.Name, id); err != nil {
			return models.NewsletterSubscriber{}, err
		}
	}
	if req.Status != nil && *req.Status != current {
		switch *req.Status {
		case models.NewsletterStatusUnsubscribed:
			_, err = tx.ExecContext(ctx, "UPDATE newsletter_subscribers SET status = 'unsubscribed', unsubscribed_at = UTC_TIMESTAMP() WHERE id = ?", id)
		case models.NewsletterStatusSubscribed:
			_, err = tx.ExecContext(ctx, "UPDATE newsletter_subscribers SET status = 'subscribed', subscribed_at = UTC_TIMESTAMP(), unsubscribed_at = NULL WHERE id = ?", id)
		default:
			return models.NewsletterSubscriber{}, fmt.Errorf("invalid subscriber status %q", *req.Status)
		}
		if err != nil {
			return models.NewsletterSubscriber{}, err
		}
	}
	if req.ListIDs != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM newsletter_subscriber_lists WHERE subscriber_id = ?", id); err != nil {
			return models.NewsletterSubscriber{}, err
		}
		if err := addMemberships(ctx, tx, id, *req.ListIDs); err != nil {
			return models.NewsletterSubscriber{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return models.NewsletterSubscriber{}, err
	}
	return GetNewsletterSubscriber(ctx, conn, id)
}

// DeleteNewsletterSubscriber hard-deletes a subscriber (erasure requests
// only); memberships go with it through the foreign key cascade.
func DeleteNewsletterSubscriber(ctx context.Context, conn *sql.DB, id int64) (bool, error) {
	res, err := conn.ExecContext(ctx, "DELETE FROM newsletter_subscribers WHERE id = ?", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func addMemberships(ctx context.Context, tx *sql.Tx, subscriberID int64, listIDs []int64) error {
	if len(listIDs) == 0 {
		return nil
	}
	values := strings.TrimSuffix(strings.Repeat("(?, ?),", len(listIDs)), ",")
	args := make([]any, 0, 2*len(listIDs))
	for _, listID := range listIDs {
		args = append(args, subscriberID, listID)
	}
	_, err := tx.ExecContext(ctx, "INSERT IGNORE INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES "+values, args...)
	return err
}

func subscriberListIDs(ctx context.Context, conn *sql.DB, ids []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	for _, id := range ids {
		out[id] = []int64{}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := conn.QueryContext(ctx,
		"SELECT subscriber_id, list_id FROM newsletter_subscriber_lists WHERE subscriber_id IN ("+placeholders(len(ids))+") ORDER BY list_id",
		int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, lid int64
		if err := rows.Scan(&sid, &lid); err != nil {
			return nil, err
		}
		out[sid] = append(out[sid], lid)
	}
	return out, rows.Err()
}

func scanNewsletterSubscriber(row interface{ Scan(...any) error }) (models.NewsletterSubscriber, error) {
	var (
		s                                       models.NewsletterSubscriber
		subscribedAt, unsubscribedAt, createdAt sql.NullTime
	)
	if err := row.Scan(&s.ID, &s.Email, &s.Name, &s.Status, &s.Source, &subscribedAt, &unsubscribedAt, &createdAt); err != nil {
		return models.NewsletterSubscriber{}, err
	}
	s.SubscribedAt = utcPtr(subscribedAt)
	s.UnsubscribedAt = utcPtr(unsubscribedAt)
	s.CreatedAt = utcPtr(createdAt)
	s.ListIDs = []int64{}
	return s, nil
}

func utcPtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

// escapeLike makes user input match literally inside a LIKE pattern that
// declares ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func isDeadlock(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}
