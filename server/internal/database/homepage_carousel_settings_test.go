package database

import (
	"testing"

	"server/internal/models"
)

func TestNormalizeHomepageCarouselSlides(t *testing.T) {
	slides := NormalizeHomepageCarouselSlides([]models.HomepageCarouselSlide{
		{Enabled: true, Title: "  Podcast  ", LinkURL: " /podcast ", ImageURL: " /images/podcast.webp ", TextColor: " "},
		{Enabled: true},
		{Enabled: false, Title: "Disabled draft"},
	})

	if len(slides) != 2 {
		t.Fatalf("slides = %d, want 2", len(slides))
	}
	if slides[0].Title != "Podcast" {
		t.Fatalf("title = %q, want %q", slides[0].Title, "Podcast")
	}
	if slides[0].LinkURL != "/podcast" {
		t.Fatalf("link_url = %q, want %q", slides[0].LinkURL, "/podcast")
	}
	if slides[0].TextColor != "#ffffff" {
		t.Fatalf("text_color = %q, want default #ffffff", slides[0].TextColor)
	}
	if slides[1].Title != "Disabled draft" {
		t.Fatalf("disabled slide title = %q, want preserved draft", slides[1].Title)
	}
}

func TestPublishedHomepageCarousel(t *testing.T) {
	slides := PublishedHomepageCarousel([]models.HomepageCarouselSlide{
		{Enabled: true, Title: "Live", LinkURL: "/live"},
		{Enabled: false, Title: "Draft", LinkURL: "/draft"},
	})

	if len(slides) != 1 {
		t.Fatalf("slides = %d, want 1", len(slides))
	}
	if slides[0].Title != "Live" {
		t.Fatalf("published title = %q, want Live", slides[0].Title)
	}
}

func TestDefaultHomepageCarouselSlidesReturnsCopy(t *testing.T) {
	first := DefaultHomepageCarouselSlides()
	first[0].Title = "Changed"

	second := DefaultHomepageCarouselSlides()
	if second[0].Title == "Changed" {
		t.Fatal("default slides share backing storage")
	}
}
