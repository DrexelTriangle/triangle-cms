package database

import "testing"

// Every template below is a real value from articles.seo_title in production,
// paired with its article's headline.
func TestExpandYoastTitleOnProductionTemplates(t *testing.T) {
	const siteTitle = "The Triangle"

	cases := []struct {
		name            string
		template        string
		title           string
		primaryCategory string
		want            string
	}{
		{
			name:     "the default template is just the headline",
			template: "%%title%% %%page%%",
			title:    "With You!",
			want:     "With You!",
		},
		{
			name:     "a dropped page variable does not stall the separator",
			template: "Where to Eat on Drexel University's Campus %%page%% %%sep%% %%sitename%%",
			title:    "Where to eat on Drexel's campus",
			want:     "Where to Eat on Drexel University's Campus - The Triangle",
		},
		{
			name:     "a trailing page variable leaves the headline alone",
			template: "Yung Lean's \"Stardust\" tour omits new tracks in favor of old hits %%page%%",
			title:    "Yung Lean's Stardust tour",
			want:     "Yung Lean's \"Stardust\" tour omits new tracks in favor of old hits",
		},
		{
			name:     "a template ending in a separator does not hang on the dash",
			template: "%%title%% %%page%% %%sep%%",
			title:    "Ready to dance? CRJ and Empress Of got you covered",
			want:     "Ready to dance? CRJ and Empress Of got you covered",
		},
		{
			name:     "an editor-typed separator keeps its space",
			template: "Drexel Unveils Stone Installation \"Pars pro Toto\" -%%sitename%%",
			title:    "Drexel unveils Pars pro Toto",
			want:     "Drexel Unveils Stone Installation \"Pars pro Toto\" - The Triangle",
		},
		{
			name:            "the primary category is substituted like any other variable",
			template:        "%%title%% %%page%% %%sep%% %%primary_category%%",
			title:           "Ten public safety tips and resources for Drexel students",
			primaryCategory: "News",
			want:            "Ten public safety tips and resources for Drexel students - News",
		},
		{
			name:     "an unrecognised variable is dropped rather than printed",
			template: "%%currentyear%% Real headline",
			title:    "Real headline",
			want:     "Real headline",
		},
		{
			name:     "a template that is nothing but punctuation expands to nothing",
			template: "%%page%% %%sep%%",
			title:    "A headline",
			want:     "",
		},
		{
			name:     "text without variables is returned unchanged",
			template: "A hand-written SEO title",
			title:    "A headline",
			want:     "A hand-written SEO title",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ExpandYoastTitle(testCase.template, testCase.title, siteTitle, testCase.primaryCategory)
			if got != testCase.want {
				t.Errorf("ExpandYoastTitle(%q) = %q, want %q", testCase.template, got, testCase.want)
			}
			if HasYoastVariables(got) {
				t.Errorf("ExpandYoastTitle(%q) left a variable behind: %q", testCase.template, got)
			}
		})
	}
}

// Expanding is not the same as rewriting: running the result back through has
// to leave it alone, or a restart would keep rewriting the same rows.
func TestExpandYoastTitleIsIdempotent(t *testing.T) {
	once := ExpandYoastTitle("%%title%% %%page%% %%sep%% %%sitename%%", "A headline", "The Triangle", "")
	twice := ExpandYoastTitle(once, "A headline", "The Triangle", "")
	if once != twice {
		t.Errorf("second pass changed the value: %q then %q", once, twice)
	}
}

func TestIsRedundantSEOTitleMatchesWhatTheSiteRendersAnyway(t *testing.T) {
	const title = "Meet the faces behind the Drexel Affirmations meme account"
	const siteTitle = "The Triangle"

	redundant := []string{
		"",
		title,
		title + " - " + siteTitle,
		"  " + title + "   ",
	}
	for _, value := range redundant {
		if !isRedundantSEOTitle(value, title, siteTitle) {
			t.Errorf("%q says no more than the headline, expected it to be redundant", value)
		}
	}

	kept := []string{
		"Drexel Affirmations: the meme account explained",
		title + " - News",
	}
	for _, value := range kept {
		if isRedundantSEOTitle(value, title, siteTitle) {
			t.Errorf("%q is an editor's own title, expected it to be kept", value)
		}
	}
}

func TestHasYoastVariables(t *testing.T) {
	if !HasYoastVariables("%%title%% %%page%%") {
		t.Error("a template should be detected")
	}
	if HasYoastVariables("A 100%% real headline about 50% off") {
		t.Error("percent signs that are not a variable should not match")
	}
}
