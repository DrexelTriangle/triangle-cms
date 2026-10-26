package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server/internal/models"
)

func TestValidateExhibitSpotlightsRejectsEnabledWithoutDate(t *testing.T) {
	err := validateExhibitSpotlights([]models.ExhibitSpotlight{{Title: "T", Body: "b", Enabled: true}})
	if err == nil || !strings.Contains(err.Error(), "feature_date") {
		t.Fatalf("err = %v, want feature_date error", err)
	}
}

func TestValidateExhibitSpotlightsRejectsBadDate(t *testing.T) {
	err := validateExhibitSpotlights([]models.ExhibitSpotlight{{Title: "T", Body: "b", Enabled: true, FeatureDate: "10/08/2026"}})
	if err == nil {
		t.Fatal("want error for non YYYY-MM-DD date")
	}
}

func TestValidateExhibitSpotlightsAllowsDisabledDraftWithoutDate(t *testing.T) {
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{{Title: "T", Body: "b"}}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestValidateExhibitSpotlightsRejectsUnsafeURLSchemes(t *testing.T) {
	base := models.ExhibitSpotlight{Title: "T", Body: "b", Enabled: true, FeatureDate: "2026-10-08"}
	js := base
	js.ImageCreditURL = "javascript:alert(1)"
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{js}); err == nil {
		t.Fatal("want error for javascript: credit URL")
	}
	img := base
	img.ImageURL = "data:image/png;base64,AAAA"
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{img}); err == nil {
		t.Fatal("want error for data: image URL")
	}
	ok := base
	ok.ImageURL = "https://delta.thetriangle.org/wp-content/uploads/scalene/exhibits/x.webp"
	ok.ImageCreditURL = "https://commons.wikimedia.org/wiki/File:X.jpg"
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{ok}); err != nil {
		t.Fatalf("err = %v, want nil for https URLs", err)
	}
}

func TestValidateExhibitSpotlightsCapsAtThirty(t *testing.T) {
	many := make([]models.ExhibitSpotlight, 31)
	for i := range many {
		many[i] = models.ExhibitSpotlight{Title: "T", Body: "b"}
	}
	if err := validateExhibitSpotlights(many); err == nil {
		t.Fatal("want error above 30 entries")
	}
}

func TestPatchExhibitSpotlightsRejectsMissingEntries(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/v1/settings/exhibit-spotlights", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	PatchExhibitSpotlights(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when entries is missing", rec.Code)
	}
}

func TestValidateExhibitSpotlightsRejectsUnsafeLinkURL(t *testing.T) {
	e := models.ExhibitSpotlight{Title: "T", Body: "b", Enabled: true, FeatureDate: "2026-10-08", LinkURL: "javascript:alert(1)"}
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{e}); err == nil {
		t.Fatal("want error for javascript: link_url")
	}
	e.LinkURL = "https://ansp.org/press-room/press-releases/2025/haddy-returns"
	if err := validateExhibitSpotlights([]models.ExhibitSpotlight{e}); err != nil {
		t.Fatalf("err = %v, want nil for https link_url", err)
	}
}
