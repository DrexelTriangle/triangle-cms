package handlers

import "testing"

func TestNormalizeSectionSlugAliases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "candp", want: "comics-puzzles"},
		{in: "Comics", want: "comics-puzzles"},
		{in: "comics-and-puzzles", want: "comics-puzzles"},
		{in: "sports", want: "sports"},
	}

	for _, tc := range cases {
		if got := normalizeSectionSlug(tc.in); got != tc.want {
			t.Fatalf("normalizeSectionSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeAndValidateArticleParams_AllowsCandpAlias(t *testing.T) {
	params, err := normalizeAndValidateArticleParams(ArticleParams{
		Section: "candp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Section != "comics-puzzles" {
		t.Fatalf("section = %q, want comics-puzzles", params.Section)
	}
}
