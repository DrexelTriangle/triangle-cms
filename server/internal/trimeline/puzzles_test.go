package trimeline

import (
	"strings"
	"testing"
	"time"
)

func TestPuzzleNumberCountsFromLaunch(t *testing.T) {
	launch := time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	if got := PuzzleNumber(launch); got != 0 {
		t.Fatalf("launch day = %d, want 0", got)
	}
	if got := PuzzleNumber(launch.AddDate(0, 0, 1)); got != 1 {
		t.Fatalf("day after launch = %d, want 1", got)
	}
	if got := PuzzleNumber(launch.AddDate(0, 0, -1)); got != -1 {
		t.Fatalf("day before launch = %d, want -1", got)
	}
}

func TestValidTitleRejectsDateLeaks(t *testing.T) {
	bad := []string{
		"Drexel unveils new plan",                      // 14 runes, too short
		strings.Repeat("a", 121),                       // too long
		"Drexel wins big in 2019 championship",         // year
		"Campus reacts to the 1999 merger",             // year
		"Students gather for the 50th annual fest",     // ordinal + annual
		"The 1st-ever dragon boat race hits the river", // ordinal
		"This year's enrollment breaks records",        // this year
	}
	for _, title := range bad {
		if ValidTitle(title) {
			t.Fatalf("ValidTitle(%q) = true, want false", title)
		}
	}
	good := []string{
		"Drexel unveils new campus plan",
		"Students gather for dragon boat race on the river",
		"Board approves tuition freeze after long debate",
	}
	for _, title := range good {
		if !ValidTitle(title) {
			t.Fatalf("ValidTitle(%q) = false, want true", title)
		}
	}
}

func TestBuildPuzzleReturnsChronologicalValidPicks(t *testing.T) {
	// Candidates span several decades, plus a few that must be filtered out.
	at := func(year, month, day int) time.Time {
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	}
	candidates := []Headline{
		{Slug: "a", Title: "Tuition hike sparks campus-wide protest", PubDate: at(1985, 4, 3)},
		{Slug: "b", Title: "Drexel unveils new campus plan", PubDate: at(1992, 9, 12)},
		{Slug: "c", Title: "Basketball team clinches conference title", PubDate: at(1993, 1, 20)}, // 1 year from b
		{Slug: "d", Title: "New dormitory opens its doors to students", PubDate: at(2001, 8, 30)},
		{Slug: "e", Title: "Mascot change debated by student senate", PubDate: at(2009, 3, 15)},
		{Slug: "f", Title: "Researchers win major federal grant", PubDate: at(2015, 10, 7)},
		{Slug: "g", Title: "The 50th annual fest draws a crowd", PubDate: at(2010, 5, 5)}, // invalid title
		{Slug: "h", Title: "Too short", PubDate: at(1997, 2, 2)},                          // invalid title
	}

	picks, err := BuildPuzzle(candidates)
	if err != nil {
		t.Fatalf("BuildPuzzle: %v", err)
	}
	if len(picks) != PicksPerPuzzle {
		t.Fatalf("got %d picks, want %d", len(picks), PicksPerPuzzle)
	}
	for _, pick := range picks {
		if !ValidTitle(pick.Title) {
			t.Fatalf("pick %q failed ValidTitle", pick.Title)
		}
	}
	// Chronological order.
	for i := 1; i < len(picks); i++ {
		if picks[i].PubDate.Before(picks[i-1].PubDate) {
			t.Fatalf("picks not chronological: %v before %v", picks[i].PubDate, picks[i-1].PubDate)
		}
	}
	// At least three distinct decades, two-year gaps.
	decades := map[int]bool{}
	for i, pick := range picks {
		decades[pick.PubDate.Year()/10] = true
		if i > 0 && pick.PubDate.Year()-picks[i-1].PubDate.Year() < 2 {
			t.Fatalf("gap under 2 years between %d and %d", picks[i-1].PubDate.Year(), pick.PubDate.Year())
		}
	}
	if len(decades) < 3 {
		t.Fatalf("got %d decades, want at least 3", len(decades))
	}
}

func TestBuildPuzzleRefusesDegeneratePools(t *testing.T) {
	at := func(year int) time.Time { return time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC) }
	// All from one decade and close together: no valid puzzle exists.
	tight := []Headline{
		{Slug: "a", Title: "First headline about campus events", PubDate: at(2010)},
		{Slug: "b", Title: "Second headline about campus events", PubDate: at(2011)},
		{Slug: "c", Title: "Third headline about campus events", PubDate: at(2011)},
		{Slug: "d", Title: "Fourth headline about campus events", PubDate: at(2012)},
		{Slug: "e", Title: "Fifth headline about campus events", PubDate: at(2012)},
	}
	if _, err := BuildPuzzle(tight); err != ErrNoPuzzle {
		t.Fatalf("BuildPuzzle(tight) err = %v, want ErrNoPuzzle", err)
	}
	// Too few candidates at all.
	short := tight[:3]
	if _, err := BuildPuzzle(short); err != ErrNoPuzzle {
		t.Fatalf("BuildPuzzle(short) err = %v, want ErrNoPuzzle", err)
	}
}

func TestShuffleKeepsContentsAndInput(t *testing.T) {
	at := func(year int) time.Time { return time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC) }
	in := []Headline{
		{Slug: "a", PubDate: at(1990)},
		{Slug: "b", PubDate: at(1995)},
		{Slug: "c", PubDate: at(2000)},
	}
	before := append([]Headline(nil), in...)
	out := Shuffle(in)
	if len(out) != len(in) {
		t.Fatalf("shuffle changed length: %d", len(out))
	}
	if in[0] != before[0] || in[1] != before[1] || in[2] != before[2] {
		t.Fatalf("shuffle mutated its input")
	}
	seen := map[string]bool{}
	for _, h := range out {
		seen[h.Slug] = true
	}
	if len(seen) != 3 {
		t.Fatalf("shuffle lost or duplicated elements: %v", seen)
	}
}

func TestTodayUsesPuzzleZone(t *testing.T) {
	// 2026-11-10 01:00 UTC is still 2026-11-09 evening in New York.
	now := time.Date(2026, time.November, 10, 1, 0, 0, 0, time.UTC)
	today := Today(now)
	if today.Format(time.DateOnly) != "2026-11-09" {
		t.Fatalf("Today = %s, want 2026-11-09 (NY midnight rollover)", today.Format(time.DateOnly))
	}
}
