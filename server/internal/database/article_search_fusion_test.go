package database

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestFuseByReciprocalRank(t *testing.T) {
	tests := []struct {
		name    string
		lexical []int64
		vector  []int64
		want    []int64
	}{
		{
			// The whole point of fusion: an article both halves like beats one
			// that either half ranks first on its own.
			name:    "agreement outranks a single list's top hit",
			lexical: []int64{1, 2, 3},
			vector:  []int64{4, 2, 5},
			want:    []int64{2, 1, 4, 3, 5},
		},
		{
			name:    "identical lists keep their order",
			lexical: []int64{9, 8, 7},
			vector:  []int64{9, 8, 7},
			want:    []int64{9, 8, 7},
		},
		{
			// Ties break toward the lexical list: when both halves rate two
			// articles equally, the one whose words the reader typed wins.
			name:    "disjoint lists interleave, lexical first on ties",
			lexical: []int64{1, 2},
			vector:  []int64{3, 4},
			want:    []int64{1, 3, 2, 4},
		},
		{
			// No sidecar, an unembedded corpus, or a vector query that failed:
			// fusion has to degrade to exactly the lexical ordering.
			name:    "an empty vector list preserves lexical order",
			lexical: []int64{5, 6, 7},
			vector:  nil,
			want:    []int64{5, 6, 7},
		},
		{
			name:    "an empty lexical list preserves vector order",
			lexical: nil,
			vector:  []int64{5, 6, 7},
			want:    []int64{5, 6, 7},
		},
		{
			name:    "both empty",
			lexical: nil,
			vector:  nil,
			want:    []int64{},
		},
		{
			// A result deep in the lexical list but top of the vector list should
			// still surface: this is the "story about parking permits that never
			// says parking" case the vector half exists for.
			name:    "a deep lexical hit rises on a strong vector rank",
			lexical: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
			vector:  []int64{11},
			want:    []int64{11, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fuseByReciprocalRank(tc.lexical, tc.vector)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("fuseByReciprocalRank(%v, %v) = %v, want %v", tc.lexical, tc.vector, got, tc.want)
			}
		})
	}
}

// Duplicates within one list would otherwise inflate an article's score enough
// to beat genuine cross-list agreement.
func TestFuseByReciprocalRankKeepsResultsUnique(t *testing.T) {
	got := fuseByReciprocalRank([]int64{1, 1, 2}, []int64{2})
	seen := make(map[int64]bool, len(got))
	for _, id := range got {
		if seen[id] {
			t.Fatalf("fused list repeats id %d: %v", id, got)
		}
		seen[id] = true
	}
}

// The nearest-neighbour scan must stand alone in a derived table. MariaDB only
// uses the HNSW index for a bare ORDER BY VEC_DISTANCE ... LIMIT over the one
// table, so joining articles in to filter by visibility (the obvious way to
// write this, and how it was first written) silently disqualifies the index
// and turns the query into a full scan of every stored vector plus a filesort.
// On the production corpus that was 360ms against 7ms.
//
// This asserts the query's shape rather than its plan on purpose. An EXPLAIN
// test cannot do the job: on a small table the optimizer picks the vector index
// even for the join form, so the two shapes only diverge at a corpus size no
// unit test should have to build. Shape is the thing that is actually load-
// bearing, and it is the thing a well-meaning rewrite would break.
func TestBuildVectorNeighbourQueryKeepsTheScanIndexEligible(t *testing.T) {
	query := buildVectorNeighbourQuery(50)

	orderBy := strings.Index(query, "ORDER BY `d`")
	if orderBy < 0 {
		t.Fatalf("query no longer orders the inner scan by distance:\n%s", query)
	}
	// Nothing may join before the distance ordering; that is exactly what
	// disqualifies the index.
	if inner := query[:orderBy]; strings.Contains(strings.ToUpper(inner), "JOIN") {
		t.Errorf("the vector scan joins another table before ordering by distance, which disqualifies the HNSW index:\n%s", query)
	}
	if !strings.Contains(query, "JOIN articles") {
		t.Errorf("the visibility filter is gone; unpublished articles could surface in search:\n%s", query)
	}
	// The outer re-sort is what makes the ranking survive the join.
	if !strings.Contains(query, "ORDER BY nn.`d`") {
		t.Errorf("the outer query does not re-sort by distance, so RRF would consume an arbitrary order:\n%s", query)
	}
	// Over-fetch, or the visibility filter eats into the requested page.
	if !strings.Contains(query, "LIMIT "+strconv.Itoa(50*vectorOverFetch)) {
		t.Errorf("the inner scan does not over-fetch, so filtered rows shrink the result page:\n%s", query)
	}
}
