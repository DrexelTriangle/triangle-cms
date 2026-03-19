package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func Select(ctx context.Context, conn *sql.DB, table string, cols []string, where string, args ...any) (*sql.Rows, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("select requires at least one column")
	}

	query := "SELECT " + strings.Join(cols, ", ") + " FROM `" + table + "`"
	if strings.TrimSpace(where) != "" {
		query += " WHERE " + where
	}

	return conn.QueryContext(ctx, query, args...)
}

func Insert(ctx context.Context, conn *sql.DB, table string, cols []string, values ...any) (sql.Result, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("insert requires at least one column")
	}
	if len(cols) != len(values) {
		return nil, fmt.Errorf("insert column/value count mismatch")
	}

	quotedCols := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, col := range cols {
		quotedCols[i] = "`" + col + "`"
		placeholders[i] = "?"
	}

	query := "INSERT INTO `" + table + "` (" + strings.Join(quotedCols, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	return conn.ExecContext(ctx, query, values...)
}

func Update(ctx context.Context, conn *sql.DB, table string, cols []string, where string, args ...any) (sql.Result, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("update requires at least one column")
	}
	if strings.TrimSpace(where) == "" {
		return nil, fmt.Errorf("update requires a WHERE clause")
	}
	if len(args) < len(cols) {
		return nil, fmt.Errorf("update requires values for all columns")
	}

	sets := make([]string, len(cols))
	for i, col := range cols {
		sets[i] = "`" + col + "` = ?"
	}

	query := "UPDATE `" + table + "` SET " + strings.Join(sets, ", ") + " WHERE " + where
	return conn.ExecContext(ctx, query, args...)
}

func Delete(ctx context.Context, conn *sql.DB, table string, where string, args ...any) (sql.Result, error) {
	if strings.TrimSpace(where) == "" {
		return nil, fmt.Errorf("delete requires a WHERE clause")
	}

	query := "DELETE FROM `" + table + "` WHERE " + where
	return conn.ExecContext(ctx, query, args...)
}
