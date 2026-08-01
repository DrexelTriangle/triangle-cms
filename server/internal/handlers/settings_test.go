package handlers

import (
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
