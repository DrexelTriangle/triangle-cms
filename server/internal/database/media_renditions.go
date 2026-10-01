package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

// Rendition statuses. A failed row is not retried until the recipe changes: the
// sidecar only reports a failure as permanent when the bytes themselves are the
// problem, and re-sending them every pass would just repeat it.
const (
	RenditionStatusOK     = "ok"
	RenditionStatusFailed = "failed"
)

// RenditionVariant is one rendered file. Path is MEDIA_ROOT-relative, the same
// shape as media.path, so URLs come from it the same way.
type RenditionVariant struct {
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int64  `json:"size_bytes"`
}

// EnsureMediaRenditionsTable creates the table recording which resized copies
// exist for each media row.
//
// One row per media item rather than one per variant, so a re-render replaces
// the whole set in a single upsert and a reader never sees half of the old
// ladder mixed with half of the new one.
//
// There is deliberately no foreign key to media. A cascade would drop the row
// the moment the media row goes, and with it the only record of which variant
// files to delete. The reconciler removes orphans itself, files first.
func EnsureMediaRenditionsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS media_renditions (
			media_id BIGINT NOT NULL PRIMARY KEY,
			recipe VARCHAR(64) NOT NULL,
			status VARCHAR(16) NOT NULL,
			variants LONGTEXT NULL,
			error VARCHAR(512) NULL,
			updated_at DATETIME NOT NULL,
			INDEX idx_media_renditions_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	return err
}

// RenditionSource is a media item that needs rendering, plus whatever variants
// it currently has so the caller can delete them once they are replaced.
type RenditionSource struct {
	MediaID  int64
	Path     string
	Previous []RenditionVariant
}

// renderableMimeTypes are the formats worth rendering. GIF is left out on
// purpose: most of the GIFs in the library are animated, and a WebP rendition
// of the first frame would silently replace an animation with a still.
var renderableMimeTypes = []any{"image/jpeg", "image/png", "image/webp"}

// MediaNeedingRenditions returns up to limit media items with no rendition for
// recipe, newest first so a backfill reaches the images readers are looking at
// before the 2011 archive.
func MediaNeedingRenditions(ctx context.Context, conn *sql.DB, recipe string, limit int) ([]RenditionSource, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0")
	}

	args := append(append([]any{}, renderableMimeTypes...), recipe)
	rows, err := conn.QueryContext(ctx, `
		SELECT m.id, m.path, r.variants
		FROM media AS m
		LEFT JOIN media_renditions AS r ON r.media_id = m.id
		WHERE m.mime_type IN (?, ?, ?)
		  AND (r.media_id IS NULL OR r.recipe <> ?)
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []RenditionSource
	for rows.Next() {
		var source RenditionSource
		var variants sql.NullString
		if err := rows.Scan(&source.MediaID, &source.Path, &variants); err != nil {
			return nil, err
		}
		if source.Previous, err = decodeVariants(variants); err != nil {
			return nil, fmt.Errorf("media %d: %w", source.MediaID, err)
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// SaveRenditions records a successful render, replacing any earlier set.
func SaveRenditions(ctx context.Context, conn *sql.DB, mediaID int64, recipe string, variants []RenditionVariant) error {
	encoded, err := json.Marshal(variants)
	if err != nil {
		return err
	}
	return upsertRendition(ctx, conn, mediaID, recipe, RenditionStatusOK, sql.NullString{String: string(encoded), Valid: true}, sql.NullString{})
}

// SaveRenditionFailure records that mediaID cannot be rendered under recipe.
// Any variants from an earlier recipe stay on disk and in use: they are still
// valid images, just made by an older recipe, so they are kept in the row.
func SaveRenditionFailure(ctx context.Context, conn *sql.DB, mediaID int64, recipe string, previous []RenditionVariant, reason string) error {
	if len(reason) > 512 {
		reason = reason[:512]
	}
	variants := sql.NullString{}
	if len(previous) > 0 {
		encoded, err := json.Marshal(previous)
		if err != nil {
			return err
		}
		variants = sql.NullString{String: string(encoded), Valid: true}
	}
	return upsertRendition(ctx, conn, mediaID, recipe, RenditionStatusFailed, variants, sql.NullString{String: reason, Valid: true})
}

func upsertRendition(ctx context.Context, conn *sql.DB, mediaID int64, recipe, status string, variants, reason sql.NullString) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO media_renditions (media_id, recipe, status, variants, error, updated_at)
		VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())
		ON DUPLICATE KEY UPDATE
			recipe = VALUES(recipe),
			status = VALUES(status),
			variants = VALUES(variants),
			error = VALUES(error),
			updated_at = UTC_TIMESTAMP()
	`, mediaID, recipe, status, variants, reason)
	return err
}

// OrphanedRendition is a rendition whose media row no longer exists.
type OrphanedRendition struct {
	MediaID  int64
	Variants []RenditionVariant
}

// OrphanedRenditions returns up to limit renditions left behind by deleted
// media, so their files can be removed before their rows.
func OrphanedRenditions(ctx context.Context, conn *sql.DB, limit int) ([]OrphanedRendition, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0")
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT r.media_id, r.variants
		FROM media_renditions AS r
		LEFT JOIN media AS m ON m.id = r.media_id
		WHERE m.id IS NULL
		LIMIT `+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orphans []OrphanedRendition
	for rows.Next() {
		var orphan OrphanedRendition
		var variants sql.NullString
		if err := rows.Scan(&orphan.MediaID, &variants); err != nil {
			return nil, err
		}
		if orphan.Variants, err = decodeVariants(variants); err != nil {
			return nil, fmt.Errorf("media %d: %w", orphan.MediaID, err)
		}
		orphans = append(orphans, orphan)
	}
	return orphans, rows.Err()
}

// DeleteRendition removes one rendition row. Callers delete its files first.
func DeleteRendition(ctx context.Context, conn *sql.DB, mediaID int64) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM media_renditions WHERE media_id = ?`, mediaID)
	return err
}

// RenditionsByMediaPath returns every usable variant set keyed by its
// original's media path. It is the whole table in one query (tens of thousands
// of short rows), which the public API holds in memory so that listing a page
// of articles costs no extra database round trip.
func RenditionsByMediaPath(ctx context.Context, conn *sql.DB) (map[string][]RenditionVariant, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT m.path, r.variants
		FROM media_renditions AS r
		JOIN media AS m ON m.id = r.media_id
		WHERE r.variants IS NOT NULL AND r.variants <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byPath := make(map[string][]RenditionVariant)
	for rows.Next() {
		var path string
		var variants sql.NullString
		if err := rows.Scan(&path, &variants); err != nil {
			return nil, err
		}
		decoded, err := decodeVariants(variants)
		if err != nil {
			// One corrupt row should not take every other image's variants out
			// of the API with it.
			continue
		}
		if len(decoded) > 0 {
			byPath[path] = decoded
		}
	}
	return byPath, rows.Err()
}

func decodeVariants(raw sql.NullString) ([]RenditionVariant, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var variants []RenditionVariant
	if err := json.Unmarshal([]byte(raw.String), &variants); err != nil {
		return nil, fmt.Errorf("decode rendition variants: %w", err)
	}
	return variants, nil
}
