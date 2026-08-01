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

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/models"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/comments_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run CommentHTTP -v
func commentHTTPTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping comment handler integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_comments_integration_test").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the comments test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_comments_integration_test")
		conn.Close()
	})

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS comments"); err != nil {
		t.Fatalf("drop comments table: %v", err)
	}
	if err := db.EnsureCommentsTable(ctx, conn); err != nil {
		t.Fatalf("ensure comments table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			slug VARCHAR(255) NOT NULL UNIQUE,
			comment_status VARCHAR(32)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}

	return conn
}

func seedCommentArticle(t *testing.T, conn *sql.DB, slug, commentStatus string) int64 {
	t.Helper()

	result, err := conn.ExecContext(context.Background(), "INSERT INTO articles (slug, comment_status) VALUES (?, ?)", slug, commentStatus)
	if err != nil {
		t.Fatalf("seed article: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read article id: %v", err)
	}
	return id
}

func postArticleCommentRequest(slug string, parentID int64) *http.Request {
	body := `{"parent_id":` + strconv.FormatInt(parentID, 10) + `,"author_name":"Reader","author_email":"reader@example.org","content":"A useful comment."}`
	req := httptest.NewRequest(http.MethodPost, "/v1/articles/"+slug+"/comments", strings.NewReader(body))
	req.SetPathValue("slug", slug)
	return req
}

func TestCommentHTTP_PostRejectsClosedArticle(t *testing.T) {
	conn := commentHTTPTestDB(t)
	seedCommentArticle(t, conn, "closed-story", "closed")

	rec := httptest.NewRecorder()
	PostArticleComment(conn, nil).ServeHTTP(rec, postArticleCommentRequest("closed-story", 0))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("post status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM comments").Scan(&count); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if count != 0 {
		t.Fatalf("closed article created %d comments, want 0", count)
	}
}

func TestCommentHTTP_PostOpenArticleCreatesPendingCommentWithoutSpamChecker(t *testing.T) {
	conn := commentHTTPTestDB(t)
	articleID := seedCommentArticle(t, conn, "open-story", "open")

	rec := httptest.NewRecorder()
	PostArticleComment(conn, nil).ServeHTTP(rec, postArticleCommentRequest("open-story", 0))

	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var comment models.CommentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &comment); err != nil {
		t.Fatalf("decode comment response: %v", err)
	}
	if comment.ArticleID != articleID {
		t.Fatalf("article_id = %d, want %d", comment.ArticleID, articleID)
	}
	if comment.Status != "pending" {
		t.Fatalf("status = %q, want pending", comment.Status)
	}
	if strings.TrimSpace(comment.Content) == "" {
		t.Fatal("comment content was not returned")
	}

	var storedArticleID, storedParentID int64
	var storedStatus string
	if err := conn.QueryRowContext(context.Background(), "SELECT article_id, parent_id, `status` FROM comments WHERE id = ?", comment.ID).Scan(&storedArticleID, &storedParentID, &storedStatus); err != nil {
		t.Fatalf("read stored comment: %v", err)
	}
	if storedArticleID != articleID || storedParentID != 0 || storedStatus != "pending" {
		t.Fatalf("stored comment = article_id %d parent_id %d status %q, want article_id %d parent_id 0 status pending", storedArticleID, storedParentID, storedStatus, articleID)
	}
}

func TestCommentHTTP_PostAllowsReplyToApprovedTopLevelComment(t *testing.T) {
	conn := commentHTTPTestDB(t)
	articleID := seedCommentArticle(t, conn, "reply-story", "open")
	parent, err := db.CreateComment(context.Background(), conn, db.CreateCommentParams{
		ArticleID:  articleID,
		AuthorName: "Parent",
		Content:    "Top-level comment",
		Status:     "approved",
		Type:       "comment",
	})
	if err != nil {
		t.Fatalf("seed parent comment: %v", err)
	}

	rec := httptest.NewRecorder()
	PostArticleComment(conn, nil).ServeHTTP(rec, postArticleCommentRequest("reply-story", parent.ID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var comment models.CommentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &comment); err != nil {
		t.Fatalf("decode comment response: %v", err)
	}
	if comment.ParentID != parent.ID {
		t.Fatalf("parent_id = %d, want %d", comment.ParentID, parent.ID)
	}
}

func TestCommentHTTP_PostAllowsReplyToReply(t *testing.T) {
	conn := commentHTTPTestDB(t)
	articleID := seedCommentArticle(t, conn, "nested-story", "open")
	parent, err := db.CreateComment(context.Background(), conn, db.CreateCommentParams{
		ArticleID:  articleID,
		AuthorName: "Parent",
		Content:    "Top-level comment",
		Status:     "approved",
		Type:       "comment",
	})
	if err != nil {
		t.Fatalf("seed parent comment: %v", err)
	}
	reply, err := db.CreateComment(context.Background(), conn, db.CreateCommentParams{
		ArticleID:  articleID,
		ParentID:   parent.ID,
		AuthorName: "Reply",
		Content:    "First-level reply",
		Status:     "approved",
		Type:       "comment",
	})
	if err != nil {
		t.Fatalf("seed reply comment: %v", err)
	}

	rec := httptest.NewRecorder()
	PostArticleComment(conn, nil).ServeHTTP(rec, postArticleCommentRequest("nested-story", reply.ID))

	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	var comment models.CommentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &comment); err != nil {
		t.Fatalf("decode comment response: %v", err)
	}
	if comment.ParentID != reply.ID {
		t.Fatalf("parent_id = %d, want %d", comment.ParentID, reply.ID)
	}
}
