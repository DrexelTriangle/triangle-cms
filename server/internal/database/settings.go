package database

import (
	"context"
	"database/sql"
	"strconv"
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
//
// These hold the MANUAL banner only. The banner an editor raises by flagging
// an article is not stored anywhere: it is derived from the article on read,
// so it appears exactly when the article publishes and disappears when the
// flag comes off.
const (
	keyBreakingNewsEnabled = "breaking_news_enabled"
	keyBreakingNewsText    = "breaking_news_text"
	keyBreakingNewsWindow  = "breaking_news_window_hours"
)

// How long a flagged article keeps the banner after it publishes.
//
// Zero means no limit, and that is the default: the banner comes down when the
// editor unticks the article, and nothing takes it down on their behalf. An
// admin can set a limit in Settings if they want one, but it is opt-in --
// a banner vanishing on a timer nobody chose would be its own surprise.
const (
	breakingNewsWindowUnlimited = 0
	maxBreakingNewsWindowHours  = 24 * 7
)

// NormalizeBreakingNewsWindow clamps a requested window into the supported
// range. A zero or negative value is "no limit".
func NormalizeBreakingNewsWindow(hours int) int {
	if hours <= 0 {
		return breakingNewsWindowUnlimited
	}
	if hours > maxBreakingNewsWindowHours {
		return maxBreakingNewsWindowHours
	}
	return hours
}

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

// GetBreakingNews returns the banner the public site should render.
//
// It is the effective half of GetBreakingNewsState -- the homepage only needs
// to know whether to show a banner and what it says, not which of the two
// sources produced it.
func GetBreakingNews(ctx context.Context, conn *sql.DB) (models.BreakingNewsSettings, error) {
	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		return models.BreakingNewsSettings{}, err
	}
	return state.BreakingNewsSettings, nil
}

// GetBreakingNewsState resolves the banner from both of its sources.
//
// A published article flagged breaking wins over the manual banner: it is the
// more specific and more recent signal, and the newsroom flow the flag exists
// for is "this story is the breaking story now". When two are live the newest
// one holds the banner, since there is only one banner to hold.
func GetBreakingNewsState(ctx context.Context, conn *sql.DB) (models.BreakingNewsState, error) {
	manual, err := getManualBreakingNews(ctx, conn)
	if err != nil {
		return models.BreakingNewsState{}, err
	}

	window, err := GetBreakingNewsWindow(ctx, conn)
	if err != nil {
		return models.BreakingNewsState{}, err
	}

	state := models.BreakingNewsState{
		BreakingNewsSettings: manual,
		Source:               models.BreakingNewsSourceManual,
		Manual:               manual,
		WindowHours:          window,
	}
	if !manual.Enabled {
		state.Source = models.BreakingNewsSourceNone
	}

	slug, title, err := latestBreakingArticle(ctx, conn, window)
	if err != nil {
		return models.BreakingNewsState{}, err
	}
	if title != "" {
		state.BreakingNewsSettings = models.BreakingNewsSettings{
			Enabled:     true,
			Text:        title,
			ArticleSlug: slug,
		}
		state.Source = models.BreakingNewsSourceArticle
		state.ArticleTitle = title
	}

	return state, nil
}

// GetBreakingNewsWindow returns the configured banner window in hours.
func GetBreakingNewsWindow(ctx context.Context, conn *sql.DB) (int, error) {
	raw, err := getSetting(ctx, conn, keyBreakingNewsWindow, "")
	if err != nil {
		return 0, err
	}
	hours, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		// A blank or corrupt value is a missing setting, not a reason to fail
		// the homepage; unset is the documented default anyway.
		return breakingNewsWindowUnlimited, nil
	}
	return NormalizeBreakingNewsWindow(hours), nil
}

func getManualBreakingNews(ctx context.Context, conn *sql.DB) (models.BreakingNewsSettings, error) {
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

// latestBreakingArticle returns the newest published article flagged breaking,
// or empty strings when none is.
//
// The published predicate is the same one every other public read uses, which
// is the whole point: a scheduled article starts driving the banner at the
// same instant it starts being readable, with no separate publish hook to
// drift out of step with it.
//
// windowHours of 0 is no limit, in which case the age clause is left off the
// query rather than passed as a sentinel -- `INTERVAL 0 HOUR` would exclude
// every article instead of including them all.
func latestBreakingArticle(ctx context.Context, conn *sql.DB, windowHours int) (string, string, error) {
	query := `
		SELECT slug, title
		FROM articles
		WHERE breaking_news = 1
		  AND pub_date IS NOT NULL
		  AND pub_date <= UTC_TIMESTAMP()
		  AND archived_at IS NULL
	`
	args := []any{}
	if windowHours > 0 {
		query += "  AND pub_date > UTC_TIMESTAMP() - INTERVAL ? HOUR\n"
		args = append(args, windowHours)
	}
	query += "\tORDER BY pub_date DESC, id DESC\n\tLIMIT 1"

	var slug, title sql.NullString
	err := conn.QueryRowContext(ctx, query, args...).Scan(&slug, &title)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(slug.String), strings.TrimSpace(title.String), nil
}

// SetBreakingNews persists the manual breaking-news banner and its window.
func SetBreakingNews(ctx context.Context, conn *sql.DB, s models.BreakingNewsSettings, windowHours int) error {
	enabled := "false"
	if s.Enabled {
		enabled = "true"
	}
	if err := setSetting(ctx, conn, keyBreakingNewsEnabled, enabled); err != nil {
		return err
	}
	if err := setSetting(ctx, conn, keyBreakingNewsText, strings.TrimSpace(s.Text)); err != nil {
		return err
	}
	return setSetting(ctx, conn, keyBreakingNewsWindow, strconv.Itoa(NormalizeBreakingNewsWindow(windowHours)))
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
