package database

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"server/internal/models"
	"server/internal/wordangle"
)

// The Wordangle queue. Every word that has ever been scheduled keeps a row, and
// the unique index on `word` is the seen-before test: a word that is queued,
// has already been a day's answer, or was retired after going live can never
// be inserted again, however the request got here.
//
// A row leaves the table only when it is replaced on a future day, because
// nobody has seen that word yet. Replacing today's word keeps the old row with
// puzzle_date cleared and retired_on set, since readers may have played it.

const wordangleTable = "wordangle_words"

const wordangleColumns = "word, puzzle_date, retired_on, source, COALESCE(set_by, ''), updated_at"

var (
	// ErrWordangleWordTaken means the word is already queued or on the
	// seen-before list.
	ErrWordangleWordTaken = errors.New("wordangle word already used")
	// ErrWordanglePoolExhausted means every generated-pool word has been used.
	ErrWordanglePoolExhausted = errors.New("wordangle word pool exhausted")
	// ErrWordanglePastDay means the day has already been played.
	ErrWordanglePastDay = errors.New("wordangle day is in the past")
)

// wordangleNow is the clock the write path reads; tests pin it.
var wordangleNow = time.Now

func EnsureWordangleTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema(wordangleTable))
	return err
}

// ListWordangleWords returns every row: queued, past and retired. The table
// grows by one row a day, so the caller splits it rather than paging.
func ListWordangleWords(ctx context.Context, conn *sql.DB) ([]models.WordangleWord, error) {
	rows, err := conn.QueryContext(ctx, "SELECT "+wordangleColumns+" FROM "+wordangleTable+
		" ORDER BY COALESCE(puzzle_date, retired_on), id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var words []models.WordangleWord
	for rows.Next() {
		word, err := scanWordangleWord(rows)
		if err != nil {
			return nil, err
		}
		words = append(words, word)
	}
	return words, rows.Err()
}

// GetWordangleWordByDate returns the word scheduled for date, or sql.ErrNoRows.
func GetWordangleWordByDate(ctx context.Context, conn *sql.DB, date time.Time) (models.WordangleWord, error) {
	row := conn.QueryRowContext(ctx, "SELECT "+wordangleColumns+" FROM "+wordangleTable+
		" WHERE puzzle_date = ?", date.Format(time.DateOnly))
	return scanWordangleWord(row)
}

// LookupWordangleWord returns the row holding word, or sql.ErrNoRows when the
// word has never been scheduled.
func LookupWordangleWord(ctx context.Context, conn *sql.DB, word string) (models.WordangleWord, error) {
	row := conn.QueryRowContext(ctx, "SELECT "+wordangleColumns+" FROM "+wordangleTable+
		" WHERE word = ?", word)
	return scanWordangleWord(row)
}

// SetWordangleWord makes word the answer for date, replacing whatever was
// there. It returns ErrWordangleWordTaken when word is already queued or seen,
// and ErrWordanglePastDay when date has already been played.
func SetWordangleWord(ctx context.Context, conn *sql.DB, date time.Time, word, source, setBy string) (models.WordangleWord, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return models.WordangleWord{}, err
	}
	defer tx.Rollback()

	day := date.Format(time.DateOnly)
	var existingID int64
	var existingWord string
	err = tx.QueryRowContext(ctx, "SELECT id, word FROM "+wordangleTable+
		" WHERE puzzle_date = ? FOR UPDATE", day).Scan(&existingID, &existingWord)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return models.WordangleWord{}, err
	}
	// Read the date only now, holding the day's lock: a request that waited
	// across midnight must see tomorrow's word as live and retire it, not
	// delete it and let it be scheduled again.
	today := wordangle.Today(wordangleNow())
	if date.Before(today) {
		return models.WordangleWord{}, ErrWordanglePastDay
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case existingWord == word:
		// Writing a day's own word back is a no-op, not a collision with itself.
		if err := tx.Commit(); err != nil {
			return models.WordangleWord{}, err
		}
		return GetWordangleWordByDate(ctx, conn, date)
	case date.Equal(today):
		if _, err := tx.ExecContext(ctx, "UPDATE "+wordangleTable+
			" SET puzzle_date = NULL, retired_on = ? WHERE id = ?", day, existingID); err != nil {
			return models.WordangleWord{}, err
		}
	default:
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+wordangleTable+" WHERE id = ?", existingID); err != nil {
			return models.WordangleWord{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO "+wordangleTable+
		" (word, puzzle_date, source, set_by) VALUES (?, ?, ?, ?)", word, day, source, setBy); err != nil {
		if isDuplicateWordKey(err) {
			return models.WordangleWord{}, ErrWordangleWordTaken
		}
		return models.WordangleWord{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.WordangleWord{}, err
	}
	return GetWordangleWordByDate(ctx, conn, date)
}

// RegenerateWordangleWord replaces date's word with a random unused word from
// the generated pool.
func RegenerateWordangleWord(ctx context.Context, conn *sql.DB, date time.Time, setBy string) (models.WordangleWord, error) {
	// Another editor can claim a candidate between reading the used set and
	// inserting; the unique index catches that, so just draw again.
	for attempt := 0; attempt < 5; attempt++ {
		candidates, err := unusedWordangleTargets(ctx, conn)
		if err != nil {
			return models.WordangleWord{}, err
		}
		if len(candidates) == 0 {
			return models.WordangleWord{}, ErrWordanglePoolExhausted
		}
		word := candidates[rand.IntN(len(candidates))]
		entry, err := SetWordangleWord(ctx, conn, date, word, models.WordangleSourceGenerated, setBy)
		if errors.Is(err, ErrWordangleWordTaken) {
			continue
		}
		return entry, err
	}
	return models.WordangleWord{}, ErrWordangleWordTaken
}

// GenerateWordangleWords fills every empty day from `from` through `to`
// (inclusive) with unused words from the generated pool, leaving days that
// already have a word alone. It returns how many days it filled; when the pool
// runs out part way, it returns the count so far with ErrWordanglePoolExhausted.
func GenerateWordangleWords(ctx context.Context, conn *sql.DB, from, to time.Time, setBy string) (int, error) {
	candidates, err := unusedWordangleTargets(ctx, conn)
	if err != nil {
		return 0, err
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	scheduled, err := wordangleScheduledDays(ctx, conn, from, to)
	if err != nil {
		return 0, err
	}

	filled := 0
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		// Skip filled days before drawing, so an exhausted pool is reported
		// only when there is actually an empty day it can't fill.
		if scheduled[day.Format(time.DateOnly)] {
			continue
		}
		for {
			if len(candidates) == 0 {
				return filled, ErrWordanglePoolExhausted
			}
			word := candidates[len(candidates)-1]
			candidates = candidates[:len(candidates)-1]

			// IGNORE swallows both unique keys: a day someone else just filled,
			// or a word someone else just queued. Telling them apart decides
			// whether to move on to the next day or the next word.
			res, err := conn.ExecContext(ctx, "INSERT IGNORE INTO "+wordangleTable+
				" (word, puzzle_date, source, set_by) VALUES (?, ?, ?, ?)",
				word, day.Format(time.DateOnly), models.WordangleSourceGenerated, setBy)
			if err != nil {
				return filled, err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				filled++
				break
			}
			if _, err := GetWordangleWordByDate(ctx, conn, day); err == nil {
				candidates = append(candidates, word) // the word is still free
				break
			} else if !errors.Is(err, sql.ErrNoRows) {
				return filled, err
			}
		}
	}
	return filled, nil
}

// EnsureTodaysWordangleWord returns today's word, generating and recording one
// when editors left the day empty. Recording it is the point: a word readers
// played has to be on the seen-before list, so the day's answer can't be an
// unrecorded pick made somewhere else.
func EnsureTodaysWordangleWord(ctx context.Context, conn *sql.DB) (models.WordangleWord, error) {
	today := wordangle.Today(wordangleNow())
	entry, err := GetWordangleWordByDate(ctx, conn, today)
	if !errors.Is(err, sql.ErrNoRows) {
		return entry, err
	}
	if _, err := GenerateWordangleWords(ctx, conn, today, today, ""); err != nil {
		return models.WordangleWord{}, err
	}
	return GetWordangleWordByDate(ctx, conn, today)
}

func wordangleScheduledDays(ctx context.Context, conn *sql.DB, from, to time.Time) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT puzzle_date FROM "+wordangleTable+
		" WHERE puzzle_date BETWEEN ? AND ?", from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]bool{}
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days[day.Format(time.DateOnly)] = true
	}
	return days, rows.Err()
}

// unusedWordangleTargets is the generated pool minus the seen-before set.
func unusedWordangleTargets(ctx context.Context, conn *sql.DB) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "SELECT word FROM "+wordangleTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	used := map[string]bool{}
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			return nil, err
		}
		used[word] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var unused []string
	for _, word := range wordangle.Targets() {
		if !used[word] {
			unused = append(unused, word)
		}
	}
	return unused, nil
}

func scanWordangleWord(row interface{ Scan(...any) error }) (models.WordangleWord, error) {
	var w models.WordangleWord
	var date, retired sql.NullTime
	if err := row.Scan(&w.Word, &date, &retired, &w.Source, &w.SetBy, &w.UpdatedAt); err != nil {
		return models.WordangleWord{}, err
	}
	if date.Valid {
		w.Date = date.Time.Format(time.DateOnly)
		w.Number = wordangle.PuzzleNumber(date.Time)
	}
	if retired.Valid {
		w.RetiredOn = retired.Time.Format(time.DateOnly)
		w.Number = wordangle.PuzzleNumber(retired.Time)
	}
	return w, nil
}

// isDuplicateWordKey reports a collision on the word index specifically. A
// collision on the date index (two editors filling the same empty day at once)
// is a different failure and must not read as "word already used".
func isDuplicateWordKey(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 &&
		strings.Contains(mysqlErr.Message, "uq_wordangle_words_word")
}
