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
	value, _, err := readSettingRaw(ctx, conn, "poll_title")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func SetPollTitle(ctx context.Context, conn *sql.DB, title string) error {
	normalized := strings.TrimSpace(title)
	return writeSettingRaw(ctx, conn, "poll_title", normalized)
}
