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

// withCategoryAliases installs aliases in the cache for one test, standing in
// for what RefreshCategoryAliases loads from site_taxonomy, and restores the
// previous contents afterwards.
func withCategoryAliases(t *testing.T, aliases map[string][]string) {
	t.Helper()
	categoryAliasMu.Lock()
	previous := categoryAliasBySlug
	categoryAliasBySlug = aliases
	categoryAliasMu.Unlock()
	t.Cleanup(func() {
		categoryAliasMu.Lock()
		categoryAliasBySlug = previous
		categoryAliasMu.Unlock()
	})
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
	withCategoryAliases(t, map[string][]string{
		"entertainment":       {"Arts & Entertainment"},
		"science-tech":        {"Science & Technology"},
		"from-the-editor":     {"From the Editor's Desk"},
		"happening-in-philly": {"What's Happening in Philly"},
	})
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
	withCategoryAliases(t, map[string][]string{"entertainment": {"Arts & Entertainment"}})
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
	withCategoryAliases(t, map[string][]string{"entertainment": {"Arts & Entertainment"}})
	if !matchesCategories("comics-puzzles", FormatTags([]string{"Comics", "Comics & Puzzles"})) {
		t.Error("a CMS-saved article must match its own section")
	}
	if !matchesCategories("entertainment", FormatTags([]string{"Arts & Entertainment"})) {
		t.Error("a CMS-saved article must match its own section via alias")
	}
}

func TestParseCategoryAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"sql null", "null", nil},
		{"empty array", "[]", []string{}},
		{"one", `["Arts & Entertainment"]`, []string{"Arts & Entertainment"}},
		{"trims and drops blanks", `["  Arts & Entertainment  ", "", "   "]`, []string{"Arts & Entertainment"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCategoryAliases(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("alias %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseCategoryAliasesRejectsMalformed(t *testing.T) {
	// A malformed value must surface as an error rather than resolving to "no
	// aliases", which would empty the section without saying why.
	if _, err := ParseCategoryAliases(`{"not":"an array"}`); err == nil {
		t.Fatal("expected an error for a non-array value")
	}
}

func TestCategoryAliasesAreCaseInsensitiveOnTheSlug(t *testing.T) {
	// Aliases are keyed by lowercased slug, and the stored title's casing must
	// not matter either -- patterns are matched against a lowercased column.
	withCategoryAliases(t, map[string][]string{"entertainment": {"ARTS & ENTERTAINMENT"}})
	if !matchesCategories("Entertainment", `["Arts & Entertainment"]`) {
		t.Error("alias matching must be case-insensitive")
	}
}

func TestCategoryMatchPatternsDeduplicatesAliases(t *testing.T) {
	// An alias that merely restates a derived pattern must not double the
	// placeholders in every query that uses the slug.
	withCategoryAliases(t, map[string][]string{"comics": {"Comics"}})
	assertPatterns(t, "comics", []string{`%"comics"%`})
}

func TestDefaultCategoryAliasesCoverTheKnownMismatches(t *testing.T) {
	// These four sections are named differently from the category their
	// articles carry. Losing a default silently empties a section on upgrade,
	// so pin them.
	want := map[string]string{
		"entertainment":       "Arts & Entertainment",
		"science-tech":        "Science & Technology",
		"from-the-editor":     "From the Editor's Desk",
		"happening-in-philly": "What's Happening in Philly",
	}
	for slug, alias := range want {
		got, ok := defaultCategoryAliases[slug]
		if !ok {
			t.Errorf("missing default alias for %q", slug)
			continue
		}
		if len(got) != 1 || got[0] != alias {
			t.Errorf("default alias for %q = %v, want [%q]", slug, got, alias)
		}
	}
}

func TestSportsAliasesRollUpSportsWithNoSubsectionRow(t *testing.T) {
	// The reported bug: an article tagged only "Men's Lacrosse" carries no
	// literal "Sports", and lacrosse has no subsection row to be matched
	// through, so it appeared on no section page at all. WordPress rolled a
	// parent category up over its child terms for free; flat matching does not.
	withCategoryAliases(t, map[string][]string{"sports": defaultCategoryAliases["sports"]})

	for _, categories := range []string{
		`["Men's Lacrosse"]`,
		`["TV","Men's Lacrosse"]`,
		`["Women's Lacrosse"]`,
		`["Tennis"]`,
		`["Swimming & Diving"]`,
		`["Athlete of the Week"]`,
	} {
		if !matchesCategories("sports", categories) {
			t.Errorf("%s should roll up into sports", categories)
		}
	}
}

func TestSportsAliasesStayWholeCategoryMatches(t *testing.T) {
	// Aliases go through the same anchored patterns as everything else, so
	// adding them must not reintroduce the substring merging that put every
	// comic in Puzzles. "Mark and Jair Explain Sports" is the live example:
	// it ends in the section's own name and is not a sports article.
	withCategoryAliases(t, map[string][]string{"sports": defaultCategoryAliases["sports"]})

	for _, categories := range []string{
		`["Mark and Jair Explain Sports"]`,
		`["Table Tennis"]`,
		`["Golfing"]`,
	} {
		if matchesCategories("sports", categories) {
			t.Errorf("%s must not be pulled into sports", categories)
		}
	}
}

func TestDefaultSportsAliasesAreNotSubsectionSlugs(t *testing.T) {
	// A sport that already has its own subsection row is matched through that
	// row, so aliasing it onto the parent as well would only add a duplicate
	// pattern to every sports query.
	rows := map[string]struct{}{
		"mens-basketball": {}, "womens-basketball": {}, "mens-soccer": {},
		"womens-soccer": {}, "field-hockey": {}, "squash": {},
		"philly-sports": {}, "big-5": {}, "nil": {},
	}
	for _, alias := range defaultCategoryAliases["sports"] {
		slug := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(alias), "'", ""), " ", "-")
		if _, clash := rows[slug]; clash {
			t.Errorf("%q duplicates the existing subsection %q", alias, slug)
		}
	}
}

// withCategorySlugsByTitle installs the title -> slug cache for one test,
// standing in for what RefreshCategoryAliases loads from site_taxonomy.
func withCategorySlugsByTitle(t *testing.T, titles map[string]string) {
	t.Helper()
	categoryAliasMu.Lock()
	previous := categorySlugByTitle
	categorySlugByTitle = titles
	categoryAliasMu.Unlock()
	t.Cleanup(func() {
		categoryAliasMu.Lock()
		categorySlugByTitle = previous
		categoryAliasMu.Unlock()
	})
}

func TestCategoryLinkSlugUsesTheTaxonomySlug(t *testing.T) {
	// The bug: the chip's link was derived from the category NAME, so
	// "Men's Basketball" pointed at /men-s-basketball while the subsection
	// page is /mens-basketball. All four apostrophe subsections 404'd.
	withCategorySlugsByTitle(t, map[string]string{
		"men's basketball": "mens-basketball",
		"women's soccer":   "womens-soccer",
	})

	if got := CanonicalizeSlug("Men's Basketball"); got == "mens-basketball" {
		t.Fatal("precondition: name-derived slug is supposed to differ from the taxonomy slug")
	}
	for name, want := range map[string]string{
		"Men's Basketball": "mens-basketball",
		"women's soccer":   "womens-soccer",
	} {
		if got := CategoryLinkSlug(name); got != want {
			t.Errorf("CategoryLinkSlug(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCategoryLinkSlugFallsBackToTheDerivedSlug(t *testing.T) {
	// A category no section lists still needs a non-empty slug; it just has
	// nowhere useful to point, exactly as before.
	withCategorySlugsByTitle(t, map[string]string{})
	if got, want := CategoryLinkSlug("Some Retired Column"), "some-retired-column"; got != want {
		t.Errorf("CategoryLinkSlug = %q, want %q", got, want)
	}
	if got := CategoryLinkSlug("   "); got != "" {
		t.Errorf("blank category = %q, want empty", got)
	}
}

func TestTaxonomySlugForCategoryReportsNothingWhenUnknown(t *testing.T) {
	withCategorySlugsByTitle(t, map[string]string{"sports": "sports"})
	if got := TaxonomySlugForCategory("Men's Lacrosse"); got != "" {
		t.Errorf("got %q, want empty for a category with no page", got)
	}
}
