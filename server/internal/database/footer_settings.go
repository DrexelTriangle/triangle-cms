package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"server/internal/models"
)

// The footer menu is a single JSON blob in cms_settings rather than its own
// table: it is one small document, read whole by the public site and written
// whole by the settings screen, and it never needs to be queried by parts.
const footerSettingKey = "footer_menu"

// defaultFooterColumns mirrors the footer the public site shipped hardcoded, so
// an untouched install serves exactly what it served before and the settings
// screen opens pre-populated instead of blank.
func defaultFooterColumns() []models.FooterColumn {
	link := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryLink, Label: label, Href: href}
	}
	external := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryLink, Label: label, Href: href, NewTab: true}
	}
	heading := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryHeading, Label: label, Href: href}
	}
	spacer := models.FooterEntry{Kind: models.FooterEntrySpacer}

	return []models.FooterColumn{
		{Entries: []models.FooterEntry{
			heading("About", "/about"),
			link("Contact Us", "/contact"),
			external("Join The Triangle", "https://docs.google.com/forms/d/e/1FAIpQLScra_6sUenvmpIuQ5FjmMyWO0a2sz9z36HkrqfnYQvJGH9BGQ/viewform"),
			link("Staff", "/staff"),
			link("Find-A-Triangle", "/find"),
			link("Photo Gallery", "/photo"),
			external("Print Archive", "https://drexel.primo.exlibrisgroup.com/discovery/collectionDiscovery?vid=01DRXU_INST:01DRXU&inst=01DRXU_INST&collectionId=81448731180004721"),
			link("Constitution", "/proxy/wp-content/uploads/2026/03/The-Triangle-Constitution-3.pdf"),
		}},
		{Entries: []models.FooterEntry{
			heading("News", "/news"),
			link("Campus", "/campus"),
			link("Academic Transformation", "/academic-transformation"),
			link("Politics", "/politics"),
			link("Transit", "/transit"),
			link("Crime & Policy Violations", "/crime-policy-violations"),
		}},
		{Entries: []models.FooterEntry{
			heading("Sports", "/sports"),
			link("Men's Basketball", "/mens-basketball"),
			link("Women's Basketball", "/womens-basketball"),
			link("Big 5", "/big-5"),
			link("Philly Sports", "/philly-sports"),
			link("Field Hockey", "/field-hockey"),
			link("Men's Soccer", "/mens-soccer"),
			link("Women's Soccer", "/womens-soccer"),
		}},
		{Entries: []models.FooterEntry{
			heading("Opinion", "/opinion"),
			link("Science & Tech", "/science-tech"),
			link("From the Editor", "/from-the-editor"),
			spacer,
			heading("Columns", "/columns"),
			link("From the Playbook", "/from-the-playbook"),
			link("The Love Triangle", "/the-love-triangle"),
			link("Tri This Sweet Treat", "/tri-this-sweet-treat"),
		}},
		{Entries: []models.FooterEntry{
			heading("Entertainment", "/entertainment"),
			link("Movies", "/movies"),
			link("Music", "/music"),
			link("Happening in Philly", "/happening-in-philly"),
			link("Cooking", "/cooking"),
			link("Books", "/books"),
			link("Gaming", "/gaming"),
			link("Listicles", "/listicles"),
		}},
		{Entries: []models.FooterEntry{
			heading("Comics & Puzzles", "/comics-puzzles"),
			link("Political Cartoons", "/political-cartoons"),
			link("Crossword", "/crossword"),
			link("Sudoku", "/sudoku"),
			spacer,
			heading("Special Editions", "/"),
			link("Graduation", "/graduation"),
			link("Welcome Week", "/search?s=Welcome%20Week"),
			external("The Rectangle", "https://therectangle.org"),
			link("100 Year Anniversary", "/one-hundred"),
		}},
	}
}

// GetFooterSettings returns the stored footer menu, falling back to the
// built-in default when the key is absent, blank, unparseable, or stores an
// empty menu. The public footer is not something that should ever render empty
// because of a bad write.
func GetFooterSettings(ctx context.Context, conn *sql.DB) (models.FooterSettings, error) {
	var raw string
	err := conn.QueryRowContext(ctx, "SELECT value_text FROM cms_settings WHERE key_name = ? LIMIT 1", footerSettingKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return models.FooterSettings{Columns: defaultFooterColumns()}, nil
	}
	if err != nil {
		return models.FooterSettings{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return models.FooterSettings{Columns: defaultFooterColumns()}, nil
	}

	var parsed models.FooterSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return models.FooterSettings{Columns: defaultFooterColumns()}, nil
	}
	parsed.Columns = normalizeFooterColumns(parsed.Columns)
	if len(parsed.Columns) == 0 {
		return models.FooterSettings{Columns: defaultFooterColumns()}, nil
	}
	return parsed, nil
}

// SetFooterSettings persists the footer menu. Passing an empty menu clears the
// customization, which makes the public site fall back to the default columns.
func SetFooterSettings(ctx context.Context, conn *sql.DB, s models.FooterSettings) error {
	s.Columns = normalizeFooterColumns(s.Columns)

	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return setSetting(ctx, conn, footerSettingKey, string(payload))
}

// normalizeFooterColumns trims the menu and drops entries that would render as
// nothing: unlabelled links, and columns left with no visible content. An
// unrecognized kind is treated as a link, since that is the only kind that
// carries a destination.
func normalizeFooterColumns(columns []models.FooterColumn) []models.FooterColumn {
	normalized := make([]models.FooterColumn, 0, len(columns))
	for _, column := range columns {
		entries := make([]models.FooterEntry, 0, len(column.Entries))
		for _, entry := range column.Entries {
			entry.Label = strings.TrimSpace(entry.Label)
			entry.Href = strings.TrimSpace(entry.Href)

			switch entry.Kind {
			case models.FooterEntrySpacer:
				entries = append(entries, models.FooterEntry{Kind: models.FooterEntrySpacer})
				continue
			case models.FooterEntryHeading:
			default:
				entry.Kind = models.FooterEntryLink
			}

			if entry.Label == "" {
				continue
			}
			entries = append(entries, entry)
		}

		// A column of nothing but spacers has no content to show.
		hasContent := false
		for _, entry := range entries {
			if entry.Kind != models.FooterEntrySpacer {
				hasContent = true
				break
			}
		}
		if !hasContent {
			continue
		}
		normalized = append(normalized, models.FooterColumn{Entries: entries})
	}
	return normalized
}
