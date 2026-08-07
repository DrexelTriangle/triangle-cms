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

// DefaultTagsLimit is how many suggestions a caller gets when it does not ask
// for a number. The imported WordPress archive has a long tail of tags used
// exactly once, so the point of the cap is to keep the list to the ones a desk
// actually reaches for.
const DefaultTagsLimit = 50

// MaxTagsLimit bounds what a caller can ask for. It caps the response, not the
// ranking: search reads every tag the archive has, and only the slice handed
// back is trimmed.
const MaxTagsLimit = 200

// tagRankingTTL is how long a computed ranking is served before it is rebuilt.
// Counting means scanning every article's tags, and the ranking moves by one
// article at a time, so a stale-by-minutes list is indistinguishable from a
// fresh one to the person picking from it.
const tagRankingTTL = 10 * time.Minute

// The cached ranking is every distinct tag in the archive, not just the popular
// ones, because search has to be able to find a tag used twice in 2019.
// That is ~20k entries against ~9k articles -- a couple of megabytes, and far
// cheaper than a per-keystroke scan of the article table.
//
// The mutex is held across the refresh rather than only around the swap: it
// serializes concurrent misses so a cold cache costs one scan instead of one
// per in-flight request.
var (
	tagRankingMu      sync.Mutex
	tagRankingCached  []PopularTag
	tagRankingFetched time.Time
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
	ranking, err := tagRanking(ctx, conn)
	if err != nil {
		return nil, err
	}
	return capTags(ranking, limit), nil
}

// SearchTags finds the tags whose text contains the query, so an editor can
// reach a tag the archive already has instead of retyping it -- and, more to
// the point, instead of coining a near-duplicate of one.
//
// It searches every tag, not the popular slice PopularTags hands back: a beat
// tag used twice in 2019 is exactly the kind of thing nobody remembers the
// exact spelling of, and it is nowhere near the top 50.
//
// Matches are ordered by how they match before how popular they are. Somebody
// typing "lacrosse" means the tag "lacrosse", and burying it under a
// better-used "Men's Lacrosse" makes the exact tag they asked for the one they
// have to hunt for.
func SearchTags(ctx context.Context, conn *sql.DB, query string, limit int) ([]PopularTag, error) {
	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" {
		return PopularTags(ctx, conn, limit)
	}

	ranking, err := tagRanking(ctx, conn)
	if err != nil {
		return nil, err
	}

	type scored struct {
		tag  PopularTag
		rank int
	}
	matches := make([]scored, 0, 64)
	for _, tag := range ranking {
		if rank := matchRank(strings.ToLower(tag.Name), normalized); rank >= 0 {
			matches = append(matches, scored{tag: tag, rank: rank})
		}
	}

	// Stable within a rank: the ranking is already ordered by uses then name,
	// so equally-good matches keep that order.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].rank < matches[j].rank })

	found := make([]PopularTag, 0, len(matches))
	for _, match := range matches {
		found = append(found, match.tag)
	}
	return capTags(found, limit), nil
}

// matchRank scores how a tag matches the query -- lower is better -- or returns
// -1 for no match. The tiers are the ones a person typing would expect: the
// tag they typed exactly, then tags starting with it, then tags with a word
// starting with it, then anything containing it.
func matchRank(name, query string) int {
	switch {
	case name == query:
		return 0
	case strings.HasPrefix(name, query):
		return 1
	case strings.Contains(name, " "+query):
		// A word boundary, so "lacrosse" reaches "Men's Lacrosse" ahead of a
		// tag that merely has the letters somewhere inside it.
		return 2
	case strings.Contains(name, query):
		return 3
	default:
		return -1
	}
}

// capTags trims a result set to the requested limit, copying so a caller that
// sorts or truncates the slice cannot corrupt the cache for everyone else.
func capTags(tags []PopularTag, limit int) []PopularTag {
	if limit <= 0 {
		limit = DefaultTagsLimit
	}
	if limit > MaxTagsLimit {
		limit = MaxTagsLimit
	}
	if limit > len(tags) {
		limit = len(tags)
	}
	out := make([]PopularTag, limit)
	copy(out, tags[:limit])
	return out
}

// tagRanking returns every tag in the archive, ranked by use, from the cache --
// rebuilding it when it has expired.
func tagRanking(ctx context.Context, conn *sql.DB) ([]PopularTag, error) {
	tagRankingMu.Lock()
	defer tagRankingMu.Unlock()

	if tagRankingCached == nil || time.Since(tagRankingFetched) > tagRankingTTL {
		ranked, err := rankArticleTags(ctx, conn)
		if err != nil {
			return nil, err
		}
		tagRankingCached = ranked
		tagRankingFetched = time.Now()
	}
	return tagRankingCached, nil
}

// InvalidatePopularTags drops the cached ranking so the next read rebuilds it.
// Used by tests; article writes deliberately do not call it, because a tag that
// takes a few minutes to enter the suggestion list is not a defect worth a
// rescan per save.
func InvalidatePopularTags() {
	tagRankingMu.Lock()
	tagRankingCached = nil
	tagRankingFetched = time.Time{}
	tagRankingMu.Unlock()
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
// testable without a database. Every tag is kept, not just the top slice:
// search reads this list, so trimming it here would make older tags
// unfindable rather than merely unpopular.
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
