package database

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeCategoryValues(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want []string
	}{
		"json array": {`["News","Campus"]`, []string{"news", "campus"}},
		// The escape encoding/json used to emit for "&". Decoding resolves it,
		// which is what makes the SQL-side REPLACE unnecessary.
		"escaped ampersand": {`["Comics & Puzzles"]`, []string{"comics & puzzles"}},
		"whitespace":        {`["  News  "]`, []string{"news"}},
		"duplicates":        {`["News","news"]`, []string{"news"}},
		"empty members":     {`["News",""]`, []string{"news"}},
		"empty array":       {`[]`, nil},
		"blank":             {"   ", nil},
		// A row that is not a JSON array never matched the LIKE predicate this
		// replaced, so indexing it would add articles to sections that have
		// never shown them.
		"not json": {`News,Campus`, nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := normalizeCategoryValues(tc.raw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeCategoryValues(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// A title longer than the column is skipped rather than truncated: a truncated
// key would compare equal to a section it does not belong to.
func TestNormalizeCategoryListSkipsOverlongTitles(t *testing.T) {
	long := strings.Repeat("a", maxCategoryLength+1)
	got := normalizeCategoryList([]string{"News", long})
	if !reflect.DeepEqual(got, []string{"news"}) {
		t.Errorf("got %v, want just [news]", got)
	}
}

// The index stores what CategoryMatchValues asks for. If the two normalizations
// ever diverge, every section page silently empties.
func TestIndexedCategoriesMatchTheValuesQueriedFor(t *testing.T) {
	withCategoryAliases(t, map[string][]string{"entertainment": {"Arts & Entertainment"}})

	indexed := normalizeCategoryValues(`["Arts & Entertainment"]`)
	wanted := CategoryMatchValues("entertainment")

	found := false
	for _, have := range indexed {
		for _, want := range wanted {
			if have == want {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("indexed %v matches none of the queried values %v", indexed, wanted)
	}
}
