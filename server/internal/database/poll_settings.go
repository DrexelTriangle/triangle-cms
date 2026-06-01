package database

import (
	"context"
	"database/sql"
	"strings"
)

const defaultPollTitle = "What is your favorite section of The Triangle?"

func EnsurePollSettings(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES ('poll_title', ?)
		ON DUPLICATE KEY UPDATE key_name = key_name
	`, defaultPollTitle)
	return err
}

func GetPollTitle(ctx context.Context, conn *sql.DB) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = 'poll_title' LIMIT 1").Scan(&value)
	if err == sql.ErrNoRows {
		return defaultPollTitle, nil
	}
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultPollTitle, nil
	}
	return value, nil
}

func SetPollTitle(ctx context.Context, conn *sql.DB, title string) error {
	normalized := strings.TrimSpace(title)
	if normalized == "" {
		normalized = defaultPollTitle
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES ('poll_title', ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, normalized)
	return err
}
