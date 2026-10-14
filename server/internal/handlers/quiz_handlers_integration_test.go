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

func editorBody(t *testing.T, in models.QuizInput) string {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func validTriviaInput(slug string) models.QuizInput {
	return models.QuizInput{
		Slug: slug, Title: "T " + slug, Kind: models.QuizKindTrivia, Status: models.QuizStatusDraft, EditVersion: 1,
		Definition: models.QuizDefinition{
			Questions: []models.QuizQuestion{{Text: "Q1", Answers: []models.QuizAnswer{{Text: "a", Correct: true}, {Text: "b"}}}},
			Results:   []models.QuizResult{{Min: intPtr(0), Max: intPtr(1), Title: "Band"}},
		},
	}
}

func TestQuizHTTP_EditorCreateValidatesAndSuffixesSlug(t *testing.T) {
	conn := quizHandlerTestDB(t)
	seedQuiz(t, conn, "taken", models.QuizStatusDraft, nil)

	in := validTriviaInput("taken")
	req := httptest.NewRequest(http.MethodPost, "/v1/quizzes", strings.NewReader(editorBody(t, in)))
	rec := httptest.NewRecorder()
	PostQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created models.Quiz
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Slug != "taken-2" {
		t.Fatalf("want auto-suffixed slug taken-2, got %q", created.Slug)
	}

	bad := validTriviaInput("bad")
	bad.Definition.Questions[0].Answers[0].Correct = false
	bad.Status = models.QuizStatusPublished
	req = httptest.NewRequest(http.MethodPost, "/v1/quizzes", strings.NewReader(editorBody(t, bad)))
	rec = httptest.NewRecorder()
	PostQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publishing without a correct answer must 422, got %d: %s", rec.Code, rec.Body)
	}
	var v models.QuizValidationResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Errors) == 0 || !strings.Contains(v.Errors[0], "Question 1") {
		t.Fatalf("want an error naming question 1, got %+v", v)
	}

	bad.Status = models.QuizStatusDraft
	bad.CoverMediaID = new(int64)
	*bad.CoverMediaID = 404
	req = httptest.NewRequest(http.MethodPost, "/v1/quizzes", strings.NewReader(editorBody(t, bad)))
	rec = httptest.NewRecorder()
	PostQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Cover image: media item not found") {
		t.Fatalf("unknown media id must 422 naming the cover, got %d: %s", rec.Code, rec.Body)
	}
}

// Strict rules apply once a quiz is published, even when the request itself
// says draft (spec 4.3).
func TestQuizHTTP_EditorSaveOfPublishedQuizIsStrict(t *testing.T) {
	conn := quizHandlerTestDB(t)
	id := seedQuiz(t, conn, "live", models.QuizStatusPublished, nil)
	in := validTriviaInput("live")
	in.Definition.Questions[0].Answers[0].Correct = false
	req := httptest.NewRequest(http.MethodPatch, "/v1/quizzes/"+itoa(id), strings.NewReader(editorBody(t, in)))
	req.SetPathValue("id", itoa(id))
	rec := httptest.NewRecorder()
	PatchQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "exactly one correct answer") {
		t.Fatalf("a draft-status save of a published quiz must run strict rules, got %d: %s", rec.Code, rec.Body)
	}
}

func TestQuizHTTP_EditorPatchConflicts(t *testing.T) {
	conn := quizHandlerTestDB(t)
	id := seedQuiz(t, conn, "p", models.QuizStatusPublished, nil)
	if _, err := db.RecordQuizAnswer(context.Background(), conn, id, models.QuizAnswerInput{DefinitionVersion: 1}); err != nil {
		t.Fatal(err)
	}
	patch := func(query string, in models.QuizInput) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/quizzes/"+itoa(id)+query, strings.NewReader(editorBody(t, in)))
		req.SetPathValue("id", itoa(id))
		rec := httptest.NewRecorder()
		PatchQuiz(conn).ServeHTTP(rec, req)
		return rec
	}
	in := validTriviaInput("p")
	in.Status = models.QuizStatusPublished
	in.Definition.Questions = append(in.Definition.Questions, in.Definition.Questions[0])
	in.Definition.Results[0].Max = intPtr(2)

	rec := patch("", in)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "reset_required") {
		t.Fatalf("want 409 reset_required, got %d: %s", rec.Code, rec.Body)
	}
	rec = patch("?reset_counts=true", in)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 with reset, got %d: %s", rec.Code, rec.Body)
	}
	var saved models.Quiz
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || saved.DefinitionVersion != 2 {
		t.Fatalf("reset must bump definition_version to 2, got %+v err=%v", saved.DefinitionVersion, err)
	}
	var oldRows int
	if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+db.QuizAnswerCountsTableName+" WHERE quiz_id = ? AND definition_version = 1", id).Scan(&oldRows); err != nil || oldRows != 0 {
		t.Fatalf("reset must leave no rows for the old version, count=%d err=%v", oldRows, err)
	}
	rec = patch("?reset_counts=true", in)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stale") {
		t.Fatalf("replaying edit_version 1 must 409 stale, got %d: %s", rec.Code, rec.Body)
	}

	in.EditVersion = 2
	in.Slug = "renamed"
	rec = patch("", in)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "slug") {
		t.Fatalf("slug change on a published quiz must 422, got %d: %s", rec.Code, rec.Body)
	}
}

func TestQuizHTTP_ListFilterAndReferences(t *testing.T) {
	conn := quizHandlerTestDB(t)
	if _, err := conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "CREATE TABLE articles (id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, slug VARCHAR(255), `text` LONGTEXT, pub_date DATETIME NULL, scheduled_pub_date DATETIME NULL, archived_at DATETIME NULL)"); err != nil {
		t.Fatal(err)
	}
	pub := seedQuiz(t, conn, "pub", models.QuizStatusPublished, nil)
	seedQuiz(t, conn, "dr", models.QuizStatusDraft, nil)
	block := "'<p>[quiz id=\"" + itoa(pub) + "\"]</p>'"
	for _, row := range []string{
		"('live', " + block + ", UTC_TIMESTAMP() - INTERVAL 1 DAY, NULL)",
		"('sched', " + block + ", NULL, UTC_TIMESTAMP() + INTERVAL 1 DAY)",
		"('draft', " + block + ", NULL, NULL)",
	} {
		if _, err := conn.ExecContext(context.Background(), "INSERT INTO articles (slug, `text`, pub_date, scheduled_pub_date) VALUES "+row); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/quizzes?status=published", nil)
	rec := httptest.NewRecorder()
	GetQuizzes(conn).ServeHTTP(rec, req)
	var list []models.QuizListItem
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list) != 1 || list[0].Slug != "pub" {
		t.Fatalf("want only the published quiz, got %d %s", rec.Code, rec.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/quizzes/"+itoa(pub)+"/references", nil)
	req.SetPathValue("id", itoa(pub))
	rec = httptest.NewRecorder()
	GetQuizReferences(conn).ServeHTTP(rec, req)
	var refs models.QuizReferencesResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &refs)
	if rec.Code != http.StatusOK || refs.Published != 1 || refs.Scheduled != 1 || refs.Draft != 1 {
		t.Fatalf("want one reference per status, got %d %s", rec.Code, rec.Body)
	}
}

func TestQuizHTTP_EditorRejectsSlugOver180(t *testing.T) {
	conn := quizHandlerTestDB(t)
	in := validTriviaInput(strings.Repeat("a", 181))
	req := httptest.NewRequest(http.MethodPost, "/v1/quizzes", strings.NewReader(editorBody(t, in)))
	rec := httptest.NewRecorder()
	PostQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "at most 180") {
		t.Fatalf("181-char slug must 422, got %d: %s", rec.Code, rec.Body)
	}
	in = validTriviaInput(strings.Repeat("a", 180))
	req = httptest.NewRequest(http.MethodPost, "/v1/quizzes", strings.NewReader(editorBody(t, in)))
	rec = httptest.NewRecorder()
	PostQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("180-char slug must pass, got %d: %s", rec.Code, rec.Body)
	}
}

// The request publishes a draft: strict rules run even though the stored
// quiz is a draft (second half of the strict rule).
func TestQuizHTTP_EditorPublishingDraftIsStrict(t *testing.T) {
	conn := quizHandlerTestDB(t)
	id := seedQuiz(t, conn, "d", models.QuizStatusDraft, nil)
	in := validTriviaInput("d")
	in.Status = models.QuizStatusPublished
	in.Definition.Questions[0].Answers[0].Correct = false
	req := httptest.NewRequest(http.MethodPatch, "/v1/quizzes/"+itoa(id), strings.NewReader(editorBody(t, in)))
	req.SetPathValue("id", itoa(id))
	rec := httptest.NewRecorder()
	PatchQuiz(conn).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publishing a draft with no correct answer must 422, got %d: %s", rec.Code, rec.Body)
	}
}

func TestQuizHTTP_ArticlePublishRefusesUnpublishedQuiz(t *testing.T) {
	conn := articlePatchTestDB(t)
	if err := db.EnsureQuizzesSchema(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "DELETE FROM "+db.QuizzesTableName); err != nil {
		t.Fatal(err)
	}
	dr := seedQuiz(t, conn, "dr", models.QuizStatusDraft, nil)
	body := `{"title":"With quiz","content":"<p>[quiz id=\"` + itoa(dr) + `\"]</p>","status":"draft","authors":[],"categories":[]}`
	rec := httptest.NewRecorder()
	PostArticles(conn).ServeHTTP(rec, postArticleRequest(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("draft with an unpublished quiz must save, got %d: %s", rec.Code, rec.Body)
	}
	blank := strings.Replace(body, `"status":"draft"`, `"status":""`, 1)
	rec = httptest.NewRecorder()
	PostArticles(conn).ServeHTTP(rec, postArticleRequest(blank))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a blank status with no date is a draft and must save, got %d: %s", rec.Code, rec.Body)
	}
	body = strings.Replace(body, `"status":"draft"`, `"status":"published"`, 1)
	rec = httptest.NewRecorder()
	PostArticles(conn).ServeHTTP(rec, postArticleRequest(body))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Quiz #"+itoa(dr)) {
		t.Fatalf("publishing must 422 naming the quiz, got %d: %s", rec.Code, rec.Body)
	}
}
