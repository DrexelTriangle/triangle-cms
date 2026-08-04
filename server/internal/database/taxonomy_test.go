package database

import (
	"strings"
	"testing"
)

func assertPatterns(t *testing.T, slug string, want []string) {
	t.Helper()
	got := CategoryMatchPatterns(slug)
	if len(got) != len(want) {
		t.Fatalf("%s: got %d patterns %v, want %d %v", slug, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: pattern %d = %q, want %q", slug, i, got[i], want[i])
		}
	}
}

func TestCategoryMatchPatternsExpandsDashedSlugs(t *testing.T) {
	// A dashed slug has to find the WordPress spelling, which uses spaces and
	// often an ampersand: "comics-puzzles" must match "Comics & Puzzles".
	assertPatterns(t, "comics-puzzles", []string{
		`%"comics-puzzles"%`, `%"comics puzzles"%`, `%"comics & puzzles"%`,
	})
}

func TestCategoryMatchPatternsRestoresPossessiveApostrophe(t *testing.T) {
	// The category text is "Men's Basketball"; no slug can carry the
	// apostrophe, so without this pattern the subsection matched nothing.
	assertPatterns(t, "mens-basketball", []string{
		`%"mens-basketball"%`, `%"mens basketball"%`, `%"mens & basketball"%`, `%"men's basketball"%`,
	})
}

func TestCategoryMatchPatternsLeavesPluralsAlone(t *testing.T) {
	// "philly-sports" is a plural, not a possessive: guessing "philly sport's"
	// would add a pattern that matches nothing.
	for _, pattern := range CategoryMatchPatterns("philly-sports") {
		if strings.Contains(pattern, "'") {
			t.Fatalf("unexpected possessive pattern %q", pattern)
		}
	}
}

func TestCategoryMatchPatternsSingleWordSlug(t *testing.T) {
	assertPatterns(t, "comics", []string{`%"comics"%`})
}

func TestCategoryMatchPatternsEmpty(t *testing.T) {
	if got := CategoryMatchPatterns("   "); got != nil {
		t.Fatalf("got %v, want nil for a blank slug", got)
	}
}

// escapedAmp is the escape encoding/json emits for "&". Assembled by
// concatenation so the sequence cannot be folded back into a literal "&" by an
// editor or a copy-paste, which would make the tests below silently vacuous.
var escapedAmp = `\` + "u0026"

// matchesCategories reports whether any pattern for slug would match a
// `categories` JSON array, mirroring what CategoryColumnExpr + LIKE does in
// SQL: lowercase the column, unescape the ampersand, then substring-match.
func matchesCategories(slug, categoriesJSON string) bool {
	column := strings.ReplaceAll(strings.ToLower(categoriesJSON), escapedAmp, "&")
	for _, pattern := range CategoryMatchPatterns(slug) {
		if strings.Contains(column, strings.Trim(pattern, "%")) {
			return true
		}
	}
	return false
}

func TestCategoryMatchIsWholeCategoryNotSubstring(t *testing.T) {
	// The bug this guards: every comic carries the parent title "Comics &
	// Puzzles", which contains "puzzles", so an unanchored pattern filed all
	// 219 comics under the Puzzles subsection.
	comic := `["Comics", "Comics & Puzzles"]`
	if matchesCategories("puzzles", comic) {
		t.Error("a Comics article must not match the puzzles subsection")
	}
	if !matchesCategories("comics", comic) {
		t.Error("a Comics article must match the comics subsection")
	}
	if !matchesCategories("comics-puzzles", comic) {
		t.Error("a Comics article must match its parent section")
	}
	if !matchesCategories("puzzles", `["Puzzles", "Comics & Puzzles"]`) {
		t.Error("a genuinely-tagged Puzzles article must match the puzzles subsection")
	}
}

func TestCategoryMatchDoesNotFoldWomensIntoMens(t *testing.T) {
	// "men's basketball" is a substring of "women's basketball", so the
	// unanchored matcher counted the women's team inside the men's.
	womens := `["Women's Basketball", "Sports"]`
	if matchesCategories("mens-basketball", womens) {
		t.Error("a Women's Basketball article must not match mens-basketball")
	}
	if !matchesCategories("womens-basketball", womens) {
		t.Error("a Women's Basketball article must match womens-basketball")
	}
	if matchesCategories("mens-soccer", `["Women's Soccer", "Sports"]`) {
		t.Error("a Women's Soccer article must not match mens-soccer")
	}
}

func TestCategoryMatchDoesNotMatchLongerCategory(t *testing.T) {
	// "Movies I've Seen" is its own column, not the Movies subsection.
	if matchesCategories("movies", `["Movies I've Seen", "Arts & Entertainment"]`) {
		t.Error(`"Movies I've Seen" must not match the movies subsection`)
	}
	if !matchesCategories("movies", `["Movies", "Arts & Entertainment"]`) {
		t.Error("a Movies article must match the movies subsection")
	}
}

func TestCategoryMatchResolvesAliases(t *testing.T) {
	// The section is "Entertainment" but every article is filed under "Arts &
	// Entertainment"; exact matching only works because the alias is explicit.
	for slug, categories := range map[string]string{
		"entertainment":       `["Arts & Entertainment"]`,
		"science-tech":        `["Science & Technology", "Opinion"]`,
		"from-the-editor":     `["From the Editor's Desk", "Opinion"]`,
		"happening-in-philly": `["What's Happening in Philly", "Arts & Entertainment"]`,
	} {
		if !matchesCategories(slug, categories) {
			t.Errorf("%s must match %s via its alias", slug, categories)
		}
	}
}

func TestCategoryMatchToleratesEscapedAmpersand(t *testing.T) {
	// Articles saved through the CMS before the FormatTags fix carry the
	// HTML-escaped ampersand; they must still match their section.
	if !matchesCategories("comics-puzzles", `["Crossword","Comics `+escapedAmp+` Puzzles"]`) {
		t.Error("an escaped-ampersand row must still match its section")
	}
	if !matchesCategories("entertainment", `["Arts `+escapedAmp+` Entertainment"]`) {
		t.Error("an escaped-ampersand alias row must still match")
	}
}

func TestTaxonomyCountConditionORsEverySlug(t *testing.T) {
	// A section matches its own slug OR any child's, so a container section
	// with no category of its own still resolves to its subsections' articles.
	condition, args := TaxonomyCountCondition([]string{"special-editions", "welcome-week"})
	if condition == "" {
		t.Fatal("expected a condition")
	}
	if strings.Contains(condition, " AND ") {
		t.Errorf("condition must OR its slugs, not AND them: %s", condition)
	}
	// 3 patterns per dashed slug, two slugs.
	if got := strings.Count(condition, "LIKE ?"); got != 6 {
		t.Errorf("got %d placeholders, want 6: %s", got, condition)
	}
	if len(args) != 6 {
		t.Errorf("got %d args, want 6: %v", len(args), args)
	}
	for _, want := range []string{`%"welcome week"%`, `%"special editions"%`} {
		found := false
		for _, arg := range args {
			if arg == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing pattern %q in %v", want, args)
		}
	}
}

func TestTaxonomyCountConditionMatchesOnTheNormalizedColumn(t *testing.T) {
	// The escape-folding has to be in the SQL, not just in the patterns.
	condition, _ := TaxonomyCountCondition([]string{"comics"})
	if !strings.Contains(condition, CategoryColumnExpr) {
		t.Errorf("condition must match on %s, got %s", CategoryColumnExpr, condition)
	}
}

func TestTaxonomyCountConditionEmpty(t *testing.T) {
	condition, args := TaxonomyCountCondition(nil)
	if condition != "" || args != nil {
		t.Fatalf("got (%q, %v), want empty", condition, args)
	}
}

func TestFormatTagsKeepsAmpersandUnescaped(t *testing.T) {
	// json.Marshal would HTML-escape the ampersand here, and that spelling no
	// longer matches the section the article is filed under, so saving an
	// article in the CMS used to drop it out of its own section.
	got := FormatTags([]string{"Comics", "Comics & Puzzles"})
	if strings.Contains(got, escapedAmp) {
		t.Errorf("FormatTags escaped the ampersand: %s", got)
	}
	if want := `["Comics","Comics & Puzzles"]`; got != want {
		t.Errorf("FormatTags() = %q, want %q", got, want)
	}
}

func TestFormatTagsRoundTripsThroughTheMatcher(t *testing.T) {
	// The two halves of the fix have to agree: whatever FormatTags writes must
	// still match the section it names.
	if !matchesCategories("comics-puzzles", FormatTags([]string{"Comics", "Comics & Puzzles"})) {
		t.Error("a CMS-saved article must match its own section")
	}
	if !matchesCategories("entertainment", FormatTags([]string{"Arts & Entertainment"})) {
		t.Error("a CMS-saved article must match its own section via alias")
	}
}
