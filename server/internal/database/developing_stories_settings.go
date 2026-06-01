package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const developingStoriesSettingKey = "developing_stories_titles"

func GetDevelopingStories(ctx context.Context, conn *sql.DB) ([]string, error) {
	var raw string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = ? LIMIT 1", developingStoriesSettingKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	var parsed []string
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return []string{}, nil
	}

	normalized := make([]string, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
	for _, item := range parsed {
		title := strings.TrimSpace(item)
		if title == "" {
			continue
		}
		if _, ok := seen[title]; ok {
			continue
		}
		seen[title] = struct{}{}
		normalized = append(normalized, title)
	}

	return normalized, nil
}

func SetDevelopingStories(ctx context.Context, conn *sql.DB, stories []string) error {
	normalized := make([]string, 0, len(stories))
	seen := make(map[string]struct{}, len(stories))
	for _, item := range stories {
		title := strings.TrimSpace(item)
		if title == "" {
			continue
		}
		if _, ok := seen[title]; ok {
			continue
		}
		seen[title] = struct{}{}
		normalized = append(normalized, title)
	}

	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, developingStoriesSettingKey, string(payload))
	return err
}
