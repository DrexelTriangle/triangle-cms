package handlers

import "testing"

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
	if _, err := normalizeAndValidateArticleParams(ArticleParams{
		Section: "candp",
	}); err == nil {
		t.Fatal("expected error for unknown section_slug")
	}
}
