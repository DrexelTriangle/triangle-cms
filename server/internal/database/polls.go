package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Poll storage.
//
// The original schema was a single flat table (cms_poll_counts: option_name ->
// vote_count) plus a `poll_title` row in cms_settings. That models exactly one
// poll and keeps no history, so there was nowhere to record a past poll, its
// question, or the dates it ran. cms_polls / cms_poll_options replace it:
// polls are first-class rows, options hang off a poll, and closing a poll
// preserves its counts instead of overwriting them.
//
// cms_poll_counts is deliberately NOT dropped. MigrateLegacyPoll copies it into
// a real poll on first boot, and leaving the table in place means a rollback to
// the previous binary still finds its data.

const (
	PollsTableName       = "cms_polls"
	PollOptionsTableName = "cms_poll_options"
	// LegacyPollTableName is the pre-archive flat counts table, kept for rollback.
	LegacyPollTableName = "cms_poll_counts"
)

// Poll lifecycle. Only one poll may be Active at a time; PublishPoll enforces
// that by closing whichever poll currently holds the status.
const (
	PollStatusDraft  = "draft"
	PollStatusActive = "active"
	PollStatusClosed = "closed"
)

var ValidPollStatuses = map[string]bool{
	PollStatusDraft:  true,
	PollStatusActive: true,
	PollStatusClosed: true,
}

// ErrNoActivePoll is returned when a read or vote targets the active poll and
// there isn't one. Callers map this to 404 rather than 500 -- "no poll running"
// is a normal state, not a failure.
var ErrNoActivePoll = errors.New("no active poll")

// ErrPollNotOpen is returned when a vote arrives for a poll that is not
// accepting votes, either because of its status or because it is outside its
// start/end window.
var ErrPollNotOpen = errors.New("poll is not open for voting")

type PollOption struct {
	ID        int64
	Name      string
	VoteCount int64
	SortOrder int
}

type Poll struct {
	ID        int64
	Question  string
	Status    string
	StartsAt  *time.Time
	EndsAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	Options   []PollOption
}

// TotalVotes sums the poll's options. Percentages are computed from this rather
// than stored, so they can never drift out of sync with the counts.
func (p Poll) TotalVotes() int64 {
	var total int64
	for _, opt := range p.Options {
		total += opt.VoteCount
	}
	return total
}

func EnsurePollsSchema(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+PollsTableName+` (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			question VARCHAR(255) NOT NULL,
			status VARCHAR(16) NOT NULL DEFAULT 'draft',
			starts_at DATETIME NULL,
			ends_at DATETIME NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_cms_polls_status (status),
			INDEX idx_cms_polls_starts_at (starts_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		return err
	}

	// ON DELETE CASCADE: deleting a poll must take its options with it, or the
	// options table accumulates orphans that no query can reach.
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+PollOptionsTableName+` (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			poll_id BIGINT NOT NULL,
			option_name VARCHAR(128) NOT NULL,
			vote_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
			sort_order INT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY uq_cms_poll_options_poll_name (poll_id, option_name),
			INDEX idx_cms_poll_options_poll_sort (poll_id, sort_order, id),
			CONSTRAINT fk_cms_poll_options_poll
				FOREIGN KEY (poll_id) REFERENCES `+PollsTableName+` (id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		return err
	}

	return nil
}

// MigrateLegacyPoll folds the old flat cms_poll_counts table and the
// cms_settings `poll_title` row into a single active poll.
//
// It runs only when cms_polls is empty, so it is a no-op on every boot after
// the first and can never duplicate the poll. The legacy table is left in place.
func MigrateLegacyPoll(ctx context.Context, conn *sql.DB) error {
	var pollCount int64
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+PollsTableName).Scan(&pollCount); err != nil {
		return err
	}
	if pollCount > 0 {
		return nil
	}

	// The legacy table may not exist at all on a fresh database.
	var legacyExists int64
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = ?
	`, LegacyPollTableName).Scan(&legacyExists); err != nil {
		return err
	}
	if legacyExists == 0 {
		return nil
	}

	rows, err := conn.QueryContext(ctx,
		`SELECT option_name, vote_count FROM `+LegacyPollTableName+` ORDER BY option_name ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyOption struct {
		name  string
		votes int64
	}
	legacy := make([]legacyOption, 0)
	for rows.Next() {
		var opt legacyOption
		if err := rows.Scan(&opt.name, &opt.votes); err != nil {
			return err
		}
		legacy = append(legacy, opt)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil
	}

	question, err := GetPollTitle(ctx, conn)
	if err != nil {
		return err
	}
	if question == "" {
		question = "Poll"
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO `+PollsTableName+` (question, status, starts_at) VALUES (?, ?, NOW())`,
		question, PollStatusActive)
	if err != nil {
		return err
	}
	pollID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	for i, opt := range legacy {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+PollOptionsTableName+` (poll_id, option_name, vote_count, sort_order) VALUES (?, ?, ?, ?)`,
			pollID, opt.name, opt.votes, i); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListPolls returns polls newest-first with their options attached.
//
// When includeDrafts is false only polls an editor has published (active or
// closed) are returned -- the public archive must never leak an unpublished
// question.
func ListPolls(ctx context.Context, conn *sql.DB, includeDrafts bool, limit, offset int) ([]Poll, error) {
	query := `
		SELECT id, question, status, starts_at, ends_at, created_at, updated_at
		FROM ` + PollsTableName
	if !includeDrafts {
		query += ` WHERE status <> '` + PollStatusDraft + `'`
	}
	// COALESCE so a poll with no explicit start date still sorts sensibly
	// instead of sinking below every dated poll.
	query += ` ORDER BY COALESCE(starts_at, created_at) DESC, id DESC LIMIT ? OFFSET ?`

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := conn.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	polls := make([]Poll, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var p Poll
		if err := rows.Scan(&p.ID, &p.Question, &p.Status, &p.StartsAt, &p.EndsAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Options = make([]PollOption, 0)
		polls = append(polls, p)
		ids = append(ids, p.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(polls) == 0 {
		return polls, nil
	}

	if err := attachOptions(ctx, conn, polls, ids); err != nil {
		return nil, err
	}
	return polls, nil
}

// attachOptions loads every option for the given polls in one query. Doing it
// per-poll would issue N+1 queries against an archive page that renders dozens.
func attachOptions(ctx context.Context, conn *sql.DB, polls []Poll, ids []int64) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT poll_id, id, option_name, vote_count, sort_order
		FROM `+PollOptionsTableName+`
		WHERE poll_id IN (`+placeholders+`)
		ORDER BY poll_id ASC, sort_order ASC, id ASC
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	byID := make(map[int64]*Poll, len(polls))
	for i := range polls {
		byID[polls[i].ID] = &polls[i]
	}

	for rows.Next() {
		var pollID int64
		var opt PollOption
		if err := rows.Scan(&pollID, &opt.ID, &opt.Name, &opt.VoteCount, &opt.SortOrder); err != nil {
			return err
		}
		if poll, ok := byID[pollID]; ok {
			poll.Options = append(poll.Options, opt)
		}
	}
	return rows.Err()
}

func GetPollByID(ctx context.Context, conn *sql.DB, id int64) (*Poll, error) {
	var p Poll
	err := conn.QueryRowContext(ctx, `
		SELECT id, question, status, starts_at, ends_at, created_at, updated_at
		FROM `+PollsTableName+` WHERE id = ?
	`, id).Scan(&p.ID, &p.Question, &p.Status, &p.StartsAt, &p.EndsAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Options = make([]PollOption, 0)

	polls := []Poll{p}
	if err := attachOptions(ctx, conn, polls, []int64{p.ID}); err != nil {
		return nil, err
	}
	return &polls[0], nil
}

// GetActivePoll returns the single active poll, or ErrNoActivePoll.
//
// Ordering by id DESC is a safety net: the write paths keep at most one poll
// active, but if that invariant were ever broken the newest one wins instead of
// the result being nondeterministic.
func GetActivePoll(ctx context.Context, conn *sql.DB) (*Poll, error) {
	var id int64
	err := conn.QueryRowContext(ctx,
		`SELECT id FROM `+PollsTableName+` WHERE status = ? ORDER BY id DESC LIMIT 1`,
		PollStatusActive).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, ErrNoActivePoll
	}
	if err != nil {
		return nil, err
	}
	return GetPollByID(ctx, conn, id)
}

// AcceptsVotes reports whether the poll is open right now. Status is the primary
// gate; the date window is enforced on top of it so a poll left active past its
// end date stops accepting votes without needing a scheduled job.
func (p Poll) AcceptsVotes(now time.Time) bool {
	if p.Status != PollStatusActive {
		return false
	}
	if p.StartsAt != nil && now.Before(*p.StartsAt) {
		return false
	}
	if p.EndsAt != nil && now.After(*p.EndsAt) {
		return false
	}
	return true
}

func CreatePoll(ctx context.Context, conn *sql.DB, question, status string, startsAt, endsAt *time.Time, options []string) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if status == PollStatusActive {
		if err := closeActivePollsTx(ctx, tx); err != nil {
			return 0, err
		}
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO `+PollsTableName+` (question, status, starts_at, ends_at) VALUES (?, ?, ?, ?)`,
		question, status, startsAt, endsAt)
	if err != nil {
		return 0, err
	}
	pollID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for i, name := range options {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+PollOptionsTableName+` (poll_id, option_name, sort_order) VALUES (?, ?, ?)`,
			pollID, name, i); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return pollID, nil
}

// UpdatePoll applies whichever fields were supplied. Nil pointers mean "leave
// alone", which is what lets the same call rename a poll without clearing its
// dates. clearStarts/clearEnds are how a caller explicitly sets a date back to
// NULL, since a nil time already means "unchanged".
func UpdatePoll(ctx context.Context, conn *sql.DB, id int64, question *string, status *string, startsAt, endsAt *time.Time, clearStarts, clearEnds bool) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if status != nil && *status == PollStatusActive {
		if _, err := tx.ExecContext(ctx,
			`UPDATE `+PollsTableName+` SET status = ? WHERE status = ? AND id <> ?`,
			PollStatusClosed, PollStatusActive, id); err != nil {
			return err
		}
	}

	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if question != nil {
		sets = append(sets, "question = ?")
		args = append(args, *question)
	}
	if status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *status)
	}
	if clearStarts {
		sets = append(sets, "starts_at = NULL")
	} else if startsAt != nil {
		sets = append(sets, "starts_at = ?")
		args = append(args, *startsAt)
	}
	if clearEnds {
		sets = append(sets, "ends_at = NULL")
	} else if endsAt != nil {
		sets = append(sets, "ends_at = ?")
		args = append(args, *endsAt)
	}

	if len(sets) > 0 {
		args = append(args, id)
		res, err := tx.ExecContext(ctx,
			`UPDATE `+PollsTableName+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		if err != nil {
			return err
		}
		// RowsAffected is 0 both when the id is unknown and when the update was
		// a no-op, so confirm existence separately rather than guessing.
		if affected, err := res.RowsAffected(); err == nil && affected == 0 {
			var exists int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+PollsTableName+` WHERE id = ?`, id).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return sql.ErrNoRows
			}
		}
	}

	return tx.Commit()
}

func closeActivePollsTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE `+PollsTableName+` SET status = ? WHERE status = ?`,
		PollStatusClosed, PollStatusActive)
	return err
}

func DeletePoll(ctx context.Context, conn *sql.DB, id int64) error {
	res, err := conn.ExecContext(ctx, `DELETE FROM `+PollsTableName+` WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func AddPollOption(ctx context.Context, conn *sql.DB, pollID int64, name string) (int64, error) {
	// New options land at the end; COALESCE covers the first option, where MAX
	// over zero rows is NULL rather than 0.
	var nextSort int
	if err := conn.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM `+PollOptionsTableName+` WHERE poll_id = ?`,
		pollID).Scan(&nextSort); err != nil {
		return 0, err
	}

	res, err := conn.ExecContext(ctx,
		`INSERT INTO `+PollOptionsTableName+` (poll_id, option_name, sort_order) VALUES (?, ?, ?)`,
		pollID, name, nextSort)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func RenamePollOption(ctx context.Context, conn *sql.DB, pollID, optionID int64, name string) error {
	res, err := conn.ExecContext(ctx,
		`UPDATE `+PollOptionsTableName+` SET option_name = ? WHERE id = ? AND poll_id = ?`,
		name, optionID, pollID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var exists int64
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+PollOptionsTableName+` WHERE id = ? AND poll_id = ?`,
			optionID, pollID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return sql.ErrNoRows
		}
	}
	return nil
}

func DeletePollOption(ctx context.Context, conn *sql.DB, pollID, optionID int64) error {
	res, err := conn.ExecContext(ctx,
		`DELETE FROM `+PollOptionsTableName+` WHERE id = ? AND poll_id = ?`, optionID, pollID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// VoteOnActivePoll increments an option on the currently active poll.
//
// The vote is applied with a single conditional UPDATE joined against the poll
// row, so the status and date-window checks happen atomically with the
// increment. Checking first and updating after would let a vote land on a poll
// closed in between.
func VoteOnActivePoll(ctx context.Context, conn *sql.DB, option string) (*Poll, error) {
	poll, err := GetActivePoll(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !poll.AcceptsVotes(time.Now()) {
		return nil, ErrPollNotOpen
	}

	res, err := conn.ExecContext(ctx, `
		UPDATE `+PollOptionsTableName+` o
		JOIN `+PollsTableName+` p ON p.id = o.poll_id
		SET o.vote_count = o.vote_count + 1
		WHERE o.poll_id = ?
		  AND o.option_name = ?
		  AND p.status = ?
		  AND (p.starts_at IS NULL OR p.starts_at <= NOW())
		  AND (p.ends_at IS NULL OR p.ends_at >= NOW())
	`, poll.ID, option, PollStatusActive)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}

	return GetPollByID(ctx, conn, poll.ID)
}
