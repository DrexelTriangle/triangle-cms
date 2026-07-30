package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	// Register decoders so image.DecodeConfig can report dimensions for the
	// common legacy formats. WebP/AVIF simply report no dimensions (best effort).
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

const (
	mediaFormField = "file"
	// defaultMaxUploadBytes caps a single upload when MEDIA_MAX_UPLOAD_BYTES is unset.
	defaultMaxUploadBytes int64 = 25 << 20 // 25 MiB
	// uploadsSubdir is the WordPress-compatible prefix (under MEDIA_ROOT) that all
	// uploads live under, so new files line up with the rsynced legacy corpus and
	// the Nginx `location /wp-content/` root that serves them.
	uploadsSubdir  = "wp-content/uploads"
	maxBaseNameLen = 100
	// mediaIndexTimeout bounds a background index run so a wedged filesystem
	// cannot leave the job permanently "running" and block every later attempt.
	mediaIndexTimeout = 2 * time.Hour
)

// allowedImageTypes maps a sniffed content type to its canonical extension. Only
// these types are accepted; the extension is derived from the sniffed bytes, not
// the client-supplied filename.
var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

func mediaRoot() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("MEDIA_ROOT")), "/")
}

func maxUploadBytes() int64 {
	if v := strings.TrimSpace(os.Getenv("MEDIA_MAX_UPLOAD_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxUploadBytes
}

// uploadURL renders the canonical wp-content-relative path of a just-stored file
// as a servable URL. MEDIA_BASE_URL should match what the WordPress ETL uses so
// uploaded and migrated images share one URL scheme; when unset it falls back to
// a root-relative path (served from the same origin as the media mount).
func uploadURL(relPath string) string {
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("MEDIA_BASE_URL")), "/"); base != "" {
		return base + "/" + relPath
	}
	return "/" + relPath
}

// PostMedia stores an uploaded image on the media filesystem (the Ceph-backed
// MEDIA_ROOT) under a WordPress-compatible wp-content/uploads/YYYY/MM/ path,
// records it in the media library, and returns its canonical path plus display
// URL.
//
// @Summary Upload a media asset
// @Tags media
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Image file"
// @Success 201 {object} models.MediaUploadResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Failure 415 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Failure 501 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media [post]
func PostMedia(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root := mediaRoot()
		if root == "" {
			writeError(w, http.StatusNotImplemented, "media storage is not configured")
			return
		}

		maxBytes := maxUploadBytes()
		// Cap the request body. A little slack over maxBytes covers multipart
		// framing so a file exactly at the limit still succeeds.
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+4096)
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("file exceeds maximum upload size of %d bytes", maxBytes))
				return
			}
			writeError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()

		file, header, err := r.FormFile(mediaFormField)
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' form field")
			return
		}
		defer file.Close()

		// Sniff the real content type from the leading bytes; never trust the
		// client-provided extension or Content-Type.
		head := make([]byte, 512)
		n, err := io.ReadFull(file, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusInternalServerError, "failed to read upload")
			return
		}
		contentType := http.DetectContentType(head[:n])
		ext, ok := allowedImageTypes[contentType]
		if !ok {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported file type: "+contentType)
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to rewind upload")
			return
		}

		now := time.Now().UTC()
		relDir := path.Join(uploadsSubdir, now.Format("2006"), now.Format("01"))
		absDir := filepath.Join(root, filepath.FromSlash(relDir))
		if err := os.MkdirAll(absDir, 0o775); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create upload directory")
			return
		}

		base := sanitizeBaseName(header.Filename)
		name, written, err := storeUpload(absDir, base, ext, file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store upload")
			return
		}

		relPath := path.Join(relDir, name)
		absPath := filepath.Join(absDir, name)
		width, height := imageDimensions(absPath)

		// The file is already durable at this point. If recording it fails the
		// upload is still reported as a failure, but the file is left in place:
		// a reindex will adopt it rather than leaving a silent orphan.
		id, err := db.InsertMedia(r.Context(), conn, relPath, contentType, written, width, height)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "file stored but could not be added to the media library")
			return
		}

		activity.LogRequest(r, "media_uploaded", name, "path", relPath)

		writeJSON(w, http.StatusCreated, models.MediaUploadResponse{
			ID:          id,
			Path:        relPath,
			URL:         uploadURL(relPath),
			ContentType: contentType,
			Size:        written,
			Width:       width,
			Height:      height,
		})
	})
}

// sanitizeBaseName reduces a client filename to a safe, slugified base (no
// directory components, no extension). This is the only use of the client name,
// so path traversal is impossible regardless of input.
func sanitizeBaseName(filename string) string {
	base := filename
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToLower(base)
	base = nonSlugChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if len(base) > maxBaseNameLen {
		base = strings.Trim(base[:maxBaseNameLen], "-")
	}
	if base == "" {
		base = "file"
	}
	return base
}

// storeUpload writes src to a uniquely-named file in dir, never overwriting an
// existing asset (important: the legacy corpus lives here too). It writes to a
// temp file first and atomically renames into place so partially-written files
// are never visible to the serving layer.
func storeUpload(dir, base, ext string, src io.Reader) (name string, size int64, err error) {
	tmp, err := os.CreateTemp(dir, ".upload-*"+ext)
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if size, err = io.Copy(tmp, src); err != nil {
		return "", 0, err
	}
	if err = tmp.Sync(); err != nil {
		return "", 0, err
	}
	if err = tmp.Close(); err != nil {
		return "", 0, err
	}

	name, err = reserveAndRename(dir, base, ext, tmpPath)
	if err != nil {
		return "", 0, err
	}
	cleanup = false // renamed away
	return name, size, nil
}

// reserveAndRename finds an unused "<base><ext>" / "<base>-N<ext>" name in dir by
// exclusively creating it, then renames tmpPath onto it. The O_EXCL create is the
// atomic no-clobber guarantee even under concurrent uploads.
func reserveAndRename(dir, base, ext, tmpPath string) (string, error) {
	reserve := func(name string) (bool, error) {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_ = f.Close()
			return true, nil
		}
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}

	tryNames := func() (string, error) {
		if ok, err := reserve(base + ext); err != nil {
			return "", err
		} else if ok {
			return base + ext, nil
		}
		for i := 1; i < 1000; i++ {
			name := fmt.Sprintf("%s-%d%s", base, i, ext)
			if ok, err := reserve(name); err != nil {
				return "", err
			} else if ok {
				return name, nil
			}
		}
		suffix, err := randomHex(6)
		if err != nil {
			return "", err
		}
		name := base + "-" + suffix + ext
		if ok, err := reserve(name); err != nil {
			return "", err
		} else if !ok {
			return "", errors.New("could not allocate a unique filename")
		}
		return name, nil
	}

	name, err := tryNames()
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(filepath.Join(dir, name))
		return "", err
	}
	return name, nil
}

func imageDimensions(p string) (int, int) {
	f, err := os.Open(p)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// withMediaURL renders the stored path as a display URL. The path is the stable
// identity in the DB; the URL depends on MEDIA_BASE_URL and so is computed per
// response rather than persisted.
func withMediaURL(item models.Media) models.Media {
	item.URL = uploadURL(item.Path)
	return item
}

func mediaListParams(r *http.Request) models.MediaListParams {
	q := r.URL.Query()
	_, limit, offset := listParams(r, 50)
	return models.MediaListParams{
		Limit:         limit,
		Offset:        offset,
		Query:         strings.TrimSpace(q.Get("search")),
		MimeType:      strings.TrimSpace(q.Get("mime_type")),
		SortBy:        models.MediaSortBy(q.Get("sort_by")),
		SortDirection: models.SortDirection(q.Get("sort_direction")),
	}
}

// boolParam reads a flag-style query parameter, where a bare "?keep_file" counts
// as true (matching how the archived filter is read elsewhere).
func boolParam(r *http.Request, key string) bool {
	q := r.URL.Query()
	if _, provided := q[key]; !provided {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(q.Get(key))) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

func mediaIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// GetMedia lists the media library.
//
// @Summary List media
// @Tags media
// @Produce json
// @Param page query int false "Page number"
// @Param limit query int false "Max results" default(50)
// @Param offset query int false "Offset"
// @Param search query string false "Filter by file name, alt text, or caption"
// @Param mime_type query string false "Filter by MIME type; a trailing slash (image/) matches the family"
// @Param sort_by query string false "Sort field" Enums(file_name,created_at,updated_at,size_bytes)
// @Param sort_direction query string false "Sort direction" Enums(asc,desc)
// @Success 200 {object} models.MediaResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media [get]
func GetMedia(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := mediaListParams(r)
		items, totalCount, err := db.ListMedia(r.Context(), conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		for i := range items {
			items[i] = withMediaURL(items[i])
		}

		page := (params.Offset / params.Limit) + 1
		hasMore := params.Offset+len(items) < totalCount
		writeJSON(w, http.StatusOK, models.MediaResponse{
			Media:      items,
			Pagination: paginationResponse(page, params.Limit, params.Offset, hasMore, totalCount),
		})
	})
}

// GetMediaGallery returns the trimmed shape used by image pickers.
//
// @Summary List media for a picker
// @Tags media
// @Produce json
// @Param limit query int false "Max results" default(50)
// @Param offset query int false "Offset"
// @Param search query string false "Filter by file name, alt text, or caption"
// @Success 200 {object} models.MediaGalleryResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/gallery [get]
func GetMediaGallery(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := mediaListParams(r)
		items, _, err := db.ListMedia(r.Context(), conn, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		gallery := make([]models.MediaOverview, 0, len(items))
		for _, item := range items {
			gallery = append(gallery, models.MediaOverview{
				ID:        item.ID,
				Path:      item.Path,
				FileName:  item.FileName,
				URL:       uploadURL(item.Path),
				MimeType:  item.MimeType,
				SizeBytes: item.SizeBytes,
				Width:     item.Width,
				Height:    item.Height,
				AltText:   item.AltText,
			})
		}

		writeJSON(w, http.StatusOK, models.MediaGalleryResponse{Media: gallery})
	})
}

// GetMediaItem returns a single library entry.
//
// @Summary Get a media asset
// @Tags media
// @Produce json
// @Param id path int true "Media ID"
// @Success 200 {object} models.Media
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/{id} [get]
func GetMediaItem(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := mediaIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		item, err := db.GetMediaByID(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, withMediaURL(item))
	})
}

// PatchMediaItem edits the descriptive fields of a library entry. The stored
// file is never touched: file_name here is the library's display name, not a
// rename on disk, because the on-disk path is what articles already reference.
//
// @Summary Update media metadata
// @Tags media
// @Accept json
// @Produce json
// @Param id path int true "Media ID"
// @Param body body models.MediaPatch true "Fields to update"
// @Success 200 {object} models.Media
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/{id} [patch]
func PatchMediaItem(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := mediaIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		var patch models.MediaPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if patch.FileName != nil && strings.TrimSpace(*patch.FileName) == "" {
			writeError(w, http.StatusBadRequest, "file_name cannot be empty")
			return
		}

		item, err := db.UpdateMediaMeta(r.Context(), conn, id, patch)
		if errors.Is(err, db.ErrNoMediaFields) {
			writeError(w, http.StatusBadRequest, "no valid fields to update")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		activity.LogRequest(r, "media_updated", item.FileName, "path", item.Path)
		writeJSON(w, http.StatusOK, withMediaURL(item))
	})
}

// DeleteMediaItem removes a library entry and, unless keep_file is set, the
// underlying file. An asset still referenced by an article is refused so a
// delete here can't blank out a published page's image.
//
// @Summary Delete a media asset
// @Tags media
// @Param id path int true "Media ID"
// @Param keep_file query bool false "Remove the library entry but leave the file on disk"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/{id} [delete]
func DeleteMediaItem(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := mediaIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "id must be a positive integer")
			return
		}

		item, err := db.GetMediaByID(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		inUse, err := db.MediaPathInUse(r.Context(), conn, item.Path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if inUse {
			writeError(w, http.StatusConflict, "media is still used as an article image")
			return
		}

		if err := db.DeleteMediaRow(r.Context(), conn, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "media not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if !boolParam(r, "keep_file") {
			if root := mediaRoot(); root != "" {
				if abs, ok := resolveMediaPath(root, item.Path); ok {
					_ = os.Remove(abs)
				}
			}
		}

		activity.LogRequest(r, "media_deleted", item.FileName, "path", item.Path)
		w.WriteHeader(http.StatusNoContent)
	})
}

// mediaIndexJob tracks the single in-flight index run for this process.
//
// The walk takes minutes on the real corpus (~145k filesystem entries, most of
// them WordPress derivatives that are skipped), which is far longer than the
// proxies in front of this service allow a request to hang: Nginx cuts an
// upstream read at 60s by default and Cloudflare at ~100s. Running it inside the
// request meant the client disconnect cancelled the walk, so a full index could
// never complete over HTTP. It now runs in the background on a context that
// outlives the request, and callers poll for progress.
type mediaIndexJob struct {
	mu         sync.Mutex
	running    bool
	startedAt  time.Time
	finishedAt time.Time
	progress   models.MediaIndexResponse
	err        string
}

// tryStart claims the job. It returns false when a run is already in flight, so
// an impatient second click extends nothing and corrupts nothing.
func (j *mediaIndexJob) tryStart() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running {
		return false
	}
	j.running = true
	j.startedAt = time.Now().UTC()
	j.finishedAt = time.Time{}
	j.progress = models.MediaIndexResponse{}
	j.err = ""
	return true
}

func (j *mediaIndexJob) setProgress(p models.MediaIndexResponse) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress = p
}

func (j *mediaIndexJob) finish(p models.MediaIndexResponse, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.running = false
	j.finishedAt = time.Now().UTC()
	j.progress = p
	if err != nil {
		j.err = err.Error()
	}
}

func (j *mediaIndexJob) status() models.MediaIndexStatusResponse {
	j.mu.Lock()
	defer j.mu.Unlock()

	status := models.MediaIndexStatusResponse{
		Running:  j.running,
		Progress: j.progress,
		Error:    j.err,
	}
	if !j.startedAt.IsZero() {
		started := j.startedAt
		status.StartedAt = &started
	}
	if !j.finishedAt.IsZero() {
		finished := j.finishedAt
		status.FinishedAt = &finished
	}
	return status
}

// One job per process. Blue/green means two backends could in principle index at
// once, but only one slot serves traffic, and the unique key on media.path makes
// a concurrent run wasteful rather than harmful (duplicates count as skips).
var mediaIndexer = &mediaIndexJob{}

// PostMediaIndex starts a background walk of the media filesystem, adding any
// asset missing from the library. This is how the migrated CephFS corpus enters
// the library, and it is safe to re-run: existing entries keep whatever alt text
// they already have.
//
// It returns 202 immediately; poll GET /v1/media/index for progress.
//
// @Summary Start a media filesystem reindex
// @Tags media
// @Produce json
// @Success 202 {object} models.MediaIndexStatusResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 501 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/index [post]
func PostMediaIndex(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root := mediaRoot()
		if root == "" {
			writeError(w, http.StatusNotImplemented, "media storage is not configured")
			return
		}

		if !mediaIndexer.tryStart() {
			writeError(w, http.StatusConflict, "a media index is already running")
			return
		}

		activity.LogRequest(r, "media_index_started", "Media library reindex started")

		// Deliberately NOT r.Context(): the request returns immediately, so the
		// walk must not be cancelled when the client disconnects.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mediaIndexTimeout)
			defer cancel()

			report, err := db.IndexMediaRootWithProgress(ctx, conn, root, uploadsSubdir, mediaIndexer.setProgress)
			mediaIndexer.finish(report, err)
			if err != nil {
				slog.Error("media index failed", "error", err,
					"walked", report.Walked, "added", report.Added)
				return
			}
			slog.Info("media index complete",
				"walked", report.Walked, "scanned", report.Scanned,
				"added", report.Added, "skipped", report.Skipped)
		}()

		writeJSON(w, http.StatusAccepted, mediaIndexer.status())
	})
}

// GetMediaIndexStatus reports the progress of the current or most recent index.
//
// @Summary Media reindex status
// @Tags media
// @Produce json
// @Success 200 {object} models.MediaIndexStatusResponse
// @Security BearerAuth
// @Router /v1/media/index [get]
func GetMediaIndexStatus() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, mediaIndexer.status())
	})
}

// resolveMediaPath maps a stored relative path to an absolute one, refusing
// anything that escapes the media root. Stored paths are generated server-side,
// so this is defence in depth against a malformed or hand-edited row.
func resolveMediaPath(root, relPath string) (string, bool) {
	cleaned := path.Clean("/" + strings.TrimSpace(relPath))
	if cleaned == "/" {
		return "", false
	}
	abs := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	rootPrefix := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(abs, rootPrefix) {
		return "", false
	}
	return abs, true
}
