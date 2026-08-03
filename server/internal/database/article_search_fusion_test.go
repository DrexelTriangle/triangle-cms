package database

import (
	"reflect"
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
			// still surface -- this is the "story about parking permits that never
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
