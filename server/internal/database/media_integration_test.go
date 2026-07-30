package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

// Like the poll tests, these need a real MariaDB: the media library leans on a
// unique key for no-duplicate indexing and on server-side LIKE/ORDER semantics.
// They skip unless CMS_TEST_DSN is set, so CI without a database stays green.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/media_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run Media -v
func mediaTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping media database integration test")
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
	acquireMediaTestLock(ctx, t, conn)

	if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS media"); err != nil {
		t.Fatalf("drop media table: %v", err)
	}
	if err := EnsureMediaTable(ctx, conn); err != nil {
		t.Fatalf("ensure media table: %v", err)
	}

	return conn
}

// mediaTestLockName guards the shared `media` table. The handler package has its
// own integration tests against the same table, and `go test ./...` runs
// packages in parallel, so without this one package drops the table the other is
// midway through using. The same name is used there.
const mediaTestLockName = "cms_media_integration_test"

func acquireMediaTestLock(ctx context.Context, t *testing.T, conn *sql.DB) {
	t.Helper()

	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", mediaTestLockName).Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the media test lock")
	}

	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", mediaTestLockName)
		conn.Close()
	})
}

// writeFile creates a zero-content file (and its parents) under root.
func writeFile(t *testing.T, root, relPath string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func TestEnsureMediaTable_IsIdempotent(t *testing.T) {
	conn := mediaTestDB(t)
	// The startup path runs this on every boot against an existing table.
	if err := EnsureMediaTable(context.Background(), conn); err != nil {
		t.Fatalf("second EnsureMediaTable: %v", err)
	}
}

func TestIndexMediaRoot_SkipsDerivativesAndRepeats(t *testing.T) {
	conn := mediaTestDB(t)
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "wp-content/uploads/2019/05/campus.jpg")
	writeFile(t, root, "wp-content/uploads/2019/05/campus-150x150.jpg") // WP thumbnail
	writeFile(t, root, "wp-content/uploads/2019/05/campus-1024x768.jpg")
	writeFile(t, root, "wp-content/uploads/2020/11/protest.png")
	writeFile(t, root, "wp-content/uploads/2020/11/notes.txt")    // not an image
	writeFile(t, root, "wp-content/uploads/2020/11/sizes-1x.png") // not a WxH suffix

	report, err := IndexMediaRoot(ctx, conn, root, "wp-content/uploads")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if report.Added != 3 || report.Scanned != 3 || report.Skipped != 0 {
		t.Fatalf("report = %+v, want 3 added / 3 scanned / 0 skipped", report)
	}

	// Re-running must not duplicate rows and must not clobber curated metadata.
	items, _, err := ListMedia(ctx, conn, models.MediaListParams{Query: "campus"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 campus row, got %d", len(items))
	}
	altText := "Students on campus"
	if _, err := UpdateMediaMeta(ctx, conn, items[0].ID, models.MediaPatch{AltText: &altText}); err != nil {
		t.Fatalf("set alt text: %v", err)
	}

	second, err := IndexMediaRoot(ctx, conn, root, "wp-content/uploads")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if second.Added != 0 || second.Skipped != 3 {
		t.Fatalf("reindex report = %+v, want 0 added / 3 skipped", second)
	}

	preserved, err := GetMediaByID(ctx, conn, items[0].ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if preserved.AltText != altText {
		t.Fatalf("alt text = %q, want %q preserved across reindex", preserved.AltText, altText)
	}
}

func TestIndexMediaRoot_MissingRootIsNotAnError(t *testing.T) {
	conn := mediaTestDB(t)
	report, err := IndexMediaRoot(context.Background(), conn, filepath.Join(t.TempDir(), "absent"), "wp-content/uploads")
	if err != nil {
		t.Fatalf("expected a missing tree to be tolerated, got %v", err)
	}
	if report.Scanned != 0 || report.Added != 0 {
		t.Fatalf("report = %+v, want zeroes", report)
	}
}

func TestListMedia_SearchFilterAndPaging(t *testing.T) {
	conn := mediaTestDB(t)
	ctx := context.Background()

	for _, seed := range []struct {
		path string
		mime string
		size int64
	}{
		{"wp-content/uploads/2024/01/alpha.jpg", "image/jpeg", 300},
		{"wp-content/uploads/2024/02/beta.png", "image/png", 100},
		{"wp-content/uploads/2024/03/gamma.png", "image/png", 200},
	} {
		if _, err := InsertMedia(ctx, conn, seed.path, seed.mime, seed.size, 0, 0); err != nil {
			t.Fatalf("seed %s: %v", seed.path, err)
		}
	}

	t.Run("search matches file name", func(t *testing.T) {
		items, total, err := ListMedia(ctx, conn, models.MediaListParams{Query: "beta"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 1 || len(items) != 1 || items[0].FileName != "beta.png" {
			t.Fatalf("items = %+v (total %d), want just beta.png", items, total)
		}
	})

	t.Run("mime family filter", func(t *testing.T) {
		_, total, err := ListMedia(ctx, conn, models.MediaListParams{MimeType: "image/"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 3 {
			t.Fatalf("total = %d, want all 3 for the image/ family", total)
		}

		_, exact, err := ListMedia(ctx, conn, models.MediaListParams{MimeType: "image/png"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if exact != 2 {
			t.Fatalf("total = %d, want 2 pngs", exact)
		}
	})

	t.Run("total count ignores the page window", func(t *testing.T) {
		items, total, err := ListMedia(ctx, conn, models.MediaListParams{Limit: 2})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != 2 || total != 3 {
			t.Fatalf("got %d items with total %d, want 2 of 3", len(items), total)
		}

		page2, _, err := ListMedia(ctx, conn, models.MediaListParams{Limit: 2, Offset: 2})
		if err != nil {
			t.Fatalf("list page 2: %v", err)
		}
		if len(page2) != 1 {
			t.Fatalf("page 2 = %d items, want 1", len(page2))
		}
		if page2[0].ID == items[0].ID || page2[0].ID == items[1].ID {
			t.Fatalf("page 2 repeated a row from page 1")
		}
	})

	t.Run("sorts by size", func(t *testing.T) {
		items, _, err := ListMedia(ctx, conn, models.MediaListParams{
			SortBy:        models.MediaSortBySizeBytes,
			SortDirection: models.SortDirectionAscending,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != 3 || items[0].SizeBytes != 100 || items[2].SizeBytes != 300 {
			t.Fatalf("sizes = %+v, want ascending 100..300", items)
		}
	})
}

func TestUpdateMediaMeta(t *testing.T) {
	conn := mediaTestDB(t)
	ctx := context.Background()

	id, err := InsertMedia(ctx, conn, "wp-content/uploads/2024/01/alpha.jpg", "image/jpeg", 10, 0, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("rejects an empty patch", func(t *testing.T) {
		if _, err := UpdateMediaMeta(ctx, conn, id, models.MediaPatch{}); err != ErrNoMediaFields {
			t.Fatalf("err = %v, want ErrNoMediaFields", err)
		}
	})

	t.Run("writes only the supplied fields", func(t *testing.T) {
		caption := "Move-in day"
		updated, err := UpdateMediaMeta(ctx, conn, id, models.MediaPatch{Caption: &caption})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.Caption != caption {
			t.Fatalf("caption = %q, want %q", updated.Caption, caption)
		}
		if updated.FileName != "alpha.jpg" {
			t.Fatalf("file_name = %q, should be untouched by a caption-only patch", updated.FileName)
		}
		if updated.UpdatedAt == nil {
			t.Fatal("updated_at should be stamped")
		}
	})

	t.Run("missing row is ErrNoRows", func(t *testing.T) {
		caption := "nobody"
		if _, err := UpdateMediaMeta(ctx, conn, 999999, models.MediaPatch{Caption: &caption}); err != sql.ErrNoRows {
			t.Fatalf("err = %v, want sql.ErrNoRows", err)
		}
	})
}

func TestDeleteMediaRow(t *testing.T) {
	conn := mediaTestDB(t)
	ctx := context.Background()

	id, err := InsertMedia(ctx, conn, "wp-content/uploads/2024/01/alpha.jpg", "image/jpeg", 10, 0, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := DeleteMediaRow(ctx, conn, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetMediaByID(ctx, conn, id); err != sql.ErrNoRows {
		t.Fatalf("err = %v, want sql.ErrNoRows after delete", err)
	}
	if err := DeleteMediaRow(ctx, conn, id); err != sql.ErrNoRows {
		t.Fatalf("second delete err = %v, want sql.ErrNoRows", err)
	}
}

// The index reports progress as it walks, which is what lets the HTTP layer run
// it in the background and be polled. A tree smaller than progressInterval must
// still produce a final report rather than silently never calling back.
func TestIndexMediaRootWithProgress_ReportsProgress(t *testing.T) {
	conn := mediaTestDB(t)
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "wp-content/uploads/2021/01/one.jpg")
	writeFile(t, root, "wp-content/uploads/2021/01/two.png")
	writeFile(t, root, "wp-content/uploads/2021/02/three.gif")

	var updates []models.MediaIndexResponse
	report, err := IndexMediaRootWithProgress(ctx, conn, root, "wp-content/uploads",
		func(p models.MediaIndexResponse) { updates = append(updates, p) })
	if err != nil {
		t.Fatalf("index: %v", err)
	}

	if len(updates) == 0 {
		t.Fatal("expected at least one progress callback")
	}
	final := updates[len(updates)-1]
	if final.Added != report.Added || final.Scanned != report.Scanned {
		t.Fatalf("last callback %+v disagrees with returned report %+v", final, report)
	}
	if report.Added != 3 {
		t.Fatalf("added = %d, want 3", report.Added)
	}
	// Walked counts every entry visited (files AND directories), so it must
	// exceed the three indexed files — that is what makes it a useful progress
	// signal on a corpus that is mostly skipped derivatives.
	if report.Walked <= report.Added {
		t.Fatalf("walked = %d, expected it to exceed added = %d", report.Walked, report.Added)
	}
}
