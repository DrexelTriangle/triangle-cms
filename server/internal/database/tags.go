package database

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"time"
)

// PopularTag is one SEO tag and the number of articles carrying it.
type PopularTag struct {
	Name string `json:"name"`
	Uses int64  `json:"uses"`
}

// DefaultPopularTagsLimit is how many suggestions the editor gets when it does
// not ask for a specific number. The imported WordPress archive has a long tail
// of tags used exactly once, so the point of the cap is to keep the list to the
// ones a desk actually reaches for.
const DefaultPopularTagsLimit = 50

// MaxPopularTagsLimit bounds what a caller can ask for, since the whole result
// is built in memory.
const MaxPopularTagsLimit = 200

// popularTagsTTL is how long a computed ranking is served before it is rebuilt.
// Counting means scanning every article's tags, and the ranking moves by one
// article at a time, so a stale-by-minutes list is indistinguishable from a
// fresh one to the person picking from it.
const popularTagsTTL = 10 * time.Minute

// popularTagsCache holds the full ranking (MaxPopularTagsLimit entries), which
// every limit is then sliced from, so varying the limit cannot cause a rebuild.
//
// The mutex is held across the refresh rather than only around the swap: it
// serializes concurrent misses so a cold cache costs one scan instead of one
// per in-flight request.
var (
	popularTagsMu      sync.Mutex
	popularTagsCached  []PopularTag
	popularTagsFetched time.Time
)

// PopularTags returns the SEO tags most used across the archive, ranked by
// article count, for the editor's tag suggestions.
//
// There is no tags table: `articles`.`tags` holds a JSON array per article, so
// "most used" can only come from aggregating the column. The aggregation runs
// in Go rather than in SQL (JSON_TABLE) for two reasons. The column is not
// reliably JSON -- FormatTags falls back to a comma-joined string when
// marshalling fails, and articles with no tags store "" -- and parseStringListField
// already absorbs both spellings, so reusing it means the suggestion list cannot
// disagree with what the article editor itself reads back. Doing it here also
// makes the case folding below expressible at all.
//
// Archived articles are excluded; drafts are not. A tag on an unpublished draft
// is still a tag the desk is using, and suggestions are about what editors
// type, not about what readers can see.
func PopularTags(ctx context.Context, conn *sql.DB, limit int) ([]PopularTag, error) {
	if limit <= 0 {
		limit = DefaultPopularTagsLimit
	}
	if limit > MaxPopularTagsLimit {
		limit = MaxPopularTagsLimit
	}

	popularTagsMu.Lock()
	defer popularTagsMu.Unlock()

	if popularTagsCached == nil || time.Since(popularTagsFetched) > popularTagsTTL {
		ranked, err := rankArticleTags(ctx, conn)
		if err != nil {
			return nil, err
		}
		popularTagsCached = ranked
		popularTagsFetched = time.Now()
	}

	if limit > len(popularTagsCached) {
		limit = len(popularTagsCached)
	}
	// Copy, so a caller that sorts or truncates the slice cannot corrupt the
	// cache for everyone else.
	out := make([]PopularTag, limit)
	copy(out, popularTagsCached[:limit])
	return out, nil
}

// InvalidatePopularTags drops the cached ranking so the next read rebuilds it.
// Used by tests; article writes deliberately do not call it, because a tag that
// takes a few minutes to enter the suggestion list is not a defect worth a
// rescan per save.
func InvalidatePopularTags() {
	popularTagsMu.Lock()
	popularTagsCached = nil
	popularTagsFetched = time.Time{}
	popularTagsMu.Unlock()
}

// rankArticleTags scans the tag column and builds the full ranking.
func rankArticleTags(ctx context.Context, conn *sql.DB) ([]PopularTag, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT `tags` FROM `articles` WHERE `archived_at` IS NULL AND `tags` IS NOT NULL AND `tags` <> ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := make([]sql.NullString, 0, 256)
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		values = append(values, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rankTagValues(values), nil
}

// rankTagValues is the ranking itself, split from the query so the counting
// rules -- case folding, per-article de-duplication, the tie-break -- are
// testable without a database.
func rankTagValues(values []sql.NullString) []PopularTag {
	// Keyed by the lowercased tag: the archive carries "Drexel" and "drexel" as
	// separate strings, and offering both as separate suggestions is exactly the
	// duplicate-looking list this feature is meant to replace.
	type tally struct {
		uses     int64
		spelling map[string]int64
	}
	counts := make(map[string]*tally)

	for _, raw := range values {
		// Duplicates within one article must not count twice, or a
		// double-tagged article outranks two genuinely different ones.
		seen := make(map[string]bool)
		for _, tag := range parseStringListField(raw) {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true

			entry := counts[key]
			if entry == nil {
				entry = &tally{spelling: make(map[string]int64)}
				counts[key] = entry
			}
			entry.uses++
			entry.spelling[name]++
		}
	}

	ranked := make([]PopularTag, 0, len(counts))
	for _, entry := range counts {
		ranked = append(ranked, PopularTag{Name: displaySpelling(entry.spelling), Uses: entry.uses})
	}

	// Ties break on the name so the list is stable between rebuilds: a
	// suggestion row that reshuffles on refresh is worse than a slightly
	// arbitrary order.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Uses != ranked[j].Uses {
			return ranked[i].Uses > ranked[j].Uses
		}
		return ranked[i].Name < ranked[j].Name
	})

	if len(ranked) > MaxPopularTagsLimit {
		ranked = ranked[:MaxPopularTagsLimit]
	}
	return ranked
}

// displaySpelling picks which casing of a tag to show: the one the archive uses
// most, falling back to the lexicographically first so the choice is
// deterministic when two spellings are equally common.
func displaySpelling(spellings map[string]int64) string {
	best := ""
	var bestCount int64
	for spelling, count := range spellings {
		if count > bestCount || (count == bestCount && (best == "" || spelling < best)) {
			best = spelling
			bestCount = count
		}
	}
	return best
}
