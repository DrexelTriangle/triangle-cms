package database

import (
	"context"
	"database/sql"
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
