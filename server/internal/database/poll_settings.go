package database

import (
	"context"
	"database/sql"
	"strings"
)

func EnsurePollSettings(ctx context.Context, conn *sql.DB) error {
	return nil
}

func GetPollTitle(ctx context.Context, conn *sql.DB) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = 'poll_title' LIMIT 1").Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func SetPollTitle(ctx context.Context, conn *sql.DB, title string) error {
	normalized := strings.TrimSpace(title)
	_, err := conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES ('poll_title', ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, normalized)
	return err
}
