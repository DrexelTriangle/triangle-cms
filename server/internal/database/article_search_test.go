package database

import "testing"

func TestBuildFulltextBooleanQuery(t *testing.T) {
	tests := []struct {
		name string
		term string
		want string
	}{
		{
			name: "requires every term and prefix-matches the last",
			term: "drexel university tuition",
			want: "+drexel +university +tuition*",
		},
		{
			// Boolean mode reads these as operators. Unstripped, this is a syntax
			// error from the database rather than a search.
			name: "strips boolean operators out of the terms",
			term: `+dragons -(basketball) "@home" ~win*`,
			want: "+dragons +basketball +home +win*",
		},
		{
			// InnoDB never indexes tokens below innodb_ft_min_token_size, so a
			// required "+of" would match nothing at all.
			name: "drops tokens shorter than the index minimum",
			term: "a of the campus",
			want: "+the +campus*",
		},
		{
			name: "keeps digits so years and numbered things still search",
			term: "2026 budget",
			want: "+2026 +budget*",
		},
		{
			// The caller reads "" as "use the LIKE path instead".
			name: "yields nothing usable for punctuation only",
			term: "!!! ++",
			want: "",
		},
		{
			name: "yields nothing usable when every token is too short",
			term: "C++ vs",
			want: "",
		},
		{
			name: "yields nothing usable for a blank term",
			term: "   ",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildFulltextBooleanQuery(tc.term); got != tc.want {
				t.Errorf("BuildFulltextBooleanQuery(%q) = %q, want %q", tc.term, got, tc.want)
			}
		})
	}
}
