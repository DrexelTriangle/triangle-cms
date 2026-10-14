package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"server/internal/models"
)

const exhibitSpotlightSettingKey = "exhibit_spotlights"

// Editors schedule entries by calendar date in the newsroom's zone, so the
// match must not drift when the server runs in UTC.
var exhibitSpotlightZone = mustLoadExhibitZone("America/New_York")

func mustLoadExhibitZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("exhibit spotlight: load zone " + name + ": " + err.Error())
	}
	return loc
}

func GetExhibitSpotlights(ctx context.Context, conn *sql.DB) ([]models.ExhibitSpotlight, error) {
	raw, found, err := readSettingRaw(ctx, conn, exhibitSpotlightSettingKey)
	if err != nil {
		return nil, err
	}
	if !found || strings.TrimSpace(raw) == "" {
		return []models.ExhibitSpotlight{}, nil
	}
	var parsed []models.ExhibitSpotlight
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		slog.Error("exhibit spotlights: stored JSON does not parse; serving none", "error", err)
		return []models.ExhibitSpotlight{}, nil
	}
	return NormalizeExhibitSpotlights(parsed), nil
}

func SetExhibitSpotlights(ctx context.Context, conn *sql.DB, entries []models.ExhibitSpotlight) error {
	payload, err := exhibitSpotlightPayload(entries)
	if err != nil {
		return err
	}
	return writeSettingRaw(ctx, conn, exhibitSpotlightSettingKey, string(payload))
}

// cms_settings.value_text is TEXT (65,535 bytes). A silently truncated row
// would fail to parse on read and make the whole series vanish, so refuse
// anything that would not fit with room to spare.
const maxExhibitSpotlightPayloadBytes = 60000

var ErrExhibitSpotlightsTooLarge = errors.New("exhibit spotlights payload too large")

func exhibitSpotlightPayload(entries []models.ExhibitSpotlight) ([]byte, error) {
	payload, err := json.Marshal(NormalizeExhibitSpotlights(entries))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxExhibitSpotlightPayloadBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d; shorten or remove entries", ErrExhibitSpotlightsTooLarge, len(payload), maxExhibitSpotlightPayloadBytes)
	}
	return payload, nil
}

// NormalizeExhibitSpotlights trims every string, drops blank facts and
// sources, and drops entries with neither a title nor a body. Disabled drafts
// are kept so editors do not lose work.
func NormalizeExhibitSpotlights(entries []models.ExhibitSpotlight) []models.ExhibitSpotlight {
	out := make([]models.ExhibitSpotlight, 0, len(entries))
	for _, e := range entries {
		next := models.ExhibitSpotlight{
			Slug:           strings.TrimSpace(e.Slug),
			FeatureDate:    strings.TrimSpace(e.FeatureDate),
			Enabled:        e.Enabled,
			Title:          strings.TrimSpace(e.Title),
			Kicker:         strings.TrimSpace(e.Kicker),
			Body:           strings.TrimSpace(e.Body),
			Tenure:         strings.TrimSpace(e.Tenure),
			FunFacts:       trimNonEmpty(e.FunFacts),
			LinkURL:        strings.TrimSpace(e.LinkURL),
			ImageURL:       strings.TrimSpace(e.ImageURL),
			ImageAlt:       strings.TrimSpace(e.ImageAlt),
			ImageCredit:    strings.TrimSpace(e.ImageCredit),
			ImageCreditURL: strings.TrimSpace(e.ImageCreditURL),
			Sources:        trimNonEmpty(e.Sources),
		}
		if next.Title == "" && next.Body == "" {
			continue
		}
		out = append(out, next)
	}
	return out
}

// TodaysExhibitSpotlight returns the first enabled entry whose feature_date is
// today in New York, or nil. A date that does not parse as YYYY-MM-DD never
// matches.
func TodaysExhibitSpotlight(entries []models.ExhibitSpotlight, now time.Time) *models.ExhibitSpotlight {
	today := now.In(exhibitSpotlightZone).Format("2006-01-02")
	for i := range entries {
		e := &entries[i]
		if !e.Enabled {
			continue
		}
		if _, err := time.Parse("2006-01-02", e.FeatureDate); err != nil {
			continue
		}
		if e.FeatureDate == today {
			return e
		}
	}
	return nil
}

func trimNonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
