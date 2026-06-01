package database

import (
	"context"
	"database/sql"
	"strings"
)

const defaultSiteTitle = "The Triangle"

func EnsureSettingsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cms_settings (
			key_name   VARCHAR(128) NOT NULL PRIMARY KEY,
			value_text TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES ('site_title', ?)
		ON DUPLICATE KEY UPDATE key_name = key_name
	`, defaultSiteTitle)
	if err != nil {
		return err
	}

	return err
}

func GetSiteTitle(ctx context.Context, conn *sql.DB) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = 'site_title' LIMIT 1").Scan(&value)
	if err == sql.ErrNoRows {
		return defaultSiteTitle, nil
	}
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultSiteTitle, nil
	}
	return value, nil
}

func SetSiteTitle(ctx context.Context, conn *sql.DB, title string) error {
	normalized := strings.TrimSpace(title)
	if normalized == "" {
		normalized = defaultSiteTitle
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES ('site_title', ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, normalized)
	return err
}
