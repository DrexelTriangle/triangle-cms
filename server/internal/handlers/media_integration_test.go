package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
	"server/internal/models"
)

// End-to-end coverage of the media HTTP surface against a real MariaDB: upload
// puts a file on disk AND a row in the library, the listing renders URLs from
// the stored path, metadata round-trips, and delete removes both. Skips unless
// CMS_TEST_DSN is set, so CI without a database stays green.
//
//	CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/media_test?parseTime=true&multiStatements=true' go test ./internal/handlers/ -run MediaHTTP -v
func mediaHTTPTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping media handler integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	// One connection so the advisory lock below, which is per-session, is held
	// and released on the same session.
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()

	// The database package's integration tests drop and recreate the same
	// `media` table, and `go test ./...` runs packages in parallel, so the two
	// suites serialize on this lock. The name is shared with that package.
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the media test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS media"); err != nil {
		t.Fatalf("drop media table: %v", err)
	}
	if err := db.EnsureMediaTable(ctx, conn); err != nil {
		t.Fatalf("ensure media table: %v", err)
	}

	// Deleting an asset checks whether any article still points at it, so the
	// fixture needs the two columns that check reads: the lead image and the
	// body HTML, which carries editor-inserted <img> tags.
	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS articles"); err != nil {
		t.Fatalf("drop articles table: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			photo_url LONGTEXT,
			`+"`text`"+` LONGTEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}

	return conn
}

func mediaIDRequest(t *testing.T, method, target string, id int64, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	return req
}

func TestMediaHTTP_UploadListPatchDelete(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	t.Setenv("MEDIA_BASE_URL", "https://media.example.org")

	// Upload stores the file and records it in one step.
	rec := httptest.NewRecorder()
	PostMedia(conn).ServeHTTP(rec, uploadRequest(t, "Move In Day.PNG", pngBytes(t, 12, 7)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("upload response carries no library id")
	}
	if created.Width != 12 || created.Height != 7 || created.ContentType != "image/png" {
		t.Fatalf("upload meta = %+v, want png 12x7", created)
	}
	absPath := filepath.Join(root, filepath.FromSlash(created.Path))
	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}

	// The listing finds it by the slugified name and renders the display URL.
	listRec := httptest.NewRecorder()
	GetMedia(conn).ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/v1/media?search=move-in", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listed models.MediaResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Media) != 1 || listed.Pagination.TotalCount != 1 {
		t.Fatalf("list = %+v, want exactly the uploaded asset", listed)
	}
	if want := "https://media.example.org/" + created.Path; listed.Media[0].URL != want {
		t.Fatalf("url = %q, want %q rendered from MEDIA_BASE_URL", listed.Media[0].URL, want)
	}

	// Alt text round-trips.
	patchRec := httptest.NewRecorder()
	PatchMediaItem(conn).ServeHTTP(patchRec,
		mediaIDRequest(t, http.MethodPatch, "/v1/media/1", created.ID, `{"alt_text":"Students moving in"}`))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", patchRec.Code, patchRec.Body.String())
	}
	var patched models.Media
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.AltText != "Students moving in" {
		t.Fatalf("alt_text = %q, want it persisted", patched.AltText)
	}

	// An empty patch is a 400, not a silent success.
	emptyRec := httptest.NewRecorder()
	PatchMediaItem(conn).ServeHTTP(emptyRec, mediaIDRequest(t, http.MethodPatch, "/v1/media/1", created.ID, `{}`))
	if emptyRec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d, want 400", emptyRec.Code)
	}

	// Delete removes the row and the file.
	delRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(delRec, mediaIDRequest(t, http.MethodDelete, "/v1/media/1", created.ID, ""))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", delRec.Code, delRec.Body.String())
	}
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Fatalf("file should be gone, stat err = %v", err)
	}
	if _, err := db.GetMediaByID(context.Background(), conn, created.ID); err != sql.ErrNoRows {
		t.Fatalf("row should be gone, err = %v", err)
	}

	// A second delete is a 404 rather than a 500.
	goneRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(goneRec, mediaIDRequest(t, http.MethodDelete, "/v1/media/1", created.ID, ""))
	if goneRec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", goneRec.Code)
	}
}

// The public gallery is a curated selection, not the upload directory. Serving
// everything put house ads, comic strips and crossword scans on /photo.
func TestMediaHTTP_PublicGalleryOnlyServesCuratedImages(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	t.Setenv("MEDIA_BASE_URL", "https://media.example.org")

	upload := func(name string) models.MediaUploadResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		PostMedia(conn).ServeHTTP(rec, uploadRequest(t, name, pngBytes(t, 4, 4)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		var created models.MediaUploadResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode upload %s: %v", name, err)
		}
		return created
	}

	photo := upload("basketball.png")
	upload("house-ad.png")

	// Nothing is in the gallery until an editor says so, including brand-new
	// uploads: the default has to be off or the /photo page fills itself.
	emptyRec := httptest.NewRecorder()
	GetPublicGallery(conn).ServeHTTP(emptyRec, httptest.NewRequest(http.MethodGet, "/v1/gallery", nil))
	var empty models.MediaGalleryResponse
	if err := json.Unmarshal(emptyRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode gallery: %v", err)
	}
	if len(empty.Media) != 0 {
		t.Fatalf("gallery = %+v, want nothing before anything is marked", empty.Media)
	}

	markRec := httptest.NewRecorder()
	PatchMediaItem(conn).ServeHTTP(markRec,
		mediaIDRequest(t, http.MethodPatch, "/v1/media/1", photo.ID, `{"in_gallery":true}`))
	if markRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", markRec.Code, markRec.Body.String())
	}
	var marked models.Media
	if err := json.Unmarshal(markRec.Body.Bytes(), &marked); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if !marked.InGallery {
		t.Fatal("in_gallery did not persist")
	}

	galleryRec := httptest.NewRecorder()
	GetPublicGallery(conn).ServeHTTP(galleryRec, httptest.NewRequest(http.MethodGet, "/v1/gallery", nil))
	var gallery models.MediaGalleryResponse
	if err := json.Unmarshal(galleryRec.Body.Bytes(), &gallery); err != nil {
		t.Fatalf("decode gallery: %v", err)
	}
	if len(gallery.Media) != 1 || gallery.Media[0].ID != photo.ID {
		t.Fatalf("gallery = %+v, want only the marked photo", gallery.Media)
	}

	// The editor-facing listing still sees the whole library, and can narrow to
	// the curated set on demand.
	listRec := httptest.NewRecorder()
	GetMedia(conn).ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/v1/media", nil))
	var listed models.MediaResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Pagination.TotalCount != 2 {
		t.Fatalf("library total = %d, want both assets", listed.Pagination.TotalCount)
	}

	filteredRec := httptest.NewRecorder()
	GetMedia(conn).ServeHTTP(filteredRec, httptest.NewRequest(http.MethodGet, "/v1/media?in_gallery=true", nil))
	var filtered models.MediaResponse
	if err := json.Unmarshal(filteredRec.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if filtered.Pagination.TotalCount != 1 || len(filtered.Media) != 1 || filtered.Media[0].ID != photo.ID {
		t.Fatalf("in_gallery=true list = %+v, want only the marked photo", filtered)
	}
}

func TestMediaHTTP_DeleteKeepFileLeavesTheFile(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)

	rec := httptest.NewRecorder()
	PostMedia(conn).ServeHTTP(rec, uploadRequest(t, "keep.png", pngBytes(t, 3, 3)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}

	delRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(delRec,
		mediaIDRequest(t, http.MethodDelete, "/v1/media/1?keep_file=true", created.ID, ""))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", delRec.Code, delRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(created.Path))); err != nil {
		t.Fatalf("keep_file should have left the file in place: %v", err)
	}
}

// An asset a published article still points at must not be deletable, or the
// article's image silently 404s on the public site.
func TestMediaHTTP_DeleteRefusesAssetInUse(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	t.Setenv("MEDIA_BASE_URL", "https://media.example.org")

	rec := httptest.NewRecorder()
	PostMedia(conn).ServeHTTP(rec, uploadRequest(t, "featured.png", pngBytes(t, 5, 5)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}

	// The ETL stores absolute URLs, so the usage check has to match on suffix.
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (photo_url) VALUES (?)", "https://media.example.org/"+created.Path); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	delRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(delRec, mediaIDRequest(t, http.MethodDelete, "/v1/media/1", created.ID, ""))
	if delRec.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409; body = %s", delRec.Code, delRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(created.Path))); err != nil {
		t.Fatalf("a refused delete must leave the file alone: %v", err)
	}
	if _, err := db.GetMediaByID(context.Background(), conn, created.ID); err != nil {
		t.Fatalf("a refused delete must leave the row alone: %v", err)
	}
}

// The same protection for an image that only ever appears inside the article
// body. Checking photo_url alone let this delete through and unlink a file a
// published article was still rendering: the one path here that destroyed
// content rather than just confusing the library.
func TestMediaHTTP_DeleteRefusesAssetUsedInArticleBody(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)

	rec := httptest.NewRecorder()
	PostMedia(conn).ServeHTTP(rec, uploadRequest(t, "inline.png", pngBytes(t, 5, 5)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload: %v", err)
	}

	// No photo_url at all: the reference exists only as an editor-inserted tag
	// in the middle of the body HTML.
	body := `<p>before</p><figure><img src="/` + created.Path + `" alt="x"></figure><p>after</p>`
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (`text`) VALUES (?)", body); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	delRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(delRec, mediaIDRequest(t, http.MethodDelete, "/v1/media/1", created.ID, ""))
	if delRec.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409; body = %s", delRec.Code, delRec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(created.Path))); err != nil {
		t.Fatalf("a refused delete must leave the file alone: %v", err)
	}
}

// An underscore is a LIKE single-char wildcard, and the migrated corpus is full
// of names like IMG_1234.jpg. Unescaped, the usage check matched articles that
// reference a *different* file and refused a legitimate delete.
func TestMediaHTTP_DeleteIsNotBlockedByWildcardLookalike(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)

	relPath := "wp-content/uploads/2019/01/IMG_1234.jpg"
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o775); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, pngBytes(t, 2, 2), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	id, err := db.InsertMedia(context.Background(), conn, relPath, "image/jpeg", 10, 2, 2)
	if err != nil {
		t.Fatalf("insert media: %v", err)
	}

	// Differs from relPath only where the wildcard would match.
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (photo_url) VALUES (?)",
		"https://media.example.org/wp-content/uploads/2019/01/IMGx1234.jpg"); err != nil {
		t.Fatalf("seed article: %v", err)
	}

	delRec := httptest.NewRecorder()
	DeleteMediaItem(conn).ServeHTTP(delRec, mediaIDRequest(t, http.MethodDelete, "/v1/media/1", id, ""))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body = %s", delRec.Code, delRec.Body.String())
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("file should have been removed, stat err = %v", err)
	}
}

func TestMediaHTTP_GetMissingItemIs404(t *testing.T) {
	conn := mediaHTTPTestDB(t)

	rec := httptest.NewRecorder()
	GetMediaItem(conn).ServeHTTP(rec, mediaIDRequest(t, http.MethodGet, "/v1/media/999999", 999999, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// The whole point of the async index: the walk must outlive the request that
// started it. This asserts POST returns 202 immediately and the work completes
// afterwards, which the previous synchronous version could not do behind a
// proxy that cuts idle upstream reads at 60s.
func TestMediaHTTP_IndexRunsInBackground(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)

	for _, rel := range []string{
		"wp-content/uploads/2019/05/campus.jpg",
		"wp-content/uploads/2019/05/campus-150x150.jpg", // derivative, must be skipped
		"wp-content/uploads/2020/11/protest.png",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Ensure the shared job is idle, and release it however this test exits.
	if !mediaIndexer.tryStart() {
		t.Fatal("package index job was unexpectedly busy")
	}
	mediaIndexer.finish(models.MediaIndexResponse{}, nil)

	start := time.Now()
	rec := httptest.NewRecorder()
	PostMediaIndex(conn).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/media/index", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	// "Immediately" — the handler must not be doing the walk inline.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("POST took %v; it should return before the walk finishes", elapsed)
	}

	deadline := time.Now().Add(30 * time.Second)
	var status models.MediaIndexStatusResponse
	for {
		statusRec := httptest.NewRecorder()
		GetMediaIndexStatus().ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/v1/media/index", nil))
		if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		if !status.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index did not finish within 30s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	if status.Error != "" {
		t.Fatalf("index reported error: %s", status.Error)
	}
	if status.FinishedAt == nil || status.StartedAt == nil {
		t.Fatalf("expected both timestamps, got %+v", status)
	}
	if status.Progress.Added != 2 {
		t.Fatalf("added = %d, want 2 (the derivative must be skipped)", status.Progress.Added)
	}

	// And the rows are really there, written by the background goroutine after
	// the request had already returned.
	_, total, err := db.ListMedia(context.Background(), conn, models.MediaListParams{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("media rows = %d, want 2", total)
	}
}
