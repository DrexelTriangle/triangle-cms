package handlers

import (
	"testing"

	"server/internal/models"
)

func newsBlock(ids ...int64) []models.ArticleListItem {
	items := make([]models.ArticleListItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, models.ArticleListItem{ID: id})
	}
	return items
}

func newsIDs(items []models.ArticleListItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSpliceFeaturedLeads(t *testing.T) {
	tests := []struct {
		name     string
		news     []int64
		featured []int64
		limit    int
		want     []int64
	}{
		{
			// A pinned sports story is not in the news block at all, so the
			// block grows by one and the oldest card falls off the end.
			name:     "article from another section takes the lead slot",
			news:     []int64{5, 4, 3},
			featured: []int64{99},
			limit:    3,
			want:     []int64{99, 5, 4},
		},
		{
			// The layout's big centre card is news[0]; pinning a story already
			// in the block must move it, not duplicate it.
			name:     "news article is promoted rather than duplicated",
			news:     []int64{5, 4, 3},
			featured: []int64{3},
			limit:    3,
			want:     []int64{3, 5, 4},
		},
		{
			name:     "already leading stays put",
			news:     []int64{5, 4, 3},
			featured: []int64{5},
			limit:    3,
			want:     []int64{5, 4, 3},
		},
		{
			// Two pins hold the first two slots in the order given, which is
			// newest pin first. This is the case the second breaking story
			// exists for.
			name:     "two pins lead in order",
			news:     []int64{5, 4, 3},
			featured: []int64{99, 3},
			limit:    3,
			want:     []int64{99, 3, 5},
		},
		{
			name:     "a pin already in the block does not cost a slot twice",
			news:     []int64{5, 4, 3},
			featured: []int64{4, 5},
			limit:    3,
			want:     []int64{4, 5, 3},
		},
		{
			name:     "short block is not padded or trimmed",
			news:     []int64{5},
			featured: []int64{99},
			limit:    13,
			want:     []int64{99, 5},
		},
		{
			name:     "empty news block still leads with the pins",
			news:     nil,
			featured: []int64{99, 98},
			limit:    13,
			want:     []int64{99, 98},
		},
		{
			name:     "nothing pinned leaves the block alone",
			news:     []int64{5, 4, 3},
			featured: nil,
			limit:    3,
			want:     []int64{5, 4, 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := spliceFeaturedLeads(newsBlock(tc.news...), newsBlock(tc.featured...), tc.limit)
			if !equalIDs(newsIDs(got), tc.want) {
				t.Errorf("news order = %v, want %v", newsIDs(got), tc.want)
			}
		})
	}
}
