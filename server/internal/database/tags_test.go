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
		t.Errorf("got %d uses, want 1 -- one article carrying a tag three times is still one article", ranked[0].Uses)
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

// The imported archive has a long tail of tags used exactly once; the ranking
// is held in memory, so it is capped before it ever reaches a caller.
func TestRankTagValuesCapsTheRanking(t *testing.T) {
	values := make([]sql.NullString, 0, MaxPopularTagsLimit+50)
	for i := 0; i < MaxPopularTagsLimit+50; i++ {
		values = append(values, sql.NullString{String: fmt.Sprintf(`["tag-%03d"]`, i), Valid: true})
	}

	ranked := rankTagValues(values)
	if len(ranked) != MaxPopularTagsLimit {
		t.Fatalf("got %d tags, want the cap of %d", len(ranked), MaxPopularTagsLimit)
	}
}
