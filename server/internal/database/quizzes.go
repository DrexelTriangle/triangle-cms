package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"server/internal/models"
)

const (
	QuizzesTableName          = "cms_quizzes"
	QuizAnswerCountsTableName = "cms_quiz_answer_counts"
)

// EnsureQuizzesSchema creates both quiz tables from their canonical files.
// Counts reference quizzes with ON DELETE CASCADE, so the order matters.
func EnsureQuizzesSchema(ctx context.Context, conn *sql.DB) error {
	for _, table := range []string{QuizzesTableName, QuizAnswerCountsTableName} {
		if _, err := conn.ExecContext(ctx, TableSchema(table)); err != nil {
			return err
		}
	}
	return nil
}

var (
	ErrQuizStale           = errors.New("quiz: stale edit_version")
	ErrQuizResetRequired   = errors.New("quiz: questions changed, reset required")
	ErrQuizSlugTaken       = errors.New("quiz: slug taken")
	ErrQuizVersionMismatch = errors.New("quiz: definition_version mismatch")
	ErrQuizNotPublished    = errors.New("quiz: not published")
	ErrQuizIndexOutOfRange = errors.New("quiz: index out of range")
)

const quizColumns = "id, slug, title, kind, status, intro, cover_media_id, definition, definition_version, edit_version, created_at, updated_at"

func scanQuiz(row interface{ Scan(...any) error }) (*models.Quiz, error) {
	var q models.Quiz
	var cover sql.NullInt64
	var def string
	var created, updated time.Time
	if err := row.Scan(&q.ID, &q.Slug, &q.Title, &q.Kind, &q.Status, &q.Intro, &cover, &def, &q.DefinitionVersion, &q.EditVersion, &created, &updated); err != nil {
		return nil, err
	}
	if cover.Valid {
		q.CoverMediaID = &cover.Int64
	}
	if err := json.Unmarshal([]byte(def), &q.Definition); err != nil {
		return nil, fmt.Errorf("quiz %d: bad definition: %w", q.ID, err)
	}
	q.CreatedAt = created.UTC().Format(time.RFC3339)
	q.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return &q, nil
}

// attachCounts fills Counts with one zero-filled slice per question, then
// overlays the stored rows for the current version. Zero-filling means the
// site can index counts[q][a] without checking for missing rows.
func attachCounts(ctx context.Context, conn *sql.DB, q *models.Quiz) error {
	q.Counts = make([][]int64, len(q.Definition.Questions))
	for i, question := range q.Definition.Questions {
		q.Counts[i] = make([]int64, len(question.Answers))
	}
	rows, err := conn.QueryContext(ctx, "SELECT question_index, answer_index, answer_count FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = ?", q.ID, q.DefinitionVersion)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var qi, ai int
		var n int64
		if err := rows.Scan(&qi, &ai, &n); err != nil {
			return err
		}
		if qi < len(q.Counts) && ai < len(q.Counts[qi]) {
			q.Counts[qi][ai] = n
		}
	}
	return rows.Err()
}

func CreateQuiz(ctx context.Context, conn *sql.DB, in models.QuizInput, def string) (int64, error) {
	res, err := conn.ExecContext(ctx, "INSERT INTO "+QuizzesTableName+" (slug, title, kind, status, intro, cover_media_id, definition) VALUES (?, ?, ?, ?, ?, ?, ?)",
		in.Slug, in.Title, in.Kind, in.Status, in.Intro, in.CoverMediaID, def)
	if err != nil {
		if strings.Contains(err.Error(), "uq_cms_quizzes_slug") {
			return 0, ErrQuizSlugTaken
		}
		return 0, err
	}
	return res.LastInsertId()
}

func GetQuizByID(ctx context.Context, conn *sql.DB, id int64) (*models.Quiz, error) {
	q, err := scanQuiz(conn.QueryRowContext(ctx, "SELECT "+quizColumns+" FROM "+QuizzesTableName+" WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	return q, attachCounts(ctx, conn, q)
}

func GetPublishedQuizBySlug(ctx context.Context, conn *sql.DB, slug string) (*models.Quiz, error) {
	q, err := scanQuiz(conn.QueryRowContext(ctx, "SELECT "+quizColumns+" FROM "+QuizzesTableName+" WHERE slug = ? AND status = ?", slug, models.QuizStatusPublished))
	if err != nil {
		return nil, err
	}
	return q, attachCounts(ctx, conn, q)
}

func GetPublishedQuizzesByIDs(ctx context.Context, conn *sql.DB, ids []int64) (map[int64]*models.Quiz, error) {
	out := map[int64]*models.Quiz{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, models.QuizStatusPublished)
	rows, err := conn.QueryContext(ctx, "SELECT "+quizColumns+" FROM "+QuizzesTableName+" WHERE id IN (?"+strings.Repeat(",?", len(ids)-1)+") AND status = ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		q, err := scanQuiz(rows)
		if err != nil {
			return nil, err
		}
		out[q.ID] = q
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, q := range out {
		if err := attachCounts(ctx, conn, q); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ListQuizzes(ctx context.Context, conn *sql.DB, status string) ([]models.QuizListItem, error) {
	query := "SELECT q.id, q.slug, q.title, q.kind, q.status, q.updated_at, q.edit_version, " +
		"COALESCE((SELECT SUM(c.answer_count) FROM " + QuizAnswerCountsTableName + " c WHERE c.quiz_id = q.id AND c.definition_version = q.definition_version AND c.question_index = 0), 0) " +
		"FROM " + QuizzesTableName + " q"
	var args []any
	if status != "" {
		query += " WHERE q.status = ?"
		args = append(args, status)
	}
	query += " ORDER BY q.updated_at DESC, q.id DESC"
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.QuizListItem{}
	for rows.Next() {
		var it models.QuizListItem
		var updated time.Time
		if err := rows.Scan(&it.ID, &it.Slug, &it.Title, &it.Kind, &it.Status, &updated, &it.EditVersion, &it.Starts); err != nil {
			return nil, err
		}
		it.UpdatedAt = updated.UTC().Format(time.RFC3339)
		items = append(items, it)
	}
	return items, rows.Err()
}

// UpdateQuiz saves the whole quiz under the row lock. A structural change (see
// QuizFingerprint) or a kind change on a quiz with counts needs resetCounts, in
// which case the version bumps and the old version's rows are deleted in the
// same transaction. in.EditVersion must equal the stored one.
func UpdateQuiz(ctx context.Context, conn *sql.DB, id int64, in models.QuizInput, def string, resetCounts bool) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var stored models.Quiz
	var storedDef string
	if err := tx.QueryRowContext(ctx, "SELECT kind, definition, definition_version, edit_version FROM "+QuizzesTableName+" WHERE id = ? FOR UPDATE", id).
		Scan(&stored.Kind, &storedDef, &stored.DefinitionVersion, &stored.EditVersion); err != nil {
		return err
	}
	if stored.EditVersion != in.EditVersion {
		return ErrQuizStale
	}
	if err := json.Unmarshal([]byte(storedDef), &stored.Definition); err != nil {
		return err
	}
	structural := stored.Kind != in.Kind || QuizFingerprint(stored.Kind, stored.Definition) != QuizFingerprint(in.Kind, in.Definition)
	version := stored.DefinitionVersion
	if structural {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = ?", id, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 && !resetCounts {
			return ErrQuizResetRequired
		}
		version++
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ?", id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE "+QuizzesTableName+" SET slug = ?, title = ?, kind = ?, status = ?, intro = ?, cover_media_id = ?, definition = ?, definition_version = ?, edit_version = edit_version + 1 WHERE id = ?",
		in.Slug, in.Title, in.Kind, in.Status, in.Intro, in.CoverMediaID, def, version, id); err != nil {
		if strings.Contains(err.Error(), "uq_cms_quizzes_slug") {
			return ErrQuizSlugTaken
		}
		return err
	}
	return tx.Commit()
}

func DeleteQuiz(ctx context.Context, conn *sql.DB, id int64, editVersion int) error {
	res, err := conn.ExecContext(ctx, "DELETE FROM "+QuizzesTableName+" WHERE id = ? AND edit_version = ?", id, editVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT 1 FROM "+QuizzesTableName+" WHERE id = ?", id).Scan(&exists); err == nil {
			return ErrQuizStale
		}
		return sql.ErrNoRows
	}
	return nil
}

// CountQuizReferences scans article bodies for the shortcode. A LIKE over
// articles.text is a full scan, which is fine at this corpus size and runs
// only from a confirm dialog.
func CountQuizReferences(ctx context.Context, conn *sql.DB, id int64) (models.QuizReferencesResponse, error) {
	var out models.QuizReferencesResponse
	pattern := fmt.Sprintf(`%%[quiz id="%d"]%%`, id)
	err := conn.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(pub_date IS NOT NULL AND pub_date <= UTC_TIMESTAMP()), 0),
		  COALESCE(SUM(scheduled_pub_date IS NOT NULL AND (pub_date IS NULL OR pub_date > UTC_TIMESTAMP())), 0),
		  COALESCE(SUM(pub_date IS NULL AND scheduled_pub_date IS NULL), 0)
		FROM articles WHERE archived_at IS NULL AND `+"`text`"+` LIKE ?`, pattern).
		Scan(&out.Published, &out.Scheduled, &out.Draft)
	return out, err
}

// RecordQuizAnswer increments one count under the quiz row lock, so a reset
// running at the same time either sees this row and deletes it or runs first
// and makes this call fail the version check.
func RecordQuizAnswer(ctx context.Context, conn *sql.DB, id int64, in models.QuizAnswerInput) ([]int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status, def string
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT status, definition, definition_version FROM "+QuizzesTableName+" WHERE id = ? FOR UPDATE", id).Scan(&status, &def, &version); err != nil {
		return nil, err
	}
	if status != models.QuizStatusPublished {
		return nil, ErrQuizNotPublished
	}
	if version != in.DefinitionVersion {
		return nil, ErrQuizVersionMismatch
	}
	var d models.QuizDefinition
	if err := json.Unmarshal([]byte(def), &d); err != nil {
		return nil, err
	}
	if in.QuestionIndex < 0 || in.QuestionIndex >= len(d.Questions) || in.AnswerIndex < 0 || in.AnswerIndex >= len(d.Questions[in.QuestionIndex].Answers) {
		return nil, ErrQuizIndexOutOfRange
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+QuizAnswerCountsTableName+" (quiz_id, definition_version, question_index, answer_index, answer_count) VALUES (?, ?, ?, ?, 1) ON DUPLICATE KEY UPDATE answer_count = answer_count + 1",
		id, version, in.QuestionIndex, in.AnswerIndex); err != nil {
		return nil, err
	}
	counts := make([]int64, len(d.Questions[in.QuestionIndex].Answers))
	rows, err := tx.QueryContext(ctx, "SELECT answer_index, answer_count FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = ? AND question_index = ?", id, version, in.QuestionIndex)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ai int
		var n int64
		if err := rows.Scan(&ai, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if ai < len(counts) {
			counts[ai] = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, tx.Commit()
}

// UniqueQuizSlug returns base, or base-2, base-3 and so on, whichever is free.
func UniqueQuizSlug(ctx context.Context, conn *sql.DB, base string) (string, error) {
	candidate := base
	for n := 2; ; n++ {
		var exists int
		err := conn.QueryRowContext(ctx, "SELECT 1 FROM "+QuizzesTableName+" WHERE slug = ?", candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}
