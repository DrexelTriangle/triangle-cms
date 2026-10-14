package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// Needs a real MariaDB: the seen-before rule is the unique index, which a mock
// would not enforce. Skips unless CMS_TEST_DSN is set, like the poll tests.
func wordangleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping wordangle database integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+wordangleTable); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if err := EnsureWordangleTable(ctx, conn); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	pinWordangleClock(t, waToday.Add(16*time.Hour))
	return conn
}

var waToday = time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)

func waDay(offset int) time.Time { return waToday.AddDate(0, 0, offset) }

func pinWordangleClock(t *testing.T, now time.Time) {
	t.Helper()
	wordangleNow = func() time.Time { return now }
	t.Cleanup(func() { wordangleNow = time.Now })
}

func TestWordangle_GenerateFillsOnlyEmptyDaysWithUniqueWords(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	if _, err := SetWordangleWord(ctx, conn, waDay(2), "abacus", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatalf("set custom word: %v", err)
	}
	filled, err := GenerateWordangleWords(ctx, conn, waToday, waDay(6), "ed")
	if err != nil || filled != 6 {
		t.Fatalf("generate week: filled=%d err=%v, want 6 days around the custom one", filled, err)
	}
	if got, _ := GetWordangleWordByDate(ctx, conn, waDay(2)); got.Word != "abacus" {
		t.Fatalf("generate overwrote a filled day: %+v", got)
	}

	words, err := ListWordangleWords(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w.Word] {
			t.Fatalf("word %q scheduled twice", w.Word)
		}
		seen[w.Word] = true
	}
	if len(words) != 7 {
		t.Fatalf("got %d rows, want 7", len(words))
	}

	// A second run over the same window has nothing left to fill.
	if filled, err := GenerateWordangleWords(ctx, conn, waToday, waDay(6), "ed"); err != nil || filled != 0 {
		t.Fatalf("regenerate full week: filled=%d err=%v", filled, err)
	}
}

func TestWordangle_CustomWordRefusedWhenQueuedOrSeen(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	// Yesterday's word is on the seen-before list.
	yesterday := waDay(-1)
	pinWordangleClock(t, yesterday.Add(16*time.Hour))
	if _, err := SetWordangleWord(ctx, conn, yesterday, "planet", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	pinWordangleClock(t, waToday.Add(16*time.Hour))
	if _, err := SetWordangleWord(ctx, conn, waDay(3), "planet", models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordangleWordTaken) {
		t.Fatalf("seen word: err = %v, want ErrWordangleWordTaken", err)
	}

	// A word queued for one day can't go on another.
	if _, err := SetWordangleWord(ctx, conn, waDay(1), "garden", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWordangleWord(ctx, conn, waDay(5), "garden", models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordangleWordTaken) {
		t.Fatalf("queued word: err = %v, want ErrWordangleWordTaken", err)
	}

	// Writing a day's own word back is not a collision.
	if got, err := SetWordangleWord(ctx, conn, waDay(1), "garden", models.WordangleSourceCustom, "ed"); err != nil || got.Word != "garden" {
		t.Fatalf("rewrite same word: %+v %v", got, err)
	}
}

func TestWordangle_ReplacingFutureFreesWordButReplacingTodayRetiresIt(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	// Future: nobody saw "garden", so it is free again.
	if _, err := SetWordangleWord(ctx, conn, waDay(2), "garden", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWordangleWord(ctx, conn, waDay(2), "silver", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	if _, err := LookupWordangleWord(ctx, conn, "garden"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replaced future word should be gone, lookup err = %v", err)
	}

	// Today: readers may have played "orange", so it stays seen.
	if _, err := SetWordangleWord(ctx, conn, waToday, "orange", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	got, err := RegenerateWordangleWord(ctx, conn, waToday, "ed")
	if err != nil || got.Word == "orange" || got.Source != models.WordangleSourceGenerated {
		t.Fatalf("regenerate today: %+v %v", got, err)
	}
	retired, err := LookupWordangleWord(ctx, conn, "orange")
	if err != nil || retired.RetiredOn != waToday.Format(time.DateOnly) || retired.Date != "" {
		t.Fatalf("today's replaced word should be retired: %+v %v", retired, err)
	}
	if _, err := SetWordangleWord(ctx, conn, waDay(4), "orange", models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordangleWordTaken) {
		t.Fatalf("retired word reused: err = %v", err)
	}
}

func TestWordangle_ReplaceAfterMidnightRetiresInsteadOfFreeing(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	if _, err := SetWordangleWord(ctx, conn, waDay(1), "garden", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	// The editor loaded the page on Oct 4, but the write lands after midnight,
	// when "garden" is already live.
	pinWordangleClock(t, waDay(1).Add(5*time.Hour))
	if _, err := SetWordangleWord(ctx, conn, waDay(1), "silver", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	if got, err := LookupWordangleWord(ctx, conn, "garden"); err != nil || got.RetiredOn == "" {
		t.Fatalf("word replaced after going live must be retired: %+v %v", got, err)
	}
	if _, err := SetWordangleWord(ctx, conn, waToday, "orange", models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordanglePastDay) {
		t.Fatalf("write to a day that has passed: err = %v, want ErrWordanglePastDay", err)
	}
}

func TestWordangle_EmptyTodayIsFilledAndRecorded(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	first, err := EnsureTodaysWordangleWord(ctx, conn)
	if err != nil || first.Date != waToday.Format(time.DateOnly) || first.Word == "" {
		t.Fatalf("ensure today: %+v %v", first, err)
	}
	again, err := EnsureTodaysWordangleWord(ctx, conn)
	if err != nil || again.Word != first.Word {
		t.Fatalf("second read must return the same word: %+v %v", again, err)
	}
	if _, err := SetWordangleWord(ctx, conn, waDay(3), first.Word, models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordangleWordTaken) {
		t.Fatalf("auto-filled word reused: err = %v", err)
	}
}

func TestWordangle_PreLaunchWordsStayUsed(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	// waToday (Oct 4) is before launch, so these rows have negative numbers.
	// The manage list hides them, but they must keep blocking reuse.
	preLaunch := []string{"factor", "harder", "mature", "signal"}
	for i, word := range preLaunch {
		if _, err := SetWordangleWord(ctx, conn, waDay(i), word, models.WordangleSourceGenerated, ""); err != nil {
			t.Fatalf("seed %s: %v", word, err)
		}
	}
	pinWordangleClock(t, time.Date(2026, time.October, 11, 16, 0, 0, 0, time.UTC))

	got, err := LookupWordangleWord(ctx, conn, "factor")
	if err != nil || got.Number == nil || *got.Number >= 0 {
		t.Fatalf("pre-launch row should remain with a negative number: %+v %v", got, err)
	}
	unused, err := unusedWordangleTargets(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range unused {
		for _, used := range preLaunch {
			if word == used {
				t.Fatalf("pre-launch word %q offered as a generated candidate", word)
			}
		}
	}
	tomorrow := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)
	for _, word := range preLaunch {
		if _, err := SetWordangleWord(ctx, conn, tomorrow, word, models.WordangleSourceCustom, "ed"); !errors.Is(err, ErrWordangleWordTaken) {
			t.Fatalf("custom reuse of pre-launch %q: err = %v, want ErrWordangleWordTaken", word, err)
		}
	}
}

func TestWordangle_GenerateOverFilledWindowIsNotExhaustion(t *testing.T) {
	conn := wordangleTestDB(t)
	ctx := context.Background()

	if _, err := SetWordangleWord(ctx, conn, waToday, "abacus", models.WordangleSourceCustom, "ed"); err != nil {
		t.Fatal(err)
	}
	// Use up the whole generated pool on days outside the window.
	if _, err := GenerateWordangleWords(ctx, conn, waDay(10), waDay(10+2000), "ed"); !errors.Is(err, ErrWordanglePoolExhausted) {
		t.Fatalf("expected the pool to run out, err = %v", err)
	}
	if filled, err := GenerateWordangleWords(ctx, conn, waToday, waToday, "ed"); err != nil || filled != 0 {
		t.Fatalf("filled window: filled=%d err=%v, want 0 and no error", filled, err)
	}
}
