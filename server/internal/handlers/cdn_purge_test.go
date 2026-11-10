package handlers

import (
	"slices"
	"testing"
)

// Scalene caches articles for 30 days, so a footer or site-title change must
// evict every article page; "/article/" travels as a prefix and does that.
func TestPurgeSiteChrome_EvictsEveryArticlePage(t *testing.T) {
	rec, waitFlush := installTestPurger(t)

	purgeSiteChrome()
	waitFlush()

	want := []string{
		"https://www.thetriangle.org/",
		"https://www.thetriangle.org/article/",
	}
	if got := rec.got(); !slices.Equal(got, want) {
		t.Fatalf("purged %v, want %v", got, want)
	}
}
