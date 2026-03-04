package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func Select(ctx context.Context, db *sql.DB, table string, columns []string, where string, args ...any) (*sql.Rows, error) {
	cols := "*"
	if len(columns) > 0 {
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = quoteIdent(c)
		}
		cols = strings.Join(quoted, ", ")
	}
	query := fmt.Sprintf("SELECT %s FROM %s", cols, quoteIdent(table))
	if where != "" {
		query += " WHERE " + where
	}
	return db.QueryContext(ctx, query, args...)
}

func Insert(ctx context.Context, db *sql.DB, table string, cols []string, args ...any) (sql.Result, error) {
	ph := strings.Repeat("?, ", len(cols))
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table),
		strings.Join(quoted, ", "),
		ph[:len(ph)-2],
	)
	return db.ExecContext(ctx, query, args...)
}

func Update(ctx context.Context, db *sql.DB, table string, setCols []string, where string, args ...any) (sql.Result, error) {
	parts := make([]string, len(setCols))
	for i, c := range setCols {
		parts[i] = quoteIdent(c) + " = ?"
	}
	query := fmt.Sprintf("UPDATE %s SET %s", quoteIdent(table), strings.Join(parts, ", "))
	if where != "" {
		query += " WHERE " + where
	}
	return db.ExecContext(ctx, query, args...)
}

func Delete(ctx context.Context, db *sql.DB, table string, where string, args ...any) (sql.Result, error) {
	query := fmt.Sprintf("DELETE FROM %s", quoteIdent(table))
	if where != "" {
		query += " WHERE " + where
	}
	return db.ExecContext(ctx, query, args...)
}

func buildDSN(dbName, user, password, host string, port int) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true", user, password, host, port, dbName)
}

func InitializeConnection(ctx context.Context, dbName, user, password, host string, port int) (*sql.DB, error) {
	dataSourceName := buildDSN(dbName, user, password, host, port)
	db, err := sql.Open("mysql", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}

	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return db, nil
}
