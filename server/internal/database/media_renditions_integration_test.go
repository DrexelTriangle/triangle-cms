package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run MediaRenditions -v
func mediaRenditionsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping media renditions integration test")
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
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the media renditions test lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	for _, table := range []string{"media_renditions", "media"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s table: %v", table, err)
		}
	}
	if err := EnsureMediaTable(ctx, conn); err != nil {
		t.Fatalf("create media table: %v", err)
	}
	if err := EnsureMediaRenditionsTable(ctx, conn); err != nil {
		t.Fatalf("create media renditions table: %v", err)
	}
	// Idempotent, since it runs on every boot.
	if err := EnsureMediaRenditionsTable(ctx, conn); err != nil {
		t.Fatalf("re-run media renditions migration: %v", err)
	}
	return conn
}

func insertTestMedia(t *testing.T, conn *sql.DB, path, mime, createdAt string) int64 {
	t.Helper()
	result, err := conn.Exec(`
		INSERT INTO media (path, file_name, mime_type, size_bytes, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?)
	`, path, path, mime, createdAt, createdAt)
	if err != nil {
		t.Fatalf("insert media %s: %v", path, err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestMediaRenditionsLifecycle(t *testing.T) {
	conn := mediaRenditionsTestDB(t)
	ctx := context.Background()

	old := insertTestMedia(t, conn, "wp-content/uploads/2012/01/old.jpg", "image/jpeg", "2012-01-01 00:00:00")
	recent := insertTestMedia(t, conn, "wp-content/uploads/2026/08/new.png", "image/png", "2026-08-01 00:00:00")
	insertTestMedia(t, conn, "wp-content/uploads/2026/08/anim.gif", "image/gif", "2026-08-02 00:00:00")
	insertTestMedia(t, conn, "wp-content/uploads/2026/08/logo.svg", "image/svg+xml", "2026-08-03 00:00:00")

	// Newest first, and only formats the sidecar renders.
	sources, err := MediaNeedingRenditions(ctx, conn, "r1", 10)
	if err != nil {
		t.Fatalf("needing renditions: %v", err)
	}
	if len(sources) != 2 || sources[0].MediaID != recent || sources[1].MediaID != old {
		t.Fatalf("unexpected sources %+v", sources)
	}

	v1 := []RenditionVariant{{Path: "wp-content/variants/r1/2026/08/new.png.480w.webp", Width: 480, Height: 240, SizeBytes: 10}}
	if err := SaveRenditions(ctx, conn, recent, "r1", v1); err != nil {
		t.Fatalf("save renditions: %v", err)
	}
	if err := SaveRenditionFailure(ctx, conn, old, "r1", nil, "not a decodable image"); err != nil {
		t.Fatalf("save failure: %v", err)
	}

	// Both are settled for r1, the failure included: it is not retried.
	if sources, err = MediaNeedingRenditions(ctx, conn, "r1", 10); err != nil || len(sources) != 0 {
		t.Fatalf("expected nothing left for r1, got %+v (%v)", sources, err)
	}

	// A new recipe re-queues everything, carrying the old variants along so the
	// caller can delete them once replaced.
	sources, err = MediaNeedingRenditions(ctx, conn, "r2", 10)
	if err != nil || len(sources) != 2 {
		t.Fatalf("expected both re-queued for r2, got %+v (%v)", sources, err)
	}
	if len(sources[0].Previous) != 1 || sources[0].Previous[0].Path != v1[0].Path {
		t.Fatalf("previous variants not carried: %+v", sources[0].Previous)
	}

	byPath, err := RenditionsByMediaPath(ctx, conn)
	if err != nil {
		t.Fatalf("renditions by path: %v", err)
	}
	if len(byPath) != 1 || len(byPath["wp-content/uploads/2026/08/new.png"]) != 1 {
		t.Fatalf("unexpected index contents %+v", byPath)
	}

	// A failed re-render under a new recipe keeps serving the old variants.
	if err := SaveRenditionFailure(ctx, conn, recent, "r2", v1, "timeout"); err != nil {
		t.Fatalf("save failure with previous: %v", err)
	}
	if byPath, _ = RenditionsByMediaPath(ctx, conn); len(byPath["wp-content/uploads/2026/08/new.png"]) != 1 {
		t.Fatalf("old variants dropped after a failed re-render: %+v", byPath)
	}

	// Deleting the media row leaves an orphan whose files can still be found.
	if _, err := conn.Exec("DELETE FROM media WHERE id = ?", recent); err != nil {
		t.Fatal(err)
	}
	orphans, err := OrphanedRenditions(ctx, conn, 10)
	if err != nil || len(orphans) != 1 || orphans[0].MediaID != recent || len(orphans[0].Variants) != 1 {
		t.Fatalf("unexpected orphans %+v (%v)", orphans, err)
	}
	if err := DeleteRendition(ctx, conn, recent); err != nil {
		t.Fatal(err)
	}
	if orphans, _ = OrphanedRenditions(ctx, conn, 10); len(orphans) != 0 {
		t.Fatalf("orphan survived deletion: %+v", orphans)
	}
}
