package database

import (
	"context"
	"database/sql"
)

const PollTableName = "cms_poll_counts"

func EnsurePollsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+PollTableName+` (
			option_name VARCHAR(128) NOT NULL,
			vote_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (option_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	return err
}
