// Package trimeline holds the calendar rules and pick-quality logic behind
// Tri-meline, the daily game where readers order five archive headlines
// oldest to newest.
//
// The database layer samples candidate headlines from the articles table;
// this package is the pure part — title quality filters, the draw that turns
// candidates into a solvable puzzle, and the puzzle calendar shared with
// the public site.
package trimeline

import (
	"errors"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // the container image ships no zoneinfo
)

// PicksPerPuzzle is how many headlines a day's puzzle draws.
const PicksPerPuzzle = 5

// maxDrawAttempts is how many random draws BuildPuzzle tries before giving
// up. A healthy archive satisfies the constraints in a handful of draws, so
// running out means the candidate pool is too thin or too clustered.
const maxDrawAttempts = 500

// ErrNoPuzzle means the candidates could not yield a valid puzzle.
var ErrNoPuzzle = errors.New("trimeline candidates cannot yield a valid puzzle")

// puzzleZone is where puzzles roll over at midnight, matching Wordangle
// (DAILY_TZ in the Scalene game code).
var puzzleZone = mustLoadLocation("America/New_York")

// launchDay is the public launch day of Tri-meline, a Monday in
// Philadelphia. Puzzle #0 is this day; each later day adds one.
var launchDay = time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)

// Headline is one article pick. PubDate is the article's publication time;
// the database layer fills it, the puzzle logic only reads it.
type Headline struct {
	Slug    string
	Title   string
	PubDate time.Time
}

// Today is the current puzzle date, as a midnight-UTC time.Time so it
// compares and formats like the DATE values read back from the database.
func Today(now time.Time) time.Time {
	y, m, d := now.In(puzzleZone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDate parses a YYYY-MM-DD puzzle date.
func ParseDate(value string) (time.Time, error) {
	return time.Parse(time.DateOnly, strings.TrimSpace(value))
}

// PuzzleNumber is the "Tri-meline #N" shown for a date. #0 is 2026-11-10,
// the public launch day; days before it are negative.
func PuzzleNumber(date time.Time) int {
	return int(date.Sub(launchDay).Hours() / 24)
}

// A title that leaks its own date gives the answer away, so a pick must not
// name a year, an anniversary ordinal ("50th"), or a year-relative phrase.
var (
	yearPattern       = regexp.MustCompile(`(19|20)\d{2}`)
	ordinalPattern    = regexp.MustCompile(`(?i)\b\d{1,3}(st|nd|rd|th)\b`)
	yearRelativeWords = []string{"annual", "this year"}
)

// ValidTitle reports whether a headline title may appear in a puzzle:
// neither too short to be a real headline nor too long to display, and free
// of anything that dates it at a glance.
func ValidTitle(title string) bool {
	length := len([]rune(title))
	if length < 15 || length > 120 {
		return false
	}
	if yearPattern.MatchString(title) || ordinalPattern.MatchString(title) {
		return false
	}
	lower := strings.ToLower(title)
	for _, word := range yearRelativeWords {
		if strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

// BuildPuzzle draws PicksPerPuzzle headlines from candidates as a puzzle:
// the picks must come from at least three distinct decades, consecutive
// picks (in chronological order) must be at least two years apart, and
// every title must pass ValidTitle. The returned picks are in chronological
// order; the caller shuffles them before showing the puzzle.
//
// Draw failures are retried with fresh randomness up to maxDrawAttempts
// times, then reported as ErrNoPuzzle rather than shipping a degenerate
// puzzle (picks a reader can order from a single glance, or worse, a tie).
func BuildPuzzle(candidates []Headline) ([]Headline, error) {
	valid := make([]Headline, 0, len(candidates))
	for _, h := range candidates {
		if ValidTitle(h.Title) {
			valid = append(valid, h)
		}
	}
	if len(valid) < PicksPerPuzzle {
		return nil, ErrNoPuzzle
	}

	for attempt := 0; attempt < maxDrawAttempts; attempt++ {
		picks := draw(valid)
		sort.Slice(picks, func(i, j int) bool { return picks[i].PubDate.Before(picks[j].PubDate) })
		if validPicks(picks) {
			return picks, nil
		}
	}
	return nil, ErrNoPuzzle
}

// Shuffle returns the headlines in random order, leaving the input alone.
// The public day endpoint serves puzzles shuffled so the answer isn't
// pre-sorted in the response body.
func Shuffle(headlines []Headline) []Headline {
	out := append([]Headline(nil), headlines...)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// draw picks PicksPerPuzzle random entries from pool, without replacement.
func draw(pool []Headline) []Headline {
	picks := append([]Headline(nil), pool...)
	rand.Shuffle(len(picks), func(i, j int) { picks[i], picks[j] = picks[j], picks[i] })
	return picks[:PicksPerPuzzle]
}

// validPicks checks the solvability rules on chronologically ordered picks.
func validPicks(picks []Headline) bool {
	if len(picks) != PicksPerPuzzle {
		return false
	}
	decades := map[int]bool{}
	for i, pick := range picks {
		year := pick.PubDate.Year()
		decades[year/10] = true
		if i > 0 && year-picks[i-1].PubDate.Year() < 2 {
			return false
		}
	}
	return len(decades) >= 3
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
