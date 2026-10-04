package wordangle

import (
	"testing"
	"time"
)

func TestTargetsAreSixLetterDictionaryWords(t *testing.T) {
	if len(Targets()) < 900 {
		t.Fatalf("expected the ~1000-word target pool, got %d", len(Targets()))
	}
	for _, word := range Targets() {
		if len(word) != WordLength || !IsWord(word) {
			t.Fatalf("target %q is not a six-letter dictionary word", word)
		}
	}
}

func TestIsWordUsesFullDictionary(t *testing.T) {
	if !IsWord("abacus") {
		t.Fatal("expected a SCOWL word outside the target pool to be playable")
	}
	for _, word := range []string{"", "abc", "zzzzzz", "planets"} {
		if IsWord(word) {
			t.Fatalf("IsWord(%q) = true", word)
		}
	}
}

func TestTodayRollsOverAtMidnightInPhiladelphia(t *testing.T) {
	// 03:30 UTC on Oct 5 is still 23:30 on Oct 4 in New York.
	got := Today(time.Date(2026, time.October, 5, 3, 30, 0, 0, time.UTC))
	if want := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Today = %v, want %v", got, want)
	}
}

func TestPuzzleNumberKeepsExistingNumbering(t *testing.T) {
	// The game showed #277 for 2026-10-04 before the numbering moved here.
	if got := PuzzleNumber(time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)); got != 277 {
		t.Fatalf("PuzzleNumber = %d, want 277", got)
	}
}

func TestListFileServesOnlyPublishedLists(t *testing.T) {
	for _, name := range []string{"targets.txt", "allowed-2.txt", "allowed-6.txt", "SCOWL-Copyright.txt"} {
		if body, ok := ListFile(name); !ok || len(body) == 0 {
			t.Fatalf("ListFile(%q) missing", name)
		}
	}
	for _, name := range []string{"words.go", "../wordangle/targets.txt", "allowed-7.txt", ""} {
		if _, ok := ListFile(name); ok {
			t.Fatalf("ListFile(%q) should not be served", name)
		}
	}
}
