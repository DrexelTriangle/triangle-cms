package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"server/internal/models"
)

// ErrNoMediaFields signals a patch that carried none of the editable fields.
var ErrNoMediaFields = errors.New("no valid fields to update")

var MediaSortByColumn = map[string]string{
	string(models.MediaSortByFileName):  "file_name",
	string(models.MediaSortByCreatedAt): "created_at",
	string(models.MediaSortByUpdatedAt): "updated_at",
	string(models.MediaSortBySizeBytes): "size_bytes",
}

var MediaColumns = []string{
	"id", "path", "file_name", "mime_type", "size_bytes",
	"width", "height", "alt_text", "caption", "in_gallery", "created_at", "updated_at",
}

// mediaMimeByExt is the extension -> MIME mapping used when indexing existing
// files. Uploads sniff the real content type from the bytes instead; indexing a
// large migrated corpus can't afford to open every file, so extension is the
// best available signal there.
var mediaMimeByExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".svg":  "image/svg+xml",
}

// wpResizedSuffix matches the "-800x600" that WordPress appends to generated
// thumbnails. Those derivatives live next to their original in the migrated
// corpus and would otherwise bury the library in near-duplicates, so the
// indexer skips them.
var wpResizedSuffix = regexp.MustCompile(`-\d+x\d+$`)

func EnsureMediaTable(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS media (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			path VARCHAR(512) NOT NULL,
			file_name VARCHAR(255) NOT NULL,
			mime_type VARCHAR(128),
			size_bytes BIGINT NOT NULL DEFAULT 0,
			width INT NULL,
			height INT NULL,
			alt_text LONGTEXT,
			caption LONGTEXT,
			in_gallery TINYINT(1) NOT NULL DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME,
			UNIQUE KEY uniq_media_path (path),
			INDEX idx_media_created_at (created_at),
			INDEX idx_media_file_name (file_name),
			INDEX idx_media_in_gallery (in_gallery, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		return err
	}

	_, err := conn.ExecContext(ctx, `
		ALTER TABLE media
		ADD COLUMN IF NOT EXISTS path VARCHAR(512) NOT NULL,
		ADD COLUMN IF NOT EXISTS file_name VARCHAR(255) NOT NULL,
		ADD COLUMN IF NOT EXISTS mime_type VARCHAR(128),
		ADD COLUMN IF NOT EXISTS size_bytes BIGINT NOT NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS width INT NULL,
		ADD COLUMN IF NOT EXISTS height INT NULL,
		ADD COLUMN IF NOT EXISTS alt_text LONGTEXT,
		ADD COLUMN IF NOT EXISTS caption LONGTEXT,
		ADD COLUMN IF NOT EXISTS in_gallery TINYINT(1) NOT NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS created_at DATETIME,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME,
		ADD INDEX IF NOT EXISTS idx_media_in_gallery (in_gallery, created_at)
	`)
	return err
}

func ScanMedia(rows *sql.Rows) (models.Media, error) {
	var (
		item      models.Media
		mimeType  sql.NullString
		width     sql.NullInt64
		height    sql.NullInt64
		altText   sql.NullString
		caption   sql.NullString
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)

	err := rows.Scan(
		&item.ID,
		&item.Path,
		&item.FileName,
		&mimeType,
		&item.SizeBytes,
		&width,
		&height,
		&altText,
		&caption,
		&item.InGallery,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return models.Media{}, err
	}

	if mimeType.Valid {
		item.MimeType = mimeType.String
	}
	if width.Valid {
		w := int(width.Int64)
		item.Width = &w
	}
	if height.Valid {
		h := int(height.Int64)
		item.Height = &h
	}
	if altText.Valid {
		item.AltText = altText.String
	}
	if caption.Valid {
		item.Caption = caption.String
	}
	if createdAt.Valid {
		t := createdAt.Time
		item.CreatedAt = &t
	}
	if updatedAt.Valid {
		t := updatedAt.Time
		item.UpdatedAt = &t
	}

	return item, nil
}

func mediaConditions(params models.MediaListParams) ([]string, []any) {
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 3)

	if query := strings.TrimSpace(params.Query); query != "" {
		like := "%" + query + "%"
		conditions = append(conditions, "(`file_name` LIKE ? OR `alt_text` LIKE ? OR `caption` LIKE ?)")
		args = append(args, like, like, like)
	}

	// "image/" (a trailing slash) filters by family; anything else is exact.
	if mimeType := strings.TrimSpace(params.MimeType); mimeType != "" {
		if strings.HasSuffix(mimeType, "/") {
			conditions = append(conditions, "`mime_type` LIKE ?")
			args = append(args, mimeType+"%")
		} else {
			conditions = append(conditions, "`mime_type` = ?")
			args = append(args, mimeType)
		}
	}

	if params.InGallery != nil {
		conditions = append(conditions, "`in_gallery` = ?")
		args = append(args, *params.InGallery)
	}

	return conditions, args
}

// ListMedia returns one page of the library plus the total matching count.
func ListMedia(ctx context.Context, conn *sql.DB, params models.MediaListParams) ([]models.Media, int, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	conditions, args := mediaConditions(params)
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	var totalCount int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM `media`"+whereClause, args...).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("count media: %w", err)
	}

	query := "SELECT `" + strings.Join(MediaColumns, "`, `") + "` FROM `media`" + whereClause
	sortBy := string(params.SortBy)
	if _, ok := MediaSortByColumn[sortBy]; !ok {
		// Newest first, with id as the tiebreaker: a bulk index stamps many rows
		// with the same created_at, so created_at alone is not a stable order.
		sortBy = ""
		query += " ORDER BY `created_at` DESC, `id` DESC"
	}
	query = BuildOrderLimit(query, sortBy, string(params.SortDirection), MediaSortByColumn, limit, offset)

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list media: %w", err)
	}
	defer rows.Close()

	items := make([]models.Media, 0)
	for rows.Next() {
		item, err := ScanMedia(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return items, totalCount, nil
}

func GetMediaByID(ctx context.Context, conn *sql.DB, id int64) (models.Media, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT `"+strings.Join(MediaColumns, "`, `")+"` FROM `media` WHERE `id` = ?", id)
	if err != nil {
		return models.Media{}, fmt.Errorf("get media: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return models.Media{}, err
		}
		return models.Media{}, sql.ErrNoRows
	}
	item, err := ScanMedia(rows)
	if err != nil {
		return models.Media{}, err
	}
	if err := rows.Err(); err != nil {
		return models.Media{}, err
	}
	return item, nil
}

// InsertMedia records a stored file in the library. Width and height are ignored
// when zero (unknown), which is what indexing reports for formats whose headers
// were not decoded.
func InsertMedia(ctx context.Context, conn *sql.DB, relPath, mimeType string, sizeBytes int64, width, height int) (int64, error) {
	now := time.Now().UTC()
	result, err := conn.ExecContext(ctx, `
		INSERT INTO media (path, file_name, mime_type, size_bytes, width, height, created_at, updated_at)
		VALUES (?, ?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), ?, ?)
	`, relPath, path.Base(relPath), mimeType, sizeBytes, width, height, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert media: %w", err)
	}
	return result.LastInsertId()
}

// UpdateMediaMeta applies the editable fields of a library row. Only the fields
// present on the patch are written; a patch with none set is a no-op error so
// the handler can answer 400 rather than silently succeeding.
func UpdateMediaMeta(ctx context.Context, conn *sql.DB, id int64, patch models.MediaPatch) (models.Media, error) {
	setClauses := make([]string, 0, 5)
	args := make([]any, 0, 6)

	if patch.FileName != nil {
		setClauses = append(setClauses, "`file_name` = ?")
		args = append(args, strings.TrimSpace(*patch.FileName))
	}
	if patch.AltText != nil {
		setClauses = append(setClauses, "`alt_text` = ?")
		args = append(args, *patch.AltText)
	}
	if patch.Caption != nil {
		setClauses = append(setClauses, "`caption` = ?")
		args = append(args, *patch.Caption)
	}
	if patch.InGallery != nil {
		setClauses = append(setClauses, "`in_gallery` = ?")
		args = append(args, *patch.InGallery)
	}
	if len(setClauses) == 0 {
		return models.Media{}, ErrNoMediaFields
	}

	setClauses = append(setClauses, "`updated_at` = ?")
	args = append(args, time.Now().UTC(), id)

	result, err := conn.ExecContext(ctx,
		"UPDATE `media` SET "+strings.Join(setClauses, ", ")+" WHERE `id` = ?", args...)
	if err != nil {
		return models.Media{}, fmt.Errorf("update media: %w", err)
	}
	if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
		// Either the row is gone or the values were already identical; the
		// follow-up read distinguishes the two.
		if _, err := GetMediaByID(ctx, conn, id); err != nil {
			return models.Media{}, err
		}
	}

	return GetMediaByID(ctx, conn, id)
}

// DeleteMediaRow removes the library entry only. Callers that also want the file
// gone are responsible for unlinking it; the row is dropped first so a failed
// unlink leaves an orphan file (which reindexing can recover) rather than a row
// pointing at nothing.
func DeleteMediaRow(ctx context.Context, conn *sql.DB, id int64) error {
	result, err := conn.ExecContext(ctx, "DELETE FROM `media` WHERE `id` = ?", id)
	if err != nil {
		return fmt.Errorf("delete media: %w", err)
	}
	if rowsAffected, err := result.RowsAffected(); err == nil && rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// likeEscaper neutralises the LIKE metacharacters in a value being matched
// literally. Underscore is the one that bites here: the migrated corpus is full
// of names like IMG_1234.jpg, and an unescaped "_" matches any single character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// MediaPathInUse reports whether any article still points at this asset, so the
// handler can refuse a delete that would break a published page.
//
// Two places have to be checked. photo_url is the article's lead image and holds
// an absolute URL from the ETL, hence the suffix match. Body images are embedded
// by the editor as <img src> inside the article text, and checking only
// photo_url meant an asset used solely inline looked unreferenced, so the delete
// went through and unlinked a file a published article was still rendering.
func MediaPathInUse(ctx context.Context, conn *sql.DB, relPath string) (bool, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return false, nil
	}
	escaped := likeEscaper.Replace(relPath)

	var exists int
	err := conn.QueryRowContext(ctx, `
		SELECT 1 FROM `+"`articles`"+`
		WHERE `+"`photo_url`"+` LIKE ? ESCAPE '\\'
		   OR `+"`text`"+` LIKE ? ESCAPE '\\'
		LIMIT 1
	`, "%"+escaped, "%"+escaped+"%").Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check media usage: %w", err)
}

// progressInterval is how many filesystem entries the indexer walks between
// progress callbacks. The corpus is mostly WordPress derivatives that are
// skipped without a stat, so reporting per *indexed* file would appear frozen
// for long stretches; reporting per entry walked keeps the count moving.
const progressInterval = 500

// IndexMediaRoot walks the uploads tree under root and inserts a library row for
// every asset not already recorded, so the migrated CephFS corpus shows up
// without a separate migration step. It is idempotent: existing paths are
// counted as skipped and left untouched, preserving any alt text already set.
func IndexMediaRoot(ctx context.Context, conn *sql.DB, root, uploadsSubdir string) (models.MediaIndexResponse, error) {
	return IndexMediaRootWithProgress(ctx, conn, root, uploadsSubdir, nil)
}

// IndexMediaRootWithProgress is IndexMediaRoot with periodic progress reporting.
// onProgress (may be nil) is called from the walking goroutine every
// progressInterval entries and once at the end, so it must not block.
func IndexMediaRootWithProgress(
	ctx context.Context,
	conn *sql.DB,
	root, uploadsSubdir string,
	onProgress func(models.MediaIndexResponse),
) (models.MediaIndexResponse, error) {
	var report models.MediaIndexResponse

	known, err := knownMediaPaths(ctx, conn)
	if err != nil {
		return report, err
	}

	report.Walked = 0
	notify := func() {
		if onProgress != nil {
			onProgress(report)
		}
	}

	uploadsDir := filepath.Join(root, filepath.FromSlash(uploadsSubdir))
	walkErr := filepath.WalkDir(uploadsDir, func(absPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree shouldn't abort the whole index; skip it.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		report.Walked++
		if report.Walked%progressInterval == 0 {
			notify()
		}
		// Skip dotfiles, before the directory check so hidden subtrees are pruned
		// too. The upload path writes its temp file as ".upload-*<ext>" in the
		// destination directory, so a container kill mid-copy leaves a truncated
		// image with a real extension sitting next to the finished ones. Adopting
		// those would fill the library with entries that can never load (Nginx
		// refuses to serve dotfiles) and whose bytes are partial anyway.
		name := entry.Name()
		if strings.HasPrefix(name, ".") && absPath != uploadsDir {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		mimeType, ok := mediaMimeByExt[ext]
		if !ok {
			return nil
		}
		if wpResizedSuffix.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) {
			return nil
		}

		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return nil
		}
		relPath := filepath.ToSlash(rel)

		report.Scanned++
		if _, seen := known[relPath]; seen {
			report.Skipped++
			return nil
		}

		var sizeBytes int64
		if info, err := entry.Info(); err == nil {
			sizeBytes = info.Size()
		}
		if _, err := InsertMedia(ctx, conn, relPath, mimeType, sizeBytes, 0, 0); err != nil {
			// A concurrent upload may have claimed the same path; the unique key
			// makes that a duplicate-key error, which is a skip, not a failure.
			report.Skipped++
			return nil
		}
		known[relPath] = struct{}{}
		report.Added++
		return nil
	})
	if walkErr != nil {
		if os.IsNotExist(walkErr) {
			notify()
			return report, nil
		}
		return report, fmt.Errorf("index media: %w", walkErr)
	}

	// Always report the final tally: a tree smaller than progressInterval would
	// otherwise never fire the callback at all.
	notify()
	return report, nil
}

func knownMediaPaths(ctx context.Context, conn *sql.DB) (map[string]struct{}, error) {
	rows, err := conn.QueryContext(ctx, "SELECT `path` FROM `media`")
	if err != nil {
		return nil, fmt.Errorf("load indexed media paths: %w", err)
	}
	defer rows.Close()

	known := make(map[string]struct{})
	for rows.Next() {
		var relPath string
		if err := rows.Scan(&relPath); err != nil {
			return nil, err
		}
		known[relPath] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return known, nil
}
