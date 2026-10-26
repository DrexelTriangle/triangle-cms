package database

import (
	"context"
	"database/sql"

	"server/internal/models"
)

// legacy_redirects maps the old WordPress host's URLs (cms.thetriangle.org) to
// their pages on www.thetriangle.org, so the links search engines, old
// newsletters and other sites hold keep working once WordPress is switched
// off. The CMS only creates and serves the table; the rows are written by
// scripts/build_legacy_redirects.py, which reads the WordPress database
// directly, and consumed by Delta's sync-legacy-redirects, which turns them
// into the nginx maps cms.thetriangle.org is answered from. See
// schema/legacy_redirects.sql.

func EnsureLegacyRedirectsTable(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, TableSchema("legacy_redirects")); err != nil {
		return err
	}

	// Expand-only migration for databases that already have the table: the
	// CREATE above is a no-op there, so a column added to
	// schema/legacy_redirects.sql reaches them only through this block. Keep
	// the two in step, and give every NOT NULL column a DEFAULT: without one
	// the ALTER fails on a table that already has rows, and so does the boot.
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE legacy_redirects
		ADD COLUMN IF NOT EXISTS target VARCHAR(1024) NOT NULL DEFAULT '',
		ADD COLUMN IF NOT EXISTS status SMALLINT UNSIGNED NOT NULL DEFAULT 301,
		ADD COLUMN IF NOT EXISTS origin VARCHAR(32) NOT NULL DEFAULT 'manual',
		ADD COLUMN IF NOT EXISTS wp_object_id BIGINT UNSIGNED NULL,
		ADD COLUMN IF NOT EXISTS article_id BIGINT NULL,
		ADD COLUMN IF NOT EXISTS note VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	`)
	return err
}

// ListLegacyRedirects returns every redirect, ordered so the export is stable
// from one fetch to the next and the sync can skip a reload when nothing
// changed.
func ListLegacyRedirects(ctx context.Context, conn *sql.DB) ([]models.LegacyRedirect, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT `source_kind`, `source`, `target`, `status` FROM `legacy_redirects` ORDER BY `source_kind`, `source`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	redirects := make([]models.LegacyRedirect, 0)
	for rows.Next() {
		var r models.LegacyRedirect
		if err := rows.Scan(&r.SourceKind, &r.Source, &r.Target, &r.Status); err != nil {
			return nil, err
		}
		redirects = append(redirects, r)
	}
	return redirects, rows.Err()
}
