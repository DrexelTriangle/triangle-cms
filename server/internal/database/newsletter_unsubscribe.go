package database

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server/internal/models"
)

// SubscriberKey is what an unsubscribe link carries: our own token, or for
// links in issues WordPress sent, the WordPress subscriber id and token
// (nk=<id>-<token>), so those keep working after the cutover.
type SubscriberKey struct {
	Token       string
	LegacyID    int64
	LegacyToken string
}

var subscriberTokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ParseSubscriberKey reads t= (ours) or nk= (WordPress). Malformed keys are
// refused here so junk never reaches a query.
func ParseSubscriberKey(token, legacy string) (SubscriberKey, bool) {
	if token != "" {
		return SubscriberKey{Token: token}, subscriberTokenPattern.MatchString(token)
	}
	idPart, tokenPart, ok := strings.Cut(legacy, "-")
	if !ok || tokenPart == "" || len(tokenPart) > 64 {
		return SubscriberKey{}, false
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return SubscriberKey{}, false
	}
	return SubscriberKey{LegacyID: id, LegacyToken: tokenPart}, true
}

// SubscriberState is what the unsubscribe page needs to know about the owner
// of a key.
type SubscriberState struct {
	ID     int64
	Email  string
	Status string
}

// lookupSubscriber finds the subscriber a key belongs to, or sql.ErrNoRows.
// q is a *sql.DB for the page, or a transaction with lock=true for a change.
func lookupSubscriber(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key SubscriberKey, lock bool) (*SubscriberState, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var s SubscriberState
	if key.Token != "" {
		err := q.QueryRowContext(ctx,
			"SELECT id, email, status FROM newsletter_subscribers WHERE token = ?"+suffix, key.Token,
		).Scan(&s.ID, &s.Email, &s.Status)
		if err != nil {
			return nil, err
		}
		return &s, nil
	}
	var legacyToken sql.NullString
	err := q.QueryRowContext(ctx,
		"SELECT id, email, status, legacy_wp_token FROM newsletter_subscribers WHERE legacy_wp_id = ?"+suffix, key.LegacyID,
	).Scan(&s.ID, &s.Email, &s.Status, &legacyToken)
	if err != nil {
		return nil, err
	}
	// The id alone is guessable; the token is what proves the link is theirs.
	if !legacyToken.Valid || subtle.ConstantTimeCompare([]byte(legacyToken.String), []byte(key.LegacyToken)) != 1 {
		return nil, sql.ErrNoRows
	}
	return &s, nil
}

// FindSubscriberByKey returns the subscriber a link belongs to, or
// sql.ErrNoRows when the key matches nobody.
func FindSubscriberByKey(ctx context.Context, conn *sql.DB, key SubscriberKey) (*SubscriberState, error) {
	return lookupSubscriber(ctx, conn, key, false)
}

// UnsubscribeByKey unsubscribes a confirmed subscriber. Anyone already
// unsubscribed or bounced is left as they are, so repeating it (inboxes retry
// one-click POSTs) changes nothing. Returns the state after the call and
// whether this call changed it.
func UnsubscribeByKey(ctx context.Context, conn *sql.DB, key SubscriberKey, now time.Time) (*SubscriberState, bool, error) {
	return changeSubscription(ctx, conn, key, models.SubscriberStatusConfirmed, models.SubscriberStatusUnsubscribed, now)
}

// ResubscribeByKey undoes an unsubscribe. A bounced address stays bounced:
// sending to it again would only bounce again and hurt the sender's standing.
func ResubscribeByKey(ctx context.Context, conn *sql.DB, key SubscriberKey, now time.Time) (*SubscriberState, bool, error) {
	return changeSubscription(ctx, conn, key, models.SubscriberStatusUnsubscribed, models.SubscriberStatusConfirmed, now)
}

// changeSubscription moves the key's subscriber from one status to another
// under a row lock, so the whole exchange is on the primary behind MaxScale
// and the returned state is the written one, not a replica's.
func changeSubscription(ctx context.Context, conn *sql.DB, key SubscriberKey, from, to string, now time.Time) (*SubscriberState, bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	s, err := lookupSubscriber(ctx, tx, key, true)
	if err != nil {
		return nil, false, err
	}
	if s.Status != from {
		return s, false, nil
	}
	now = now.UTC()
	var unsubscribedAt any
	if to == models.SubscriberStatusUnsubscribed {
		unsubscribedAt = now
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE newsletter_subscribers SET status = ?, unsubscribed_at = ?, updated_at = ? WHERE id = ?",
		to, unsubscribedAt, now, s.ID,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	s.Status = to
	return s, true, nil
}

// IsNoSubscriber reports whether err means the key matched nobody.
func IsNoSubscriber(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
