package database

import (
	"strings"
	"testing"
)

func TestCategoryMatchPatternsExpandsDashedSlugs(t *testing.T) {
	// A dashed slug has to find the WordPress spelling, which uses spaces and
	// often an ampersand: "comics-puzzles" must match "Comics & Puzzles".
	got := CategoryMatchPatterns("comics-puzzles")
	want := []string{"%comics-puzzles%", "%comics puzzles%", "%comics & puzzles%"}
	if len(got) != len(want) {
		t.Fatalf("got %d patterns %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCategoryMatchPatternsRestoresPossessiveApostrophe(t *testing.T) {
	// The category text is "Men's Basketball"; no slug can carry the
	// apostrophe, so without this pattern the subsection matched nothing.
	got := CategoryMatchPatterns("mens-basketball")
	want := []string{"%mens-basketball%", "%mens basketball%", "%mens & basketball%", "%men's basketball%"}
	if len(got) != len(want) {
		t.Fatalf("got %d patterns %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern %d = %q, want %q", i, got[i], want[i])
		}
	}
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
	got := CategoryMatchPatterns("comics")
	if len(got) != 1 || got[0] != "%comics%" {
		t.Fatalf("got %v, want [%%comics%%]", got)
	}
}

func TestCategoryMatchPatternsEmpty(t *testing.T) {
	if got := CategoryMatchPatterns("   "); got != nil {
		t.Fatalf("got %v, want nil for a blank slug", got)
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
	for _, want := range []string{"%welcome week%", "%special editions%"} {
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

func TestTaxonomyCountConditionEmpty(t *testing.T) {
	condition, args := TaxonomyCountCondition(nil)
	if condition != "" || args != nil {
		t.Fatalf("got (%q, %v), want empty", condition, args)
	}
}
