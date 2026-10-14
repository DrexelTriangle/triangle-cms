package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server/internal/models"
)

func TestValidateHomepageCarouselSlides(t *testing.T) {
	valid := []models.HomepageCarouselSlide{
		{Enabled: true, Title: "Podcast", LinkURL: "https://example.com", ImageURL: "/images/podcast.webp"},
		{Enabled: false, Title: "Draft"},
	}
	if err := validateHomepageCarouselSlides(valid); err != nil {
		t.Fatalf("valid slides rejected: %v", err)
	}

	invalid := []models.HomepageCarouselSlide{
		{Enabled: true, Title: "Broken", LinkURL: "ftp://example.com/file"},
	}
	if err := validateHomepageCarouselSlides(invalid); err == nil || !strings.Contains(err.Error(), "link_url") {
		t.Fatalf("invalid link_url error = %v, want link_url validation", err)
	}
}

func TestValidateFooterColumns(t *testing.T) {
	valid := []models.FooterColumn{{Entries: []models.FooterEntry{
		{Kind: models.FooterEntryLink, Label: "Staff", Href: "/staff"},
		{Kind: models.FooterEntryLink, Label: "Games", Href: "/games", VisibleFrom: "2099-01-01T09:00:00-05:00"},
		// A spacer's schedule is stripped, not judged.
		{Kind: models.FooterEntrySpacer, VisibleFrom: "whenever"},
	}}}
	if err := validateFooterColumns(valid); err != nil {
		t.Fatalf("valid footer rejected: %v", err)
	}

	zoneless := []models.FooterColumn{
		{Entries: []models.FooterEntry{{Kind: models.FooterEntryHeading, Label: "About", Href: "/about"}}},
		{Entries: []models.FooterEntry{
			{Kind: models.FooterEntryHeading, Label: "Comics & Puzzles", Href: "/comics-puzzles"},
			{Kind: models.FooterEntryLink, Label: "Games", Href: "/games", VisibleFrom: "2099-01-01T09:00"},
		}},
	}
	err := validateFooterColumns(zoneless)
	if err == nil || !strings.Contains(err.Error(), "column 2 entry 2") || !strings.Contains(err.Error(), "visible_from") {
		t.Fatalf("zoneless visible_from error = %v, want one naming column 2 entry 2", err)
	}
}

// A schedule the server cannot read must not be saved with the date silently
// dropped: that would publish a launch link the moment the editor hit Save.
// The 400 comes before any database access, so no connection is needed.
func TestPatchFooterSettings_RejectsAMalformedSchedule(t *testing.T) {
	body := `{"columns":[{"entries":[{"kind":"link","label":"Games","href":"/games","visible_from":"tomorrow"}]}]}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/settings/footer", strings.NewReader(body))
	rec := httptest.NewRecorder()

	PatchFooterSettings(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "visible_from") {
		t.Errorf("error body does not name the field: %s", rec.Body.String())
	}
}
