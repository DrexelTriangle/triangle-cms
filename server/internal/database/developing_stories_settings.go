package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const developingStoriesSettingKey = "developing_stories_titles"

// DevelopingStory is one row of the homepage rail. The setting used to hold a
// bare JSON array of titles, so editors folded the blurb into the title with a
// dash; Description gives it its own field and GetDevelopingStories still reads
// the old shape.
type DevelopingStory struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func GetDevelopingStories(ctx context.Context, conn *sql.DB) ([]DevelopingStory, error) {
	raw, found, err := readSettingRaw(ctx, conn, developingStoriesSettingKey)
	if err != nil {
		return nil, err
	}
	if !found {
		return []DevelopingStory{}, nil
	}

	return parseDevelopingStories(raw), nil
}

func parseDevelopingStories(raw string) []DevelopingStory {
	if strings.TrimSpace(raw) == "" {
		return []DevelopingStory{}
	}

	var elements []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &elements); err != nil {
		return []DevelopingStory{}
	}

	parsed := make([]DevelopingStory, 0, len(elements))
	for _, element := range elements {
		// Legacy rows are plain strings; current rows are objects.
		var title string
		if err := json.Unmarshal(element, &title); err == nil {
			parsed = append(parsed, DevelopingStory{Title: title})
			continue
		}
		var story DevelopingStory
		if err := json.Unmarshal(element, &story); err != nil {
			continue
		}
		parsed = append(parsed, story)
	}

	return normalizeDevelopingStories(parsed)
}

func SetDevelopingStories(ctx context.Context, conn *sql.DB, stories []DevelopingStory) error {
	payload, err := json.Marshal(normalizeDevelopingStories(stories))
	if err != nil {
		return err
	}

	return writeSettingRaw(ctx, conn, developingStoriesSettingKey, string(payload))
}

// normalizeDevelopingStories trims both fields, drops untitled entries and
// keeps the first of any duplicate title, so the title stays usable as the id
// the add/update/delete endpoints address a story by.
func normalizeDevelopingStories(stories []DevelopingStory) []DevelopingStory {
	normalized := make([]DevelopingStory, 0, len(stories))
	seen := make(map[string]struct{}, len(stories))
	for _, story := range stories {
		title := strings.TrimSpace(story.Title)
		if title == "" {
			continue
		}
		if _, ok := seen[title]; ok {
			continue
		}
		seen[title] = struct{}{}
		normalized = append(normalized, DevelopingStory{
			Title:       title,
			Description: strings.TrimSpace(story.Description),
		})
	}
	return normalized
}
