package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/internal/models"
)

func TestGetWordangleDayRefusesPreLaunchWithoutDB(t *testing.T) {
	// A nil connection panics if touched, so a 404 here proves the day was
	// refused before the retained used-word row could be read.
	handler := GetWordangleDay(nil)
	for _, day := range []string{"2026-10-08", "2026-10-04", "2020-01-01"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/wordangle/days/"+day, nil)
		req.SetPathValue("date", day)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: status %d, want 404", day, rec.Code)
		}
	}
}

func TestSplitWordangleManageHidesPreLaunchHistory(t *testing.T) {
	num := func(n int) *int { return &n }
	words := []models.WordangleWord{
		{Word: "factor", Date: "2026-10-04", Number: num(-5)},
		{Word: "harder", RetiredOn: "2026-10-08", Number: num(-1)},
		{Word: "crane", Date: "2026-10-09", Number: num(0)},
		{Word: "orange", RetiredOn: "2026-10-10", Number: num(1)},
		{Word: "planet", Date: "2026-10-10", Number: num(1)},
		{Word: "garden", Date: "2026-10-12", Number: num(3)},
	}
	today := time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC)

	resp := splitWordangleManage(words, today)

	if got := wordangleWordList(resp.Seen); got != "planet orange crane" {
		t.Fatalf("seen = %q, want launch-day onward, newest first", got)
	}
	if got := wordangleWordList(resp.Queue); got != "garden" {
		t.Fatalf("queue = %q, want garden", got)
	}
}

func TestSplitWordangleManageQueueUnchangedBeforeLaunch(t *testing.T) {
	// Before launch, today's and future words still show in the queue even
	// though their numbers are negative.
	num := func(n int) *int { return &n }
	words := []models.WordangleWord{
		{Word: "signal", Date: "2026-10-06", Number: num(-3)},
		{Word: "mature", Date: "2026-10-07", Number: num(-2)},
		{Word: "crane", Date: "2026-10-09", Number: num(0)},
	}
	today := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	resp := splitWordangleManage(words, today)

	if got := wordangleWordList(resp.Queue); got != "mature crane" {
		t.Fatalf("queue = %q, want mature crane", got)
	}
	if len(resp.Seen) != 0 {
		t.Fatalf("seen = %+v, want pre-launch history hidden", resp.Seen)
	}
}

func wordangleWordList(words []models.WordangleWord) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += " "
		}
		out += w.Word
	}
	return out
}
