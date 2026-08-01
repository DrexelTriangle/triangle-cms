package database

import (
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
	defaults := defaultFooterColumns()
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
