package database

import (
	"database/sql"
	"fmt"
	"testing"
)

func tagRows(values ...string) []sql.NullString {
	rows := make([]sql.NullString, 0, len(values))
	for _, value := range values {
		rows = append(rows, sql.NullString{String: value, Valid: true})
	}
	return rows
}

func TestRankTagValuesRanksByArticleCount(t *testing.T) {
	ranked := rankTagValues(tagRows(
		`["triangle","drexel"]`,
		`["triangle","news"]`,
		`["triangle"]`,
		`["drexel"]`,
	))

	want := []PopularTag{
		{Name: "triangle", Uses: 3},
		{Name: "drexel", Uses: 2},
		{Name: "news", Uses: 1},
	}
	if len(ranked) != len(want) {
		t.Fatalf("got %d tags, want %d: %v", len(ranked), len(want), ranked)
	}
	for i, expected := range want {
		if ranked[i] != expected {
			t.Errorf("position %d: got %+v, want %+v", i, ranked[i], expected)
		}
	}
}

// The archive carries the same tag under several casings. Folding them is the
// whole point: two entries reading "Drexel" and "drexel" is the duplicated list
// the suggestions are meant to replace.
func TestRankTagValuesFoldsCaseAndShowsTheCommonestSpelling(t *testing.T) {
	ranked := rankTagValues(tagRows(
		`["Drexel"]`,
		`["drexel"]`,
		`["drexel"]`,
		`["DREXEL"]`,
	))

	if len(ranked) != 1 {
		t.Fatalf("case variants were not folded: %v", ranked)
	}
	if ranked[0].Uses != 4 {
		t.Errorf("got %d uses, want 4", ranked[0].Uses)
	}
	if ranked[0].Name != "drexel" {
		t.Errorf("got display spelling %q, want the commonest one, %q", ranked[0].Name, "drexel")
	}
}

// Equally common spellings must not depend on map iteration order, or the
// suggestion row renames itself every time the cache rebuilds.
func TestRankTagValuesPicksADeterministicSpellingOnATie(t *testing.T) {
	for i := 0; i < 20; i++ {
		ranked := rankTagValues(tagRows(`["Title IX"]`, `["title ix"]`))
		if len(ranked) != 1 {
			t.Fatalf("case variants were not folded: %v", ranked)
		}
		if ranked[0].Name != "Title IX" {
			t.Fatalf("got %q, want the lexicographically first spelling %q", ranked[0].Name, "Title IX")
		}
	}
}

func TestRankTagValuesCountsAnArticleOncePerTag(t *testing.T) {
	ranked := rankTagValues(tagRows(`["drexel","Drexel","drexel"]`))

	if len(ranked) != 1 {
		t.Fatalf("got %d tags, want 1: %v", len(ranked), ranked)
	}
	if ranked[0].Uses != 1 {
		t.Errorf("got %d uses, want 1; one article carrying a tag three times is still one article", ranked[0].Uses)
	}
}

// FormatTags falls back to a comma-joined string when marshalling fails, and
// rows written before the CMS existed are not guaranteed to be JSON either. A
// tag that the article editor reads back has to be a tag the suggestions count.
func TestRankTagValuesReadsTheCommaJoinedFallback(t *testing.T) {
	ranked := rankTagValues(tagRows(`triangle,drexel`, `["triangle"]`))

	want := map[string]int64{"triangle": 2, "drexel": 1}
	if len(ranked) != len(want) {
		t.Fatalf("got %v, want %v", ranked, want)
	}
	for _, tag := range ranked {
		if want[tag.Name] != tag.Uses {
			t.Errorf("tag %q: got %d uses, want %d", tag.Name, tag.Uses, want[tag.Name])
		}
	}
}

func TestRankTagValuesSkipsEmptyAndBlankEntries(t *testing.T) {
	ranked := rankTagValues(append(
		tagRows(`[]`, `[""]`, `["   "]`, `null`, `["  drexel  "]`),
		sql.NullString{},
	))

	if len(ranked) != 1 {
		t.Fatalf("got %d tags, want 1: %v", len(ranked), ranked)
	}
	if ranked[0].Name != "drexel" {
		t.Errorf("got %q, want the trimmed tag %q", ranked[0].Name, "drexel")
	}
}

// The ranking keeps every tag, because search reads it: a tag used twice in
// 2019 has to stay findable, and it is nowhere near the popular slice. Only the
// response is capped.
func TestRankTagValuesKeepsTheWholeArchive(t *testing.T) {
	values := make([]sql.NullString, 0, MaxTagsLimit+50)
	for i := 0; i < MaxTagsLimit+50; i++ {
		values = append(values, sql.NullString{String: fmt.Sprintf(`["tag-%03d"]`, i), Valid: true})
	}

	ranked := rankTagValues(values)
	if len(ranked) != MaxTagsLimit+50 {
		t.Fatalf("got %d tags, want all %d of them", len(ranked), MaxTagsLimit+50)
	}
	if capped := capTags(ranked, 0); len(capped) != DefaultTagsLimit {
		t.Errorf("response was not capped: got %d tags, want %d", len(capped), DefaultTagsLimit)
	}
}

// Somebody typing "lacrosse" means the tag "lacrosse". Ordering matches by
// popularity alone buries it under a better-used "Men's Lacrosse", which makes
// the exact tag they asked for the one they have to hunt for.
func TestMatchRankPrefersTheCloserMatch(t *testing.T) {
	ordered := []string{"lacrosse", "lacrosse team", "men's lacrosse", "collacrosse"}
	for i := 1; i < len(ordered); i++ {
		previous := matchRank(ordered[i-1], "lacrosse")
		current := matchRank(ordered[i], "lacrosse")
		if previous < 0 || current < 0 {
			t.Fatalf("%q or %q did not match at all", ordered[i-1], ordered[i])
		}
		if previous >= current {
			t.Errorf("%q ranked %d, %q ranked %d; the closer match must rank lower",
				ordered[i-1], previous, ordered[i], current)
		}
	}
}

func TestMatchRankRejectsANonMatch(t *testing.T) {
	if rank := matchRank("drexel", "lacrosse"); rank >= 0 {
		t.Errorf("got rank %d, want -1 for a tag that does not contain the query", rank)
	}
}
