package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"server/internal/models"
)

// The footer menu is a single JSON blob in cms_settings rather than its own
// table: it is one small document, read whole by the public site and written
// whole by the settings screen, and it never needs to be queried by parts.
const footerSettingKey = "footer_menu"

// footerPart is one group inside a generated column: either a taxonomy section,
// rendered as its heading plus its visible subsections, or a literal run of
// entries for the things the taxonomy does not describe.
type footerPart struct {
	Section string
	Entries []models.FooterEntry
}

// footerTemplate is the SHAPE of the footer (which sections share a column and
// in what order) with the links themselves left to the taxonomy.
//
// The shape stays hand-written because it is a layout decision that no data
// answers: Columns is stacked under Opinion and Special Editions under Comics &
// Puzzles to keep the footer at six columns, and a rule like "one column per
// section" would widen it to eight the moment somebody adds a section.
//
// The two literal blocks are the entries that are not taxonomy at all. The About
// column is site furniture. The Special Editions block is subtler: those rows DO
// exist in the taxonomy now, but three of the four deliberately point somewhere
// other than their own page (The Rectangle is an external site, Welcome Week is
// a search URL, 100 Year Anniversary is a bespoke page) and Graduation is a
// section of its own that appears in no other column. Generating them would
// quietly redirect four working links.
func footerTemplate() [][]footerPart {
	link := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryLink, Label: label, Href: href}
	}
	external := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryLink, Label: label, Href: href, NewTab: true}
	}
	heading := func(label, href string) models.FooterEntry {
		return models.FooterEntry{Kind: models.FooterEntryHeading, Label: label, Href: href}
	}

	return [][]footerPart{
		{{Entries: []models.FooterEntry{
			heading("About", "/about"),
			link("Contact Us", "/contact"),
			external("Join The Triangle", "https://docs.google.com/forms/d/e/1FAIpQLScra_6sUenvmpIuQ5FjmMyWO0a2sz9z36HkrqfnYQvJGH9BGQ/viewform"),
			link("Staff", "/staff"),
			link("Find-A-Triangle", "/find"),
			link("Photo Gallery", "/photo"),
			external("Print Archive", "https://drexel.primo.exlibrisgroup.com/discovery/collectionDiscovery?vid=01DRXU_INST:01DRXU&inst=01DRXU_INST&collectionId=81448731180004721"),
			link("Constitution", "/proxy/wp-content/uploads/2026/03/The-Triangle-Constitution-3.pdf"),
		}}},
		{{Section: "news"}},
		{{Section: "sports"}},
		{{Section: "opinion"}, {Section: "columns"}},
		{{Section: "entertainment"}},
		{{Section: "comics-puzzles"}, {Entries: []models.FooterEntry{
			heading("Special Editions", "/"),
			link("Graduation", "/graduation"),
			link("Welcome Week", "/search?s=Welcome%20Week"),
			external("The Rectangle", "https://therectangle.org"),
			link("100 Year Anniversary", "/one-hundred"),
		}}},
	}
}

// footerTaxonomy is the section tree the footer needs: each section's title, and
// the visible subsections directly under it, in strip order.
type footerTaxonomy struct {
	titles   map[string]string
	children map[string][]models.FooterEntry
}

// loadFooterTaxonomy reads the sections and their DIRECT visible subsections.
//
// Direct only. The tree is three levels deep now (Beer Reviews hangs under
// Food, which hangs under A&E) and a footer that recursed would list the whole
// archive. One layer is what the section strip shows, so the footer matches the
// page it links to.
//
// is_visible filters both, which is what makes the footer stop being a separate
// thing to maintain: hiding a subsection in the sections screen removes it from
// the strip and from the footer in one action.
func loadFooterTaxonomy(ctx context.Context, conn *sql.DB) (footerTaxonomy, error) {
	loaded := footerTaxonomy{
		titles:   map[string]string{},
		children: map[string][]models.FooterEntry{},
	}
	if conn == nil {
		return loaded, nil
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT kind, slug, canonical_title, COALESCE(parent_slug, '')
		FROM site_taxonomy
		WHERE kind IN ('section', 'subsection') AND is_visible = 1
		ORDER BY id ASC
	`)
	if err != nil {
		return footerTaxonomy{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var kind, slug, title, parent string
		if err := rows.Scan(&kind, &slug, &title, &parent); err != nil {
			return footerTaxonomy{}, err
		}
		slug = strings.TrimSpace(slug)
		title = strings.TrimSpace(title)
		if slug == "" || title == "" {
			continue
		}
		if kind == "section" {
			loaded.titles[slug] = title
			continue
		}
		if parent = strings.TrimSpace(parent); parent != "" {
			loaded.children[parent] = append(loaded.children[parent], models.FooterEntry{
				Kind:  models.FooterEntryLink,
				Label: title,
				Href:  "/" + slug,
			})
		}
	}
	return loaded, rows.Err()
}

// buildFooterColumns renders footerTemplate against the taxonomy.
//
// Falls back to the static columns whenever the taxonomy cannot supply the
// sections: an unreadable table, or one that has not been imported yet. The
// footer is on every page, so "render the old links" beats "render an empty
// nav" by a wide margin.
func buildFooterColumns(ctx context.Context, conn *sql.DB) []models.FooterColumn {
	loaded, err := loadFooterTaxonomy(ctx, conn)
	if err != nil || len(loaded.titles) == 0 {
		return staticFooterColumns()
	}

	columns := make([]models.FooterColumn, 0, len(footerTemplate()))
	for _, parts := range footerTemplate() {
		entries := make([]models.FooterEntry, 0, 12)
		for _, part := range parts {
			rendered := part.Entries
			if part.Section != "" {
				title, known := loaded.titles[part.Section]
				if !known {
					// A section the template names but the taxonomy does not
					// have. Skipping the group keeps the rest of the column,
					// rather than emitting a heading that links nowhere.
					continue
				}
				rendered = append([]models.FooterEntry{{
					Kind:  models.FooterEntryHeading,
					Label: title,
					Href:  "/" + part.Section,
				}}, loaded.children[part.Section]...)
			}
			if len(rendered) == 0 {
				continue
			}
			// The blank line between stacked groups, added only between them so
			// a column never opens or closes on a spacer.
			if len(entries) > 0 {
				entries = append(entries, models.FooterEntry{Kind: models.FooterEntrySpacer})
			}
			entries = append(entries, rendered...)
		}
		if len(entries) > 0 {
			columns = append(columns, models.FooterColumn{Entries: entries})
		}
	}

	if len(columns) == 0 {
		return staticFooterColumns()
	}
	return columns
}

// staticFooterColumns mirrors the footer the public site shipped hardcoded. It
// is now only the fallback for when the taxonomy cannot be read; the live
// default is generated from it by buildFooterColumns.
func staticFooterColumns() []models.FooterColumn {
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
			link("Public Safety", "/public-safety"),
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

// generatedFooter caches the columns built from the taxonomy.
//
// The footer renders on every page and the stored setting is absent by default,
// so without this every request would run a taxonomy query, the exact shape of
// load that put a queue in front of the database on 2026-08-06. It shares
// settingsCacheTTL with cms_settings, since it is the same kind of value: read
// constantly, changed a few times a month.
var (
	generatedFooterMu      sync.RWMutex
	generatedFooterColumns []models.FooterColumn
	generatedFooterExpires time.Time
)

// defaultFooterColumns is the footer served when nothing is stored: generated
// from the taxonomy, cached, and falling back to the static columns.
func defaultFooterColumns(ctx context.Context, conn *sql.DB) []models.FooterColumn {
	ttl := settingsCacheTTL()
	if ttl > 0 {
		generatedFooterMu.RLock()
		cached, expires := generatedFooterColumns, generatedFooterExpires
		generatedFooterMu.RUnlock()
		if cached != nil && time.Now().Before(expires) {
			return cached
		}
	}

	columns := buildFooterColumns(ctx, conn)
	if ttl > 0 {
		generatedFooterMu.Lock()
		generatedFooterColumns, generatedFooterExpires = columns, time.Now().Add(ttl)
		generatedFooterMu.Unlock()
	}
	return columns
}

// InvalidateGeneratedFooter drops the cached columns, so a taxonomy edit shows
// up in the footer on the next request rather than up to a TTL later. Called
// from the same place that refreshes the other taxonomy-derived state.
func InvalidateGeneratedFooter() {
	generatedFooterMu.Lock()
	generatedFooterColumns, generatedFooterExpires = nil, time.Time{}
	generatedFooterMu.Unlock()
}

// GetFooterSettings returns the stored footer menu, falling back to the
// built-in default when the key is absent, blank, unparseable, or stores an
// empty menu. The public footer is not something that should ever render empty
// because of a bad write.
func GetFooterSettings(ctx context.Context, conn *sql.DB) (models.FooterSettings, error) {
	raw, found, err := readSettingRaw(ctx, conn, footerSettingKey)
	if err != nil {
		return models.FooterSettings{}, err
	}
	if !found {
		return models.FooterSettings{Columns: defaultFooterColumns(ctx, conn)}, nil
	}
	if strings.TrimSpace(raw) == "" {
		return models.FooterSettings{Columns: defaultFooterColumns(ctx, conn)}, nil
	}

	var parsed models.FooterSettings
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return models.FooterSettings{Columns: defaultFooterColumns(ctx, conn)}, nil
	}
	parsed.Columns = normalizeFooterColumns(parsed.Columns)
	if len(parsed.Columns) == 0 {
		return models.FooterSettings{Columns: defaultFooterColumns(ctx, conn)}, nil
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
