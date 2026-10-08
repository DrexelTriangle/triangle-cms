// Package wordangle holds the word lists and calendar rules behind the
// Wordangle queue that editors manage in the CMS.
//
// This directory is the only copy of Wordangle's word lists: the CMS checks
// and generates answers against them, and serves them to the public site's
// game through /v1/wordangle/lists/<file>. scripts/build-wordangle-wordlists.py
// rebuilds the allowed-N lists; targets.txt is hand-maintained.
package wordangle

import (
	"embed"
	"io/fs"
	"strings"
	"time"
	_ "time/tzdata" // the container image ships no zoneinfo
)

// WordLength is the length of every Wordangle answer.
const WordLength = 6

//go:embed targets.txt allowed-*.txt SCOWL-Copyright.txt
var listFS embed.FS

var (
	targets    = parseWords(mustRead("targets.txt"))
	dictionary = toSet(append(parseWords(mustRead("allowed-6.txt")), targets...))
)

// ListFile returns one of the published word-list files by name, or false for
// anything else.
func ListFile(name string) ([]byte, bool) {
	if strings.Contains(name, "/") || !strings.HasSuffix(name, ".txt") {
		return nil, false
	}
	body, err := fs.ReadFile(listFS, name)
	return body, err == nil
}

func mustRead(name string) string {
	body, err := fs.ReadFile(listFS, name)
	if err != nil {
		panic(err)
	}
	return string(body)
}

// puzzleZone is where puzzles roll over at midnight. Scalene asks for the day
// by its own clock in the same zone (DAILY_TZ in src/utils/wordangle/game.ts).
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

// PuzzleNumber is the "Wordangle #N" shown for a date. #0 is 2026-10-09, the
// public launch day in Philadelphia, and each later day adds one.
func PuzzleNumber(date time.Time) int {
	launch := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
	return int(date.Sub(launch).Hours() / 24)
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
