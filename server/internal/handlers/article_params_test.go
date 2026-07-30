package handlers

import (
	"context"
	"testing"
)

func TestNormalizeSectionSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: " sports ", want: "sports"},
		{in: "Opinion", want: "opinion"},
		{in: "", want: ""},
		{in: "sports", want: "sports"},
	}

	for _, tc := range cases {
		if got := normalizeSectionSlug(tc.in); got != tc.want {
			t.Fatalf("normalizeSectionSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeAndValidateArticleParams_RejectsUnknownSectionSlug(t *testing.T) {
	if _, err := normalizeAndValidateArticleParams(context.Background(), nil, ArticleParams{
		Section: "candp",
	}); err == nil {
		t.Fatal("expected error for unknown section_slug")
	}
}

func TestNormalizeAndValidateArticleParams_FallbackAllowsKnownColumn(t *testing.T) {
	got, err := normalizeAndValidateArticleParams(context.Background(), nil, ArticleParams{
		Subsection: "the-love-triangle",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Section != "columns" {
		t.Fatalf("section = %q, want columns", got.Section)
	}
}
