package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"server/internal/models"
)

const homepageCarouselSettingKey = "homepage_carousel"

// Default slide images live on the media filesystem under a `scalene/` prefix
// rather than in Scalene's bundled public/images. Bundled paths could not be
// changed from the CMS -- editing the Podcast banner meant a Scalene commit and
// redeploy -- which defeats the point of a CMS-editable carousel, and a rename
// on the Scalene side silently broke the slide.
//
// The prefix sits under wp-content/uploads because that is the only tree Nginx
// serves (see deploy/nginx/triangle-cms.conf); it is outside the YYYY/MM layout
// so the media reindex and the legacy WP corpus stay visibly separate.
//
// That location is served with 30-day immutable caching, which assumes a
// filename pins its bytes. To swap one of these banners, upload under a NEW
// name and repoint the slide -- overwriting in place leaves the old image at
// the edge for up to a month.
const defaultCarouselImageBase = "https://delta.thetriangle.org/wp-content/uploads/scalene"

var defaultHomepageCarouselSlides = []models.HomepageCarouselSlide{
	{
		Enabled:     true,
		Title:       "100 Years of The Triangle",
		LinkURL:     "/one-hundred",
		TextColor:   "#ffffff",
		DesktopOnly: true,
	},
	{
		Enabled:         true,
		Title:           "Podcast",
		LinkURL:         "https://tr.ee/bqz8s-E4NK",
		ImageURL:        defaultCarouselImageBase + "/PodcastBanner.webp",
		BackgroundColor: "#acd4f4",
		TextColor:       "#ffffff",
	},
	{
		Enabled:         true,
		Title:           "Classifieds",
		LinkURL:         "/classifieds",
		ImageURL:        defaultCarouselImageBase + "/classifiedsBannerNew.webp",
		BackgroundColor: "#E5E7EB",
		TextColor:       "#ffffff",
	},
	{
		Enabled:         true,
		Title:           "Apply to The Triangle",
		LinkURL:         "https://docs.google.com/forms/d/e/1FAIpQLScra_6sUenvmpIuQ5FjmMyWO0a2sz9z36HkrqfnYQvJGH9BGQ/viewform",
		ImageURL:        defaultCarouselImageBase + "/applyBannerNew.webp",
		BackgroundColor: "#275997",
		TextColor:       "#ffffff",
	},
}

func DefaultHomepageCarouselSlides() []models.HomepageCarouselSlide {
	return cloneHomepageCarouselSlides(defaultHomepageCarouselSlides)
}

func GetHomepageCarousel(ctx context.Context, conn *sql.DB) ([]models.HomepageCarouselSlide, error) {
	var raw string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = ? LIMIT 1", homepageCarouselSettingKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return DefaultHomepageCarouselSlides(), nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return DefaultHomepageCarouselSlides(), nil
	}

	var parsed []models.HomepageCarouselSlide
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return DefaultHomepageCarouselSlides(), nil
	}

	return NormalizeHomepageCarouselSlides(parsed), nil
}

func SetHomepageCarousel(ctx context.Context, conn *sql.DB, slides []models.HomepageCarouselSlide) error {
	normalized := NormalizeHomepageCarouselSlides(slides)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, homepageCarouselSettingKey, string(payload))
	return err
}

func PublishedHomepageCarousel(slides []models.HomepageCarouselSlide) []models.HomepageCarouselSlide {
	normalized := NormalizeHomepageCarouselSlides(slides)
	published := make([]models.HomepageCarouselSlide, 0, len(normalized))
	for _, slide := range normalized {
		if slide.Enabled {
			published = append(published, slide)
		}
	}
	return published
}

func NormalizeHomepageCarouselSlides(slides []models.HomepageCarouselSlide) []models.HomepageCarouselSlide {
	normalized := make([]models.HomepageCarouselSlide, 0, len(slides))
	for _, slide := range slides {
		next := models.HomepageCarouselSlide{
			Enabled:         slide.Enabled,
			Title:           strings.TrimSpace(slide.Title),
			LinkURL:         strings.TrimSpace(slide.LinkURL),
			ImageURL:        strings.TrimSpace(slide.ImageURL),
			BackgroundColor: strings.TrimSpace(slide.BackgroundColor),
			TextColor:       strings.TrimSpace(slide.TextColor),
			DesktopOnly:     slide.DesktopOnly,
		}
		if next.LinkURL == "" && next.ImageURL == "" && next.Title == "" {
			continue
		}
		if next.TextColor == "" {
			next.TextColor = "#ffffff"
		}
		normalized = append(normalized, next)
	}
	return normalized
}

func cloneHomepageCarouselSlides(slides []models.HomepageCarouselSlide) []models.HomepageCarouselSlide {
	cloned := make([]models.HomepageCarouselSlide, len(slides))
	copy(cloned, slides)
	return cloned
}
