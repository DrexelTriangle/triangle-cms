package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/triangle_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run QuizHTTP -v
func quizHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping quiz handler integration test")
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
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil || !acquired.Valid || acquired.Int64 != 1 {
		t.Fatalf("lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})
	for _, table := range []string{db.QuizAnswerCountsTableName, db.QuizzesTableName, "media"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsureQuizzesSchema(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureMediaTable(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func seedQuiz(t *testing.T, conn *sql.DB, slug, status string, coverID *int64) int64 {
	t.Helper()
	def := models.QuizDefinition{
		Questions: []models.QuizQuestion{{Text: "Q1", Answers: []models.QuizAnswer{{Text: "a", Correct: true}, {Text: "b"}}}},
		Results:   []models.QuizResult{{Min: intPtr(0), Max: intPtr(1), Title: "Band"}},
	}
	raw, _ := json.Marshal(def)
	id, err := db.CreateQuiz(context.Background(), conn, models.QuizInput{Slug: slug, Title: slug, Kind: models.QuizKindTrivia, Status: status, Definition: def, CoverMediaID: coverID}, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func intPtr(n int) *int { return &n }

func TestQuizHTTP_PublicReadBySlug(t *testing.T) {
	conn := quizHandlerTestDB(t)
	t.Setenv("MEDIA_BASE_URL", "https://media.test")
	res, err := conn.ExecContext(context.Background(), "INSERT INTO media (path, file_name, mime_type, size_bytes) VALUES ('wp-content/uploads/2026/10/c.jpg', 'c.jpg', 'image/jpeg', 10)")
	if err != nil {
		t.Fatal(err)
	}
	coverID, _ := res.LastInsertId()
	seedQuiz(t, conn, "pub", models.QuizStatusPublished, &coverID)
	seedQuiz(t, conn, "dr", models.QuizStatusDraft, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/quiz/pub", nil)
	req.SetPathValue("slug", "pub")
	rec := httptest.NewRecorder()
	GetQuizBySlug(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("want no-store, got %q", got)
	}
	var q models.Quiz
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if q.CoverURL == nil || *q.CoverURL != "https://media.test/wp-content/uploads/2026/10/c.jpg" {
		t.Fatalf("cover url not resolved: %+v", q.CoverURL)
	}
	if len(q.Counts) != 1 || len(q.Counts[0]) != 2 {
		t.Fatalf("counts shape: %v", q.Counts)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/quiz/dr", nil)
	req.SetPathValue("slug", "dr")
	rec = httptest.NewRecorder()
	GetQuizBySlug(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("draft must 404, got %d", rec.Code)
	}
}

func TestQuizHTTP_BatchReadDropsDraftsAndCaps(t *testing.T) {
	conn := quizHandlerTestDB(t)
	pub := seedQuiz(t, conn, "pub", models.QuizStatusPublished, nil)
	dr := seedQuiz(t, conn, "dr", models.QuizStatusDraft, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/quiz?ids="+itoa(pub)+","+itoa(dr)+",999", nil)
	rec := httptest.NewRecorder()
	GetQuizzesByIDs(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got map[string]models.Quiz
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[itoa(pub)].Slug != "pub" {
		t.Fatalf("want only the published quiz, got %v", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/quiz?ids=1,2,3,4,5,6,7,8,9,10,11", nil)
	rec = httptest.NewRecorder()
	GetQuizzesByIDs(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("11 ids must be 400, got %d", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestQuizHTTP_AnswerPostWithoutAuth(t *testing.T) {
	conn := quizHandlerTestDB(t)
	id := seedQuiz(t, conn, "pub", models.QuizStatusPublished, nil)
	handler := middleware.Chain(PostQuizAnswer(conn), middleware.RateLimitByIP(60, time.Minute))

	post := func(body, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/quiz/"+itoa(id)+"/answers", strings.NewReader(body))
		req.SetPathValue("id", itoa(id))
		req.RemoteAddr = "203.0.113.5:1"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"definition_version":1,"question_index":0,"answer_index":1}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 without auth, got %d: %s", rec.Code, rec.Body)
	}
	var counts struct {
		Counts []int64 `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil || len(counts.Counts) != 2 || counts.Counts[1] != 1 {
		t.Fatalf("want counts [0 1], got %s", rec.Body)
	}
	if rec := post(`{"definition_version":1,"question_index":0,"answer_index":9}`, "application/json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("out of range must 400, got %d", rec.Code)
	}
	if rec := post(`{"definition_version":2,"question_index":0,"answer_index":0}`, "application/json"); rec.Code != http.StatusConflict {
		t.Fatalf("stale version must 409, got %d", rec.Code)
	}
	if rec := post(`{"definition_version":1}`, "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type must 415, got %d", rec.Code)
	}

	dr := seedQuiz(t, conn, "dr", models.QuizStatusDraft, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/quiz/"+itoa(dr)+"/answers", strings.NewReader(`{"definition_version":1}`))
	req.SetPathValue("id", itoa(dr))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("draft must 404, got %d", rec.Code)
	}
}
