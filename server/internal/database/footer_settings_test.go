package database

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"server/internal/models"
)

func TestNormalizeFooterColumns_TrimsAndDropsEmptyEntries(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryHeading, Label: "  News  ", Href: "  /news  "},
			{Kind: models.FooterEntryLink, Label: "   ", Href: "/campus"},
			{Kind: models.FooterEntryLink, Label: "Campus", Href: "/campus"},
		}},
	})

	if len(columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(columns))
	}
	entries := columns[0].Entries
	if len(entries) != 2 {
		t.Fatalf("expected the unlabelled entry to be dropped, got %d entries", len(entries))
	}
	if entries[0].Label != "News" || entries[0].Href != "/news" {
		t.Errorf("expected the heading to be trimmed, got %+v", entries[0])
	}
}

func TestNormalizeFooterColumns_DefaultsUnknownKindToLink(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{{Kind: "banner", Label: "Staff", Href: "/staff"}}},
	})

	if len(columns) != 1 || len(columns[0].Entries) != 1 {
		t.Fatalf("expected a single entry, got %+v", columns)
	}
	if got := columns[0].Entries[0].Kind; got != models.FooterEntryLink {
		t.Errorf("expected kind %q, got %q", models.FooterEntryLink, got)
	}
}

// A spacer renders as a blank line, so a column holding nothing else would show
// as an empty gap in the footer.
func TestNormalizeFooterColumns_DropsColumnsWithoutContent(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{{Kind: models.FooterEntrySpacer}}},
		{Entries: nil},
		{Entries: []models.FooterEntry{{Kind: models.FooterEntryHeading, Label: "Sports", Href: "/sports"}}},
	})

	if len(columns) != 1 {
		t.Fatalf("expected only the column with content to survive, got %d", len(columns))
	}
	if columns[0].Entries[0].Label != "Sports" {
		t.Errorf("kept the wrong column: %+v", columns[0])
	}
}

// Spacers carry no destination; a stored label or href would be dead data the
// public site must then decide whether to render.
func TestNormalizeFooterColumns_StripsSpacerContent(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryHeading, Label: "Opinion", Href: "/opinion"},
			{Kind: models.FooterEntrySpacer, Label: "leftover", Href: "/stale", NewTab: true},
		}},
	})

	spacer := columns[0].Entries[1]
	if spacer.Label != "" || spacer.Href != "" || spacer.NewTab {
		t.Errorf("expected the spacer to be stripped, got %+v", spacer)
	}
}

// The default menu is what an untouched install serves, so it must survive
// normalization unchanged.
func TestDefaultFooterColumns_SurviveNormalization(t *testing.T) {
	defaults := staticFooterColumns()
	normalized := normalizeFooterColumns(defaults)

	if len(normalized) != len(defaults) {
		t.Fatalf("expected %d columns, got %d", len(defaults), len(normalized))
	}
	for i, column := range normalized {
		if len(column.Entries) != len(defaults[i].Entries) {
			t.Errorf("column %d: expected %d entries, got %d", i, len(defaults[i].Entries), len(column.Entries))
		}
	}
}

// TestBuildFooterColumns_FallsBackWithoutTaxonomy is the safety net: the footer
// is on every page, so a taxonomy it cannot read has to leave the old links
// standing rather than render an empty nav.
func TestBuildFooterColumns_FallsBackWithoutTaxonomy(t *testing.T) {
	got := buildFooterColumns(context.Background(), nil)

	want := staticFooterColumns()
	if len(got) != len(want) {
		t.Fatalf("got %d columns, want the %d static ones", len(got), len(want))
	}
	if got[0].Entries[0].Label != "About" {
		t.Errorf("first entry = %q, want the static About heading", got[0].Entries[0].Label)
	}
}

// TestFooterTemplate_KeepsTheNonTaxonomyLinks guards the entries that must NOT
// be generated. Three of the Special Editions links deliberately point away
// from their own taxonomy page, and Graduation is a section that appears in no
// other column, so generating that block would quietly redirect four links.
func TestFooterTemplate_KeepsTheNonTaxonomyLinks(t *testing.T) {
	var literals []models.FooterEntry
	for _, column := range footerTemplate() {
		for _, part := range column {
			if part.Section == "" {
				literals = append(literals, part.Entries...)
			}
		}
	}

	for _, want := range []struct{ label, href string }{
		{"The Rectangle", "https://therectangle.org"},
		{"Welcome Week", "/search?s=Welcome%20Week"},
		{"100 Year Anniversary", "/one-hundred"},
		{"Graduation", "/graduation"},
		{"Contact Us", "/contact"},
	} {
		found := false
		for _, entry := range literals {
			if entry.Label == want.label && entry.Href == want.href {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q -> %q is no longer a literal footer entry; generating it would change where it points", want.label, want.href)
		}
	}
}

// One instant, one spelling: the editor's browser sends its own offset, and the
// public site compares schedules as instants, so storage settles on UTC.
func TestCanonicalFooterVisibleFrom(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{" 2026-10-10T09:00:00-04:00 ", "2026-10-10T13:00:00Z"},
		{"2026-10-10T13:00:00Z", "2026-10-10T13:00:00Z"},
		{"2026-10-10T13:00:00.000Z", "2026-10-10T13:00:00Z"},
	} {
		got, err := CanonicalFooterVisibleFrom(tc.in)
		if err != nil {
			t.Errorf("CanonicalFooterVisibleFrom(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CanonicalFooterVisibleFrom(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Zoneless is what a datetime-local value is; reading it as UTC would
	// launch a 9am Philadelphia link at 5am.
	for _, bad := range []string{"2026-10-10T09:00", "2026-10-10", "next tuesday"} {
		if got, err := CanonicalFooterVisibleFrom(bad); err == nil {
			t.Errorf("CanonicalFooterVisibleFrom(%q) = %q, want an error", bad, got)
		}
	}
}

// The CMS serves a future schedule as-is; hiding the entry is the public
// site's call, so normalization must not drop or blank it.
func TestNormalizeFooterColumns_KeepsAFutureSchedule(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryHeading, Label: "Comics & Puzzles", Href: "/comics-puzzles"},
			{Kind: models.FooterEntryLink, Label: "Games", Href: "/games", VisibleFrom: " 2099-01-01T09:00:00-05:00 "},
		}},
	})

	if got := columns[0].Entries[1].VisibleFrom; got != "2099-01-01T14:00:00Z" {
		t.Errorf("expected the schedule to be kept in UTC, got %q", got)
	}
	if got := columns[0].Entries[0].VisibleFrom; got != "" {
		t.Errorf("an unscheduled entry gained a schedule: %q", got)
	}
}

// A malformed value can only be in storage if it predates validation or was
// written by hand. Clearing it shows the link; failing would empty the footer.
func TestNormalizeFooterColumns_ClearsAMalformedStoredSchedule(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryLink, Label: "Games", Href: "/games", VisibleFrom: "soon"},
		}},
	})

	if len(columns) != 1 || len(columns[0].Entries) != 1 {
		t.Fatalf("expected the entry to survive, got %+v", columns)
	}
	if got := columns[0].Entries[0].VisibleFrom; got != "" {
		t.Errorf("expected the malformed schedule to be cleared, got %q", got)
	}
}

func TestNormalizeFooterColumns_StripsASpacerSchedule(t *testing.T) {
	columns := normalizeFooterColumns([]models.FooterColumn{
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryHeading, Label: "Opinion", Href: "/opinion"},
			{Kind: models.FooterEntrySpacer, VisibleFrom: "2099-01-01T00:00:00Z"},
		}},
	})

	if got := columns[0].Entries[1].VisibleFrom; got != "" {
		t.Errorf("expected the spacer's schedule to be stripped, got %q", got)
	}
}

// Unscheduled entries must serialize exactly as before the field existed, so
// the stored production footer and older public-site builds see no change.
func TestFooterEntryJSON_OmitsAnEmptySchedule(t *testing.T) {
	plain, err := json.Marshal(models.FooterEntry{Kind: models.FooterEntryLink, Label: "Staff", Href: "/staff"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "visible_from") {
		t.Errorf("unscheduled entry serialized a schedule: %s", plain)
	}

	scheduled, err := json.Marshal(models.FooterEntry{Kind: models.FooterEntryLink, Label: "Games", Href: "/games", VisibleFrom: "2099-01-01T14:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scheduled), `"visible_from":"2099-01-01T14:00:00Z"`) {
		t.Errorf("scheduled entry lost its schedule: %s", scheduled)
	}
}
