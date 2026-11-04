package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
	"server/internal/trimeline"
)

// Needs a real MariaDB: the JSON picks column and the unique date index are
// what a mock would not enforce. Skips unless CMS_TEST_DSN is set, like the
// wordangle integration tests. Recreates the articles table the way the
// search integration tests do, seeded with a pool spanning enough decades
// for the constraint solver.
func trimelineTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping trimeline database integration test")
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+trimelineTable); err != nil {
		t.Fatalf("drop trimeline table: %v", err)
	}
	if err := EnsureTrimelineTable(ctx, conn); err != nil {
		t.Fatalf("ensure trimeline table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title LONGTEXT,
			slug VARCHAR(255) NOT NULL UNIQUE,
			tags LONGTEXT,
			`+"`text`"+` LONGTEXT,
			pub_date DATETIME NULL,
			scheduled_pub_date DATETIME NULL,
			archived_at DATETIME NULL,
			noindex BOOL NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}
	t.Cleanup(func() { conn.Exec("DROP TABLE IF EXISTS articles") })
	return conn
}

func seedTrimelineArticle(t *testing.T, conn *sql.DB, slug, title string, year int) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, pub_date) VALUES (?, ?, ?)",
		title, slug, fmt.Sprintf("%d-03-01 12:00:00", year),
	); err != nil {
		t.Fatalf("seed article %s: %v", slug, err)
	}
}

func TestTrimeline_GenerateFillsOnlyEmptyDaysWithValidPuzzles(t *testing.T) {
	conn := trimelineTestDB(t)
	ctx := context.Background()

	// A healthy pool: two headlines per decade from the 1980s on.
	for i, year := range []int{1982, 1987, 1993, 1996, 2003, 2007, 2012, 2016, 2021, 2024} {
		seedTrimelineArticle(t, conn, fmt.Sprintf("story-%d", year),
			fmt.Sprintf("Campus reacts to major policy change %d", i), year)
	}

	today := time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	filled, err := GenerateTrimelinePuzzles(ctx, conn, today, today.AddDate(0, 0, 6), "ed")
	if err != nil || filled != 7 {
		t.Fatalf("generate week: filled=%d err=%v, want 7", filled, err)
	}

	puzzles, err := ListTrimelinePuzzles(ctx, conn)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(puzzles) != 7 {
		t.Fatalf("got %d puzzles, want 7", len(puzzles))
	}
	for _, puzzle := range puzzles {
		if len(puzzle.Headlines) != trimeline.PicksPerPuzzle {
			t.Fatalf("day %s has %d picks, want %d", puzzle.Date, len(puzzle.Headlines), trimeline.PicksPerPuzzle)
		}
		for _, headline := range puzzle.Headlines {
			if !trimeline.ValidTitle(headline.Title) {
				t.Fatalf("day %s shipped a leaking title %q", puzzle.Date, headline.Title)
			}
		}
	}

	// Second run is a no-op: every day is filled.
	filled, err = GenerateTrimelinePuzzles(ctx, conn, today, today.AddDate(0, 0, 6), "ed")
	if err != nil || filled != 0 {
		t.Fatalf("regenerate: filled=%d err=%v, want 0 (days must not be replaced)", filled, err)
	}
}

func TestTrimeline_GenerateRefusesDegeneratePool(t *testing.T) {
	conn := trimelineTestDB(t)
	ctx := context.Background()

	// All candidates from one decade, clustered: no valid puzzle exists.
	for i, year := range []int{2011, 2012, 2012, 2013, 2013} {
		seedTrimelineArticle(t, conn, fmt.Sprintf("cluster-%d", year),
			fmt.Sprintf("A story about campus life, part %d", i), year)
	}

	today := time.Date(2026, time.November, 10, 0, 0, 0, 0, time.UTC)
	filled, err := GenerateTrimelinePuzzles(ctx, conn, today, today, "ed")
	if err != ErrTrimelineNoPuzzle {
		t.Fatalf("generate degenerate: err=%v filled=%d, want ErrTrimelineNoPuzzle", err, filled)
	}
	if filled != 0 {
		t.Fatalf("degenerate pool filled %d days, want 0", filled)
	}
}

func TestTrimeline_SeededDayIsLeftAlone(t *testing.T) {
	conn := trimelineTestDB(t)
	ctx := context.Background()

	day2 := time.Date(2026, time.November, 12, 0, 0, 0, 0, time.UTC)
	if err := insertTrimelinePuzzle(ctx, conn, day2, trimelinePlaceholderPuzzle("kept"), "ed"); err != nil {
		t.Fatalf("seed day 2 puzzle: %v", err)
	}

	puzzle, err := GetTrimelinePuzzleByDate(ctx, conn, day2)
	if err != nil {
		t.Fatalf("read seeded puzzle: %v", err)
	}
	if puzzle.SetBy != "ed" || len(puzzle.Headlines) != trimeline.PicksPerPuzzle {
		t.Fatalf("seeded puzzle round-trip mismatch: %+v", puzzle)
	}
}

// trimelinePlaceholderPuzzle builds a five-pick puzzle for tests that only
// need a row to exist.
func trimelinePlaceholderPuzzle(slug string) models.TrimelinePuzzle {
	puzzle := models.TrimelinePuzzle{Headlines: []models.TrimelineHeadline{}}
	at := time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < trimeline.PicksPerPuzzle; i++ {
		puzzle.Headlines = append(puzzle.Headlines, models.TrimelineHeadline{
			Title: "Placeholder headline number " + slug, Slug: slug,
			PubDate: at.AddDate(i*5, 0, 0).Format(time.RFC3339),
		})
	}
	return puzzle
}
