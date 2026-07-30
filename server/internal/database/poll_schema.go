package database

import (
	"context"
	"database/sql"
)

const PollTableName = "cms_poll_counts"

func EnsurePollsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema(PollTableName))
	return err
}
