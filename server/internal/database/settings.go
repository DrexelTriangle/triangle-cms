package database

import (
	"context"
	"database/sql"
	"strings"

	"server/internal/models"
)

const defaultSiteTitle = "The Triangle"

// SEO settings keys and their defaults, stored in the cms_settings key-value table.
const (
	keySEOOGTitle       = "seo_og_title"
	keySEOOGDescription = "seo_og_description"
	keySEOSitemapURL    = "seo_sitemap_url"
	keySEORobotsURL     = "seo_robots_url"
)

// Breaking-news banner settings keys, stored in the cms_settings key-value table.
const (
	keyBreakingNewsEnabled = "breaking_news_enabled"
	keyBreakingNewsText    = "breaking_news_text"
)

var seoSettingDefaults = map[string]string{
	keySEOOGTitle:       "The Triangle | Drexel University's Independent Student Newspaper",
	keySEOOGDescription: "Award-winning independent student journalism at Drexel University since 1925.",
	keySEOSitemapURL:    "https://www.thetriangle.org/sitemap.xml",
	keySEORobotsURL:     "https://www.thetriangle.org/robots.txt",
}

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
	return getSetting(ctx, conn, "site_title", defaultSiteTitle)
}

func SetSiteTitle(ctx context.Context, conn *sql.DB, title string) error {
	normalized := strings.TrimSpace(title)
	if normalized == "" {
		normalized = defaultSiteTitle
	}
	return setSetting(ctx, conn, "site_title", normalized)
}

// GetBreakingNews returns the breaking-news banner settings, defaulting to
// disabled with empty text when unset.
func GetBreakingNews(ctx context.Context, conn *sql.DB) (models.BreakingNewsSettings, error) {
	enabled, err := getSetting(ctx, conn, keyBreakingNewsEnabled, "false")
	if err != nil {
		return models.BreakingNewsSettings{}, err
	}
	// getSetting falls back when the stored value is blank, so an empty banner
	// text is read raw rather than through getSetting's fallback handling.
	text, _, err := readSettingRaw(ctx, conn, keyBreakingNewsText)
	if err != nil {
		return models.BreakingNewsSettings{}, err
	}
	return models.BreakingNewsSettings{
		Enabled: enabled == "true",
		Text:    strings.TrimSpace(text),
	}, nil
}

// SetBreakingNews persists the breaking-news banner settings.
func SetBreakingNews(ctx context.Context, conn *sql.DB, s models.BreakingNewsSettings) error {
	enabled := "false"
	if s.Enabled {
		enabled = "true"
	}
	if err := setSetting(ctx, conn, keyBreakingNewsEnabled, enabled); err != nil {
		return err
	}
	return setSetting(ctx, conn, keyBreakingNewsText, strings.TrimSpace(s.Text))
}

// getSetting reads a single cms_settings value, returning fallback when the key
// is absent or stored empty.
func getSetting(ctx context.Context, conn *sql.DB, key, fallback string) (string, error) {
	value, found, err := readSettingRaw(ctx, conn, key)
	if err != nil {
		return "", err
	}
	if !found {
		return fallback, nil
	}
	if value = strings.TrimSpace(value); value == "" {
		return fallback, nil
	}
	return value, nil
}

func setSetting(ctx context.Context, conn *sql.DB, key, value string) error {
	return writeSettingRaw(ctx, conn, key, value)
}

// GetSEOSettings returns the site-wide SEO/social defaults, falling back to the
// seeded defaults for any key that has not been customized.
func GetSEOSettings(ctx context.Context, conn *sql.DB) (models.SEOSettings, error) {
	var s models.SEOSettings
	fields := []struct {
		key string
		dst *string
	}{
		{keySEOOGTitle, &s.OGTitle},
		{keySEOOGDescription, &s.OGDescription},
		{keySEOSitemapURL, &s.SitemapURL},
		{keySEORobotsURL, &s.RobotsURL},
	}
	for _, f := range fields {
		value, err := getSetting(ctx, conn, f.key, seoSettingDefaults[f.key])
		if err != nil {
			return models.SEOSettings{}, err
		}
		*f.dst = value
	}
	return s, nil
}

// SetSEOSettings persists the site-wide SEO/social defaults. Empty fields fall
// back to their seeded defaults so a saved record is never blank.
func SetSEOSettings(ctx context.Context, conn *sql.DB, s models.SEOSettings) error {
	fields := []struct {
		key   string
		value string
	}{
		{keySEOOGTitle, s.OGTitle},
		{keySEOOGDescription, s.OGDescription},
		{keySEOSitemapURL, s.SitemapURL},
		{keySEORobotsURL, s.RobotsURL},
	}
	for _, f := range fields {
		value := strings.TrimSpace(f.value)
		if value == "" {
			value = seoSettingDefaults[f.key]
		}
		if err := setSetting(ctx, conn, f.key, value); err != nil {
			return err
		}
	}
	return nil
}
