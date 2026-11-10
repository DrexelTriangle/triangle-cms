package database

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"
)

const DefaultScheduleInterval = time.Minute

type ScheduleResult struct {
	ArticlesPublished int64
	// PublishedArticleIDs are the articles this tick found due. Both blue/green
	// slots run the scheduler, so the other slot may be the one whose UPDATE
	// took a row; the ids are for purging caches, which is harmless to repeat.
	PublishedArticleIDs []int64
	PollsClosed         int64
}

func RunScheduleTick(ctx context.Context, conn *sql.DB) (ScheduleResult, error) {
	var result ScheduleResult

	articles, ids, err := PublishDueArticles(ctx, conn)
	if err != nil {
		return result, err
	}
	result.ArticlesPublished = articles
	result.PublishedArticleIDs = ids

	polls, err := ReconcileDuePolls(ctx, conn)
	if err != nil {
		return result, err
	}
	result.PollsClosed = polls

	return result, nil
}

// dueArticlesPredicate is the one definition of "scheduled and now due", shared
// by the read that names the rows and the UPDATE that publishes them, so the
// UPDATE can never take a row the read did not report.
const dueArticlesPredicate = `pub_date IS NULL
		  AND scheduled_pub_date IS NOT NULL
		  AND scheduled_pub_date <= UTC_TIMESTAMP()
		  AND archived_at IS NULL`

// PublishDueArticles publishes every scheduled article whose time has come and
// returns how many rows the UPDATE reported plus the ids it targeted.
//
// The ids are read first, then the UPDATE is restricted to them and re-checks
// the predicate: a row the other slot published in between is skipped by the
// UPDATE but still reported, which costs a redundant cache purge and nothing
// else. The ids are not derived from the row count, which MaxScale does not
// report reliably.
func PublishDueArticles(ctx context.Context, conn *sql.DB) (int64, []int64, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM articles WHERE `+dueArticlesPredicate)
	if err != nil {
		return 0, nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	res, err := conn.ExecContext(ctx, `
		UPDATE articles
		SET pub_date = scheduled_pub_date,
		    scheduled_pub_date = NULL
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
		  AND `+dueArticlesPredicate, args...)
	if err != nil {
		return 0, nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	return n, ids, nil
}

func ReconcileDuePolls(ctx context.Context, conn *sql.DB) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var liveID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM `+PollsTableName+`
		WHERE status = ?
		  AND (starts_at IS NULL OR starts_at <= NOW())
		  AND (ends_at IS NULL OR ends_at >= NOW())
		ORDER BY COALESCE(starts_at, created_at) DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, PollStatusActive).Scan(&liveID)
	if err == sql.ErrNoRows {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE `+PollsTableName+`
		SET status = ?
		WHERE status = ?
		  AND id <> ?
		  AND (starts_at IS NULL OR starts_at <= NOW())
	`, PollStatusClosed, PollStatusActive, liveID)
	if err != nil {
		return 0, err
	}
	closed, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return closed, nil
}

// RunScheduler ticks until ctx ends. onPublished, if set, receives the ids of
// articles a tick took live, so the caller can purge the caches in front of
// the pages they now appear on; it runs even when a later step of the tick
// (closing polls) failed, because the articles are live regardless.
func RunScheduler(ctx context.Context, conn *sql.DB, interval time.Duration, logger *slog.Logger, onPublished func([]int64)) {
	if conn == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultScheduleInterval
	}
	if logger == nil {
		logger = slog.Default()
	}

	run := func() {
		result, err := RunScheduleTick(ctx, conn)
		if onPublished != nil && len(result.PublishedArticleIDs) > 0 {
			onPublished(result.PublishedArticleIDs)
		}
		if err != nil {
			logger.Error("schedule tick failed", "error", err)
			return
		}
		if result.ArticlesPublished > 0 || result.PollsClosed > 0 {
			logger.Info(
				"schedule tick applied",
				"articles_published", result.ArticlesPublished,
				"polls_closed", result.PollsClosed,
			)
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
