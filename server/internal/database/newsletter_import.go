package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"server/internal/models"
)

// LegacySubscriber is one row of the WordPress Newsletter plugin's
// wp_newsletter table, already mapped to CMS terms by the importer.
type LegacySubscriber struct {
	WPID           int64
	Email          string
	Name           string
	Token          string
	Status         string
	Lists          []int
	SubscribedAt   time.Time
	UnsubscribedAt *time.Time
}

// LegacyImportReport counts what an import did (or, in a dry run, would do).
type LegacyImportReport struct {
	Inserted     int
	Unsubscribed int
	Unchanged    int
	// WordPress ids whose address already belongs to a CMS subscriber that
	// did not come from that WordPress row. Left alone for a human to look at.
	EmailTaken []int64
}

// ImportLegacySubscribers copies WordPress subscribers in, keyed on their
// WordPress id so it can be run again until WordPress is retired.
//
// A re-run only ever moves a subscriber towards unsubscribed: someone who
// unsubscribed in WordPress since the last run is unsubscribed here too, but
// nobody is ever resubscribed by an import, because the CMS record of an
// unsubscribe must win over a stale WordPress row. Lists are copied on insert
// only.
//
// It works inside the caller's transaction, so a dry run is the same code
// followed by a rollback.
func ImportLegacySubscribers(ctx context.Context, tx *sql.Tx, subs []LegacySubscriber) (LegacyImportReport, error) {
	var report LegacyImportReport
	now := time.Now().UTC()

	for _, s := range subs {
		var (
			existingID     int64
			existingStatus string
		)
		err := tx.QueryRowContext(ctx,
			"SELECT id, status FROM newsletter_subscribers WHERE legacy_wp_id = ? FOR UPDATE", s.WPID,
		).Scan(&existingID, &existingStatus)
		switch {
		case err == nil:
			if s.Status != models.SubscriberStatusConfirmed && existingStatus == models.SubscriberStatusConfirmed {
				unsubscribedAt := now
				if s.UnsubscribedAt != nil {
					unsubscribedAt = s.UnsubscribedAt.UTC()
				}
				if _, err := tx.ExecContext(ctx,
					"UPDATE newsletter_subscribers SET status = ?, unsubscribed_at = ?, updated_at = ? WHERE id = ?",
					s.Status, unsubscribedAt, now, existingID,
				); err != nil {
					return report, err
				}
				report.Unsubscribed++
			} else {
				report.Unchanged++
			}
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return report, err
		}

		var taken int64
		err = tx.QueryRowContext(ctx, "SELECT id FROM newsletter_subscribers WHERE email = ?", s.Email).Scan(&taken)
		if err == nil {
			report.EmailTaken = append(report.EmailTaken, s.WPID)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}

		token, err := NewSubscriberToken()
		if err != nil {
			return report, err
		}
		var unsubscribedAt any
		if s.Status != models.SubscriberStatusConfirmed {
			t := now
			if s.UnsubscribedAt != nil {
				t = s.UnsubscribedAt.UTC()
			}
			unsubscribedAt = t
		}
		var legacyToken any
		if s.Token != "" {
			legacyToken = s.Token
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO newsletter_subscribers (email, name, status, token, legacy_wp_id, legacy_wp_token,
				subscribed_at, unsubscribed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, s.Email, s.Name, s.Status, token, s.WPID, legacyToken, s.SubscribedAt.UTC(), unsubscribedAt, now, now)
		if err != nil {
			return report, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return report, err
		}
		for _, list := range s.Lists {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (?, ?)", id, list,
			); err != nil {
				return report, err
			}
		}
		report.Inserted++
	}
	return report, nil
}
