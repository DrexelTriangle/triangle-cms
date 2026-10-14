package database

import (
	"strings"
	"testing"
	"time"

	"server/internal/models"
)

func TestNormalizeExhibitSpotlightsTrimsAndDrops(t *testing.T) {
	entries := NormalizeExhibitSpotlights([]models.ExhibitSpotlight{
		{Slug: " haddy ", Title: "  Hadrosaurus  ", Body: " text ", FunFacts: []string{" a ", "", "b"}, Sources: []string{"", " https://x "}},
		{Slug: "empty"},
	})
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Slug != "haddy" || e.Title != "Hadrosaurus" || e.Body != "text" {
		t.Fatalf("trim failed: %+v", e)
	}
	if len(e.FunFacts) != 2 || e.FunFacts[0] != "a" {
		t.Fatalf("fun_facts = %v, want [a b]", e.FunFacts)
	}
	if len(e.Sources) != 1 || e.Sources[0] != "https://x" {
		t.Fatalf("sources = %v, want [https://x]", e.Sources)
	}
}

func TestNormalizeExhibitSpotlightsNilSlicesBecomeEmpty(t *testing.T) {
	entries := NormalizeExhibitSpotlights([]models.ExhibitSpotlight{{Title: "T"}})
	if entries[0].FunFacts == nil || entries[0].Sources == nil {
		t.Fatalf("slices must be non-nil so JSON renders [] not null: %+v", entries[0])
	}
}

func TestTodaysExhibitSpotlightUsesNewYorkDate(t *testing.T) {
	entries := []models.ExhibitSpotlight{
		{Slug: "oct8", Title: "Oct 8", FeatureDate: "2026-10-08", Enabled: true},
		{Slug: "oct9", Title: "Oct 9", FeatureDate: "2026-10-09", Enabled: true},
	}
	// 03:30 UTC on Oct 9 is 23:30 EDT on Oct 8.
	now := time.Date(2026, 10, 9, 3, 30, 0, 0, time.UTC)
	got := TodaysExhibitSpotlight(entries, now)
	if got == nil || got.Slug != "oct8" {
		t.Fatalf("got %+v, want oct8", got)
	}
	// 04:30 UTC on Oct 9 is 00:30 EDT on Oct 9.
	now = time.Date(2026, 10, 9, 4, 30, 0, 0, time.UTC)
	got = TodaysExhibitSpotlight(entries, now)
	if got == nil || got.Slug != "oct9" {
		t.Fatalf("got %+v, want oct9", got)
	}
}

func TestTodaysExhibitSpotlightSkipsDisabledAndBadDates(t *testing.T) {
	now := time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)
	entries := []models.ExhibitSpotlight{
		{Slug: "off", Title: "Off", FeatureDate: "2026-10-08", Enabled: false},
		{Slug: "bad", Title: "Bad", FeatureDate: "10/08/2026", Enabled: true},
		{Slug: "short", Title: "Short", FeatureDate: "2026-10-8", Enabled: true},
	}
	if got := TodaysExhibitSpotlight(entries, now); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestTodaysExhibitSpotlightFirstWinsOnTie(t *testing.T) {
	now := time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)
	entries := []models.ExhibitSpotlight{
		{Slug: "a", Title: "A", FeatureDate: "2026-10-08", Enabled: true},
		{Slug: "b", Title: "B", FeatureDate: "2026-10-08", Enabled: true},
	}
	if got := TodaysExhibitSpotlight(entries, now); got == nil || got.Slug != "a" {
		t.Fatalf("got %+v, want a", got)
	}
}

func TestTodaysExhibitSpotlightEmptyList(t *testing.T) {
	if got := TodaysExhibitSpotlight(nil, time.Now()); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestExhibitSpotlightPayloadRejectsOversize(t *testing.T) {
	big := make([]models.ExhibitSpotlight, 30)
	for i := range big {
		big[i] = models.ExhibitSpotlight{Title: "T", Body: strings.Repeat("x", 2500)}
	}
	if _, err := exhibitSpotlightPayload(big); err == nil {
		t.Fatal("want error when the JSON would not fit the TEXT column")
	}
	if _, err := exhibitSpotlightPayload(big[:1]); err != nil {
		t.Fatalf("err = %v, want nil for a small payload", err)
	}
}

func TestNormalizeExhibitSpotlightsTrimsLinkURL(t *testing.T) {
	entries := NormalizeExhibitSpotlights([]models.ExhibitSpotlight{{Title: "T", LinkURL: " https://ansp.org "}})
	if entries[0].LinkURL != "https://ansp.org" {
		t.Fatalf("link_url = %q, want trimmed", entries[0].LinkURL)
	}
}
