package database

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

const DefaultScheduleInterval = time.Minute

type ScheduleResult struct {
	ArticlesPublished int64
	PollsClosed       int64
}

func RunScheduleTick(ctx context.Context, conn *sql.DB) (ScheduleResult, error) {
	var result ScheduleResult

	articles, err := PublishDueArticles(ctx, conn)
	if err != nil {
		return result, err
	}
	result.ArticlesPublished = articles

	polls, err := ReconcileDuePolls(ctx, conn)
	if err != nil {
		return result, err
	}
	result.PollsClosed = polls

	return result, nil
}

func PublishDueArticles(ctx context.Context, conn *sql.DB) (int64, error) {
	res, err := conn.ExecContext(ctx, `
		UPDATE articles
		SET pub_date = scheduled_pub_date,
		    scheduled_pub_date = NULL
		WHERE pub_date IS NULL
		  AND scheduled_pub_date IS NOT NULL
		  AND scheduled_pub_date <= UTC_TIMESTAMP()
		  AND archived_at IS NULL
	`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

func RunScheduler(ctx context.Context, conn *sql.DB, interval time.Duration, logger *slog.Logger) {
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
