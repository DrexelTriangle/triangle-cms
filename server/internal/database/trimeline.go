package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"server/internal/models"
	"server/internal/trimeline"
)

// The Tri-meline queue: one row per calendar day holding the day's five
// headline picks as JSON, in chronological order. Puzzles are drawn once and
// kept — a past puzzle must never change under a reader who already played
// it — and days are only ever filled, never rewritten.

const trimelineTable = "trimeline_puzzles"

const trimelineColumns = "puzzle_date, picks, source, COALESCE(set_by, ''), updated_at"

// trimelineCandidateLimit is how many headlines one draw samples from the
// archive. The candidate pool only needs to be a few times larger than the
// pick count for the constraint solver to find a puzzle quickly, but it is
// capped so a huge archive doesn't ship a multi-megabyte response per day.
const trimelineCandidateLimit = 500

var (
	// ErrTrimelineNoPuzzle means the archive draw could not yield a valid
	// puzzle after repeated attempts.
	ErrTrimelineNoPuzzle = errors.New("trimeline puzzle could not be generated")
	// ErrTrimelinePastDay is reserved for a future set/replace endpoint;
	// past puzzles are frozen and this is the error that will report it.
	ErrTrimelinePastDay = errors.New("trimeline day is in the past")
)

// trimelineNow is the clock the write path reads; tests pin it.
var trimelineNow = time.Now

func EnsureTrimelineTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema(trimelineTable))
	return err
}

// ListTrimelinePuzzles returns every row, oldest day first.
func ListTrimelinePuzzles(ctx context.Context, conn *sql.DB) ([]models.TrimelinePuzzle, error) {
	rows, err := conn.QueryContext(ctx, "SELECT "+trimelineColumns+" FROM "+trimelineTable+
		" ORDER BY puzzle_date, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var puzzles []models.TrimelinePuzzle
	for rows.Next() {
		puzzle, err := scanTrimelinePuzzle(rows)
		if err != nil {
			return nil, err
		}
		puzzles = append(puzzles, puzzle)
	}
	return puzzles, rows.Err()
}

// GetTrimelinePuzzleByDate returns the puzzle scheduled for date, or
// sql.ErrNoRows.
func GetTrimelinePuzzleByDate(ctx context.Context, conn *sql.DB, date time.Time) (models.TrimelinePuzzle, error) {
	row := conn.QueryRowContext(ctx, "SELECT "+trimelineColumns+" FROM "+trimelineTable+
		" WHERE puzzle_date = ?", date.Format(time.DateOnly))
	return scanTrimelinePuzzle(row)
}

// GenerateTrimelinePuzzles fills every empty day from `from` through `to`
// (inclusive) with a freshly drawn puzzle, leaving days that already have
// one alone. It returns how many days it filled; when the archive can't
// yield a valid puzzle for an empty day, it returns the count so far with
// ErrTrimelineNoPuzzle rather than shipping a degenerate one.
func GenerateTrimelinePuzzles(ctx context.Context, conn *sql.DB, from, to time.Time, setBy string) (int, error) {
	filled := 0
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if _, err := GetTrimelinePuzzleByDate(ctx, conn, day); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return filled, err
		}
		puzzle, err := drawTrimelinePuzzle(ctx, conn)
		if err != nil {
			return filled, err
		}
		puzzle.Date = day.Format(time.DateOnly)
		inserted, err := insertTrimelinePuzzle(ctx, conn, day, puzzle, setBy)
		if err != nil {
			return filled, err
		}
		// A concurrent fill won the race for this day: their puzzle stands,
		// and this day must not be counted as one we created.
		if inserted {
			filled++
		}
	}
	return filled, nil
}

// EnsureTodaysTrimelinePuzzle returns today's puzzle, drawing and recording
// one when editors left the day empty, so no day a reader could play is
// left without a puzzle.
func EnsureTodaysTrimelinePuzzle(ctx context.Context, conn *sql.DB) (models.TrimelinePuzzle, error) {
	today := trimeline.Today(trimelineNow())
	puzzle, err := GetTrimelinePuzzleByDate(ctx, conn, today)
	if !errors.Is(err, sql.ErrNoRows) {
		return puzzle, err
	}
	if _, err := GenerateTrimelinePuzzles(ctx, conn, today, today, ""); err != nil {
		return models.TrimelinePuzzle{}, err
	}
	return GetTrimelinePuzzleByDate(ctx, conn, today)
}

// drawTrimelinePuzzle samples candidates from the archive and builds one
// puzzle from them. The candidate pinning matches the public random-article
// endpoint: published, not archived, non-empty slug.
func drawTrimelinePuzzle(ctx context.Context, conn *sql.DB) (models.TrimelinePuzzle, error) {
	rows, err := conn.QueryContext(ctx, "SELECT `slug`, `title`, `pub_date` FROM `articles`"+
		" WHERE `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL"+
		" AND TRIM(COALESCE(`slug`, '')) <> ''"+
		" ORDER BY RAND() LIMIT ?", trimelineCandidateLimit)
	if err != nil {
		return models.TrimelinePuzzle{}, err
	}
	defer rows.Close()

	var candidates []trimeline.Headline
	for rows.Next() {
		var h trimeline.Headline
		if err := rows.Scan(&h.Slug, &h.Title, &h.PubDate); err != nil {
			return models.TrimelinePuzzle{}, err
		}
		candidates = append(candidates, h)
	}
	if err := rows.Err(); err != nil {
		return models.TrimelinePuzzle{}, err
	}

	picks, err := trimeline.BuildPuzzle(candidates)
	if err != nil {
		return models.TrimelinePuzzle{}, ErrTrimelineNoPuzzle
	}
	puzzle := models.TrimelinePuzzle{Source: models.TrimelineSourceGenerated}
	for _, pick := range picks {
		puzzle.Headlines = append(puzzle.Headlines, models.TrimelineHeadline{
			Title: pick.Title, Slug: pick.Slug, PubDate: pick.PubDate.Format(time.RFC3339),
		})
	}
	return puzzle, nil
}

// insertTrimelinePuzzle writes one day's puzzle, reporting whether this call
// created the row. A day someone else filled between the read and the write is
// a happy race: IGNORE keeps their puzzle, so the caller's day is simply
// already filled and insertTrimelinePuzzle reports false.
//
// RowsAffected is consulted for the ignored case even though the server
// connects with clientFoundRows=true, because that flag only inflates
// matched-row counts for UPDATE-style statements; INSERT IGNORE still reports
// 0 for a row the unique key silently discarded.
func insertTrimelinePuzzle(ctx context.Context, conn *sql.DB, day time.Time, puzzle models.TrimelinePuzzle, setBy string) (bool, error) {
	picks, err := json.Marshal(puzzle.Headlines)
	if err != nil {
		return false, err
	}
	res, err := conn.ExecContext(ctx, "INSERT IGNORE INTO "+trimelineTable+
		" (puzzle_date, picks, source, set_by) VALUES (?, ?, ?, ?)",
		day.Format(time.DateOnly), picks, puzzle.Source, setBy)
	if err != nil {
		return false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return inserted == 1, nil
}

func scanTrimelinePuzzle(row interface{ Scan(...any) error }) (models.TrimelinePuzzle, error) {
	var p models.TrimelinePuzzle
	var date time.Time
	var picks []byte
	if err := row.Scan(&date, &picks, &p.Source, &p.SetBy, &p.UpdatedAt); err != nil {
		return models.TrimelinePuzzle{}, err
	}
	p.Date = date.Format(time.DateOnly)
	p.Number = trimeline.PuzzleNumber(date)
	if err := json.Unmarshal(picks, &p.Headlines); err != nil {
		return models.TrimelinePuzzle{}, err
	}
	if len(p.Headlines) == 0 {
		return models.TrimelinePuzzle{}, errors.New("trimeline puzzle has no picks")
	}
	return p, nil
}
