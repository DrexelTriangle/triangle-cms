// Package wordangle holds the word lists and calendar rules behind the
// Wordangle queue that editors manage in the CMS.
//
// targets.txt, allowed-6.txt and SCOWL-Copyright.txt are copies of the files
// Scalene serves from public/wordangle/. Scalene's copies are canonical (its
// scripts/build-wordangle-wordlists.py writes them); refresh these alongside
// them so the CMS never queues a word the game would reject.
package wordangle

import (
	_ "embed"
	"strings"
	"time"
	_ "time/tzdata" // the container image ships no zoneinfo
)

// WordLength is the length of every Wordangle answer.
const WordLength = 6

//go:embed targets.txt
var targetsFile string

//go:embed allowed-6.txt
var allowedFile string

var (
	targets    = parseWords(targetsFile)
	dictionary = toSet(append(parseWords(allowedFile), targets...))
)

// puzzleZone is where puzzles roll over at midnight, matching DAILY_TZ in
// Scalene's src/utils/wordangle/game.ts.
var puzzleZone = mustLoadLocation("America/New_York")

// Targets returns the hand-picked answer pool that generated words come from.
func Targets() []string {
	return append([]string(nil), targets...)
}

// IsWord reports whether word (already normalized) is a playable six-letter
// answer: anything in the SCOWL guess dictionary, so an editor can write in a
// word that is not in the generated pool.
func IsWord(word string) bool {
	return dictionary[word]
}

// Normalize lowercases and trims an editor's input.
func Normalize(word string) string {
	return strings.ToLower(strings.TrimSpace(word))
}

// Today is the current puzzle date, as a midnight-UTC time.Time so it compares
// and formats like the DATE values read back from the database.
func Today(now time.Time) time.Time {
	y, m, d := now.In(puzzleZone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDate parses a YYYY-MM-DD puzzle date.
func ParseDate(value string) (time.Time, error) {
	return time.Parse(time.DateOnly, strings.TrimSpace(value))
}

// PuzzleNumber is the "Wordangle #N" shown for a date; #1 is 2026-01-01, as in
// Scalene's dailyNumber.
func PuzzleNumber(date time.Time) int {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return int(date.Sub(start).Hours()/24) + 1
}

func parseWords(text string) []string {
	var words []string
	for _, line := range strings.Split(text, "\n") {
		if word := Normalize(line); len(word) == WordLength {
			words = append(words, word)
		}
	}
	return words
}

func toSet(words []string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}
