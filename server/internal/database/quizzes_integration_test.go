package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/triangle_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run Quiz -v
func quizTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping quiz integration test")
	}
	if !strings.Contains(dsn, "clientFoundRows") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "clientFoundRows=true"
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the quiz test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})
	for _, table := range []string{QuizAnswerCountsTableName, QuizzesTableName, "articles"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := EnsureQuizzesSchema(ctx, conn); err != nil {
		t.Fatalf("ensure quiz schema: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE articles (id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, slug VARCHAR(255) NOT NULL, `text` LONGTEXT, pub_date DATETIME NULL, scheduled_pub_date DATETIME NULL, archived_at DATETIME NULL)"); err != nil {
		t.Fatalf("create articles: %v", err)
	}
	return conn
}

func triviaInput(title string) (models.QuizInput, string) {
	def := trivia(2)
	raw, _ := json.Marshal(def)
	return models.QuizInput{Slug: strings.ToLower(title), Title: title, Kind: models.QuizKindTrivia, Status: models.QuizStatusPublished, Definition: def, EditVersion: 1}, string(raw)
}

func TestQuizCreateGetAndCounts(t *testing.T) {
	conn := quizTestDB(t)
	ctx := context.Background()
	in, raw := triviaInput("Alpha")
	id, err := CreateQuiz(ctx, conn, in, raw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	q, err := GetPublishedQuizBySlug(ctx, conn, "alpha")
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if q.ID != id || q.DefinitionVersion != 1 || q.EditVersion != 1 {
		t.Fatalf("unexpected quiz %+v", q)
	}
	if len(q.Counts) != 2 || len(q.Counts[0]) != 2 || q.Counts[0][0] != 0 {
		t.Fatalf("counts must be zero-filled per answer, got %v", q.Counts)
	}
	counts, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 1, QuestionIndex: 0, AnswerIndex: 1})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if counts[1] != 1 {
		t.Fatalf("want answer 1 counted once, got %v", counts)
	}
	if _, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 1, QuestionIndex: 0, AnswerIndex: 5}); !errors.Is(err, ErrQuizIndexOutOfRange) {
		t.Fatalf("want ErrQuizIndexOutOfRange, got %v", err)
	}
	if _, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 2, QuestionIndex: 0, AnswerIndex: 0}); !errors.Is(err, ErrQuizVersionMismatch) {
		t.Fatalf("want ErrQuizVersionMismatch, got %v", err)
	}
}

func TestQuizDraftIsNotPublic(t *testing.T) {
	conn := quizTestDB(t)
	ctx := context.Background()
	in, raw := triviaInput("Beta")
	in.Status = models.QuizStatusDraft
	id, _ := CreateQuiz(ctx, conn, in, raw)
	if _, err := GetPublishedQuizBySlug(ctx, conn, "beta"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("draft must 404 by slug, got %v", err)
	}
	if _, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 1}); !errors.Is(err, ErrQuizNotPublished) {
		t.Fatalf("draft must refuse answers, got %v", err)
	}
	pub, rawPub := triviaInput("Gamma")
	pubID, _ := CreateQuiz(ctx, conn, pub, rawPub)
	got, err := GetPublishedQuizzesByIDs(ctx, conn, []int64{id, pubID, 999})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(got) != 1 || got[pubID] == nil {
		t.Fatalf("batch must return only the published quiz, got %v", got)
	}
}

func TestQuizUpdateResetRules(t *testing.T) {
	conn := quizTestDB(t)
	ctx := context.Background()
	in, raw := triviaInput("Delta")
	id, _ := CreateQuiz(ctx, conn, in, raw)
	if _, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 1}); err != nil {
		t.Fatal(err)
	}

	// Text-only edit keeps counts and version.
	in.Definition.Questions[0].Text = "Reworded"
	textRaw, _ := json.Marshal(in.Definition)
	if err := UpdateQuiz(ctx, conn, id, in, string(textRaw), false); err != nil {
		t.Fatalf("text edit must save: %v", err)
	}
	q, _ := GetQuizByID(ctx, conn, id)
	if q.DefinitionVersion != 1 || q.Counts[0][0] != 1 || q.EditVersion != 2 {
		t.Fatalf("text edit changed version or counts: %+v", q)
	}

	// Stale edit_version.
	if err := UpdateQuiz(ctx, conn, id, in, string(textRaw), false); !errors.Is(err, ErrQuizStale) {
		t.Fatalf("want ErrQuizStale, got %v", err)
	}
	in.EditVersion = 2

	// Structural edit without the flag.
	in.Definition = trivia(3)
	structRaw, _ := json.Marshal(in.Definition)
	if err := UpdateQuiz(ctx, conn, id, in, string(structRaw), false); !errors.Is(err, ErrQuizResetRequired) {
		t.Fatalf("want ErrQuizResetRequired, got %v", err)
	}
	// With the flag: version bumps, old rows gone.
	if err := UpdateQuiz(ctx, conn, id, in, string(structRaw), true); err != nil {
		t.Fatalf("reset save: %v", err)
	}
	q, _ = GetQuizByID(ctx, conn, id)
	if q.DefinitionVersion != 2 || q.Counts[0][0] != 0 {
		t.Fatalf("reset did not bump version or clear counts: %+v", q)
	}
	var old int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = 1", id).Scan(&old); err != nil || old != 0 {
		t.Fatalf("old version rows must be deleted, count=%d err=%v", old, err)
	}
}

func TestQuizResetRacesAnswerWrites(t *testing.T) {
	conn := quizTestDB(t)
	ctx := context.Background()
	in, raw := triviaInput("Race")
	id, _ := CreateQuiz(ctx, conn, in, raw)
	if _, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: 1}); err != nil {
		t.Fatal(err)
	}
	in.Definition = trivia(3)
	structRaw, _ := json.Marshal(in.Definition)

	// Half the readers post on the old version and half on the new one, so
	// both orders of lock acquisition are exercised. Every post must either be
	// accepted or fail with a version mismatch (the handler's 409), and every
	// accepted post on the new version must be counted on the new version.
	var wg sync.WaitGroup
	var mu sync.Mutex
	acceptedOnNew := 0
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		version := 1 + i%2
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := RecordQuizAnswer(ctx, conn, id, models.QuizAnswerInput{DefinitionVersion: version, QuestionIndex: 1, AnswerIndex: 0})
			switch {
			case err == nil && version == 2:
				mu.Lock()
				acceptedOnNew++
				mu.Unlock()
			case err != nil && !errors.Is(err, ErrQuizVersionMismatch):
				t.Errorf("answer on version %d: want success or ErrQuizVersionMismatch, got %v", version, err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := UpdateQuiz(ctx, conn, id, in, string(structRaw), true); err != nil {
			t.Errorf("reset: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	var old int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = 1", id).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if old != 0 {
		t.Fatalf("%d rows for the old version survived the reset", old)
	}
	var onNew int64
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(SUM(answer_count), 0) FROM "+QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = 2", id).Scan(&onNew); err != nil {
		t.Fatal(err)
	}
	if onNew != int64(acceptedOnNew) {
		t.Fatalf("%d answers were accepted on the new version but %d are counted there", acceptedOnNew, onNew)
	}
}

func TestQuizReferencesAndSlug(t *testing.T) {
	conn := quizTestDB(t)
	ctx := context.Background()
	in, raw := triviaInput("Refs")
	id, _ := CreateQuiz(ctx, conn, in, raw)
	for _, row := range []string{
		"('live', '<p>[quiz id=\"1\"]</p>', UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL, NULL)",
		"('sched', '<p>[quiz id=\"1\"]</p>', NULL, UTC_TIMESTAMP() + INTERVAL 1 DAY, NULL)",
		"('draft', '<p>[quiz id=\"1\"]</p>', NULL, NULL, NULL)",
		"('trashed', '<p>[quiz id=\"1\"]</p>', UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL, UTC_TIMESTAMP())",
		"('other', '<p>[quiz id=\"12\"]</p>', UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL, NULL)",
	} {
		if _, err := conn.ExecContext(ctx, "INSERT INTO articles (slug, `text`, pub_date, scheduled_pub_date, archived_at) VALUES "+row); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := CountQuizReferences(ctx, conn, id)
	if err != nil {
		t.Fatal(err)
	}
	if refs.Published != 1 || refs.Scheduled != 1 || refs.Draft != 1 {
		t.Fatalf("want 1/1/1, got %+v", refs)
	}

	slug, err := UniqueQuizSlug(ctx, conn, "refs")
	if err != nil || slug != "refs-2" {
		t.Fatalf("want refs-2, got %q err=%v", slug, err)
	}
	if err := DeleteQuiz(ctx, conn, id, 99); !errors.Is(err, ErrQuizStale) {
		t.Fatalf("delete with stale version must fail, got %v", err)
	}
	if err := DeleteQuiz(ctx, conn, id, 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
