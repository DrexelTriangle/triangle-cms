package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const developingStoriesSettingKey = "developing_stories_titles"

func GetDevelopingStories(ctx context.Context, conn *sql.DB) ([]string, error) {
	raw, found, err := readSettingRaw(ctx, conn, developingStoriesSettingKey)
	if err != nil {
		return nil, err
	}
	if !found {
		return []string{}, nil
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

	return writeSettingRaw(ctx, conn, developingStoriesSettingKey, string(payload))
}
