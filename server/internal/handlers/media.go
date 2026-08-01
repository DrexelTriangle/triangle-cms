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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/models"
)

const (
	mediaFormField = "file"
	// defaultMaxUploadBytes caps a single upload when MEDIA_MAX_UPLOAD_BYTES is
	// unset. The migrated WordPress corpus contains full-resolution camera
	// originals up to ~77 MiB, so anything smaller than this rejects files the
	// newsroom demonstrably produces. The ceiling is Cloudflare's 100 MB request
	// limit on the tunnel in front of Delta -- stay below it, since a body that
	// clears the backend but not the tunnel fails with an opaque edge error.
	defaultMaxUploadBytes int64 = 90 << 20 // 90 MiB
	// multipartMemoryBytes is how much of a multipart body ParseMultipartForm may
	// hold in RAM before spilling the rest to temp files. It is deliberately
	// decoupled from the size limit: passing the limit itself would let a single
	// upload pin its full size in memory (and Go adds ~10 MiB of slack on top).
	multipartMemoryBytes int64 = 8 << 20 // 8 MiB
	// uploadsSubdir is the WordPress-compatible prefix (under MEDIA_ROOT) that all
	// uploads live under, so new files line up with the rsynced legacy corpus and
	// the Nginx `location /wp-content/` root that serves them.
	uploadsSubdir  = "wp-content/uploads"
	maxBaseNameLen = 100
	// mediaIndexTimeout bounds a background index run so a wedged filesystem
	// cannot leave the job permanently "running" and block every later attempt.
	mediaIndexTimeout = 2 * time.Hour
	// remoteFetchTimeout bounds a whole sideload (see PostMediaFetch). Pasting
	// blocks the editor's attachment on this call, so it has to fail fast rather
	// than inherit the server's much longer default.
	remoteFetchTimeout = 30 * time.Second
	// maxRemoteRedirects caps a sideload's redirect chain. Every hop is
	// re-validated by safeDialControl, so this only bounds the work, not the risk.
	maxRemoteRedirects = 5
)

// Sentinel failures from storeImage, mapped onto status codes by
// writeStoreImageError. The distinction matters to callers: an unsupported type
// is the client's fault, whereas an index failure means the bytes are already
// durable on disk and only the library row is missing.
var (
	errMediaNotConfigured = errors.New("media storage is not configured")
	errMediaUnavailable   = errors.New("media storage is unavailable")
	errUnsupportedType    = errors.New("unsupported file type")
	errStoreFailed        = errors.New("failed to store upload")
	errIndexFailed        = errors.New("file stored but could not be added to the media library")
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

// wpDerivativeSuffix matches the "-800x600" WordPress appends to generated
// thumbnails. The indexer skips names of this shape (they are derivatives of an
// original that is already in the library), so an *upload* must never be given
// one: its row exists now, but a rebuild of the media table -- which the reseed
// plan calls for -- would not adopt the file back while articles still point at
// it. sanitizeBaseName defuses the shape instead.
var wpDerivativeSuffix = regexp.MustCompile(`-\d+x\d+$`)

func mediaRoot() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("MEDIA_ROOT")), "/")
}

// mediaSentinel is a MEDIA_ROOT-relative path that must exist for the media tree
// to be considered mounted. Empty (the default) disables the check.
func mediaSentinel() string {
	return strings.Trim(strings.TrimSpace(os.Getenv("MEDIA_ROOT_SENTINEL")), "/")
}

// CheckMediaStorage reports whether MEDIA_ROOT looks like the real media tree
// rather than an empty directory standing in for an unmounted filesystem.
//
// This matters because the Docker bind mount silently creates MEDIA_HOST_PATH if
// CephFS is not mounted at deploy time. Without this check uploads land on the
// host's local disk and index cleanly, and are then shadowed -- effectively lost
// -- the moment the mount is repaired. MEDIA_ROOT_SENTINEL names something the
// migrated corpus is known to contain (the uploads directory itself is the
// natural choice); a fresh install with no corpus should leave it unset.
func CheckMediaStorage() error {
	root := mediaRoot()
	if root == "" {
		return errMediaNotConfigured
	}
	sentinel := mediaSentinel()
	if sentinel == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(sentinel))); err != nil {
		return fmt.Errorf("%w: sentinel %q missing under MEDIA_ROOT %s (is the media filesystem mounted?)",
			errMediaUnavailable, sentinel, root)
	}
	return nil
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
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media [post]
func PostMedia(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := CheckMediaStorage(); err != nil {
			// Checked before reading the body: there is no point streaming 90 MiB
			// into a directory we are about to refuse to write to.
			writeStoreImageError(w, err)
			return
		}

		maxBytes := maxUploadBytes()
		// Cap the request body. A little slack over maxBytes covers multipart
		// framing so a file exactly at the limit still succeeds.
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+4096)
		// MaxBytesReader, not this argument, is what enforces the limit; this only
		// decides where the bytes land on the way in (see multipartMemoryBytes).
		if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
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

		stored, err := storeImage(r.Context(), conn, file, header.Filename)
		if err != nil {
			writeStoreImageError(w, err)
			return
		}

		activity.LogRequest(r, "media_uploaded", path.Base(stored.Path), "path", stored.Path)
		writeJSON(w, http.StatusCreated, stored)
	})
}

// storeImage validates, stores and indexes a single image, and is the one place
// bytes become a media library entry -- shared by the multipart upload path and
// the paste sideload path so both agree on what is accepted and where it lands.
//
// src must be seekable: sniffing the content type consumes the leading bytes,
// which have to be rewound before the file is written.
func storeImage(ctx context.Context, conn *sql.DB, src io.ReadSeeker, filename string) (models.MediaUploadResponse, error) {
	if err := CheckMediaStorage(); err != nil {
		return models.MediaUploadResponse{}, err
	}
	root := mediaRoot()

	// Sniff the real content type from the leading bytes; never trust the
	// client-provided extension or Content-Type.
	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: read: %v", errStoreFailed, err)
	}
	contentType := http.DetectContentType(head[:n])
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: %s", errUnsupportedType, contentType)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: rewind: %v", errStoreFailed, err)
	}

	now := time.Now().UTC()
	relDir := path.Join(uploadsSubdir, now.Format("2006"), now.Format("01"))
	absDir := filepath.Join(root, filepath.FromSlash(relDir))
	if err := os.MkdirAll(absDir, 0o775); err != nil {
		slog.Error("media upload: create directory", "dir", absDir, "error", err)
		return models.MediaUploadResponse{}, fmt.Errorf("%w: create directory: %v", errStoreFailed, err)
	}
	// MkdirAll's mode is masked by the process umask, so the first upload of a
	// new month can leave YYYY/ or YYYY/MM without the world-execute bit the
	// Nginx worker needs to traverse into it -- a 403 indistinguishable from the
	// file-mode one. Best effort: a directory the ETL's rsync already created is
	// owned by another uid and cannot be chmod'ed by us, which is fine because
	// that one is already correct.
	ensureTraversable(filepath.Dir(absDir), absDir)

	name, written, err := storeUpload(absDir, sanitizeBaseName(filename), ext, src)
	if err != nil {
		// Worth a log line rather than just a 500: the message the client
		// gets cannot say whether the media volume is full, unmounted, or
		// simply not writable by this container's uid, and those need very
		// different fixes. A permission error here is easy to mistake for a
		// mkdir failure, since MkdirAll returns nil for a directory that
		// already exists -- which every migrated YYYY/MM directory does.
		slog.Error("media upload: store file", "dir", absDir, "error", err)
		return models.MediaUploadResponse{}, fmt.Errorf("%w: %v", errStoreFailed, err)
	}

	relPath := path.Join(relDir, name)
	width, height := imageDimensions(filepath.Join(absDir, name))

	// The file is already durable at this point. If recording it fails the
	// upload is still reported as a failure, but the file is left in place:
	// a reindex will adopt it rather than leaving a silent orphan.
	id, err := db.InsertMedia(ctx, conn, relPath, contentType, written, width, height)
	if err != nil {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: %v", errIndexFailed, err)
	}

	return models.MediaUploadResponse{
		ID:          id,
		Path:        relPath,
		URL:         uploadURL(relPath),
		ContentType: contentType,
		Size:        written,
		Width:       width,
		Height:      height,
	}, nil
}

// writeStoreImageError maps a storeImage failure onto a response. Only the
// sentinel text is echoed to the client; the wrapped detail stays in the log,
// since it can name filesystem paths.
func writeStoreImageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMediaNotConfigured):
		writeError(w, http.StatusNotImplemented, errMediaNotConfigured.Error())
	case errors.Is(err, errMediaUnavailable):
		// 503, not 500: the bytes were never touched and the request is worth
		// retrying once the mount is back. The detail names a filesystem path,
		// so it stays in the log.
		slog.Error("media storage unavailable", "error", err)
		writeError(w, http.StatusServiceUnavailable, errMediaUnavailable.Error())
	case errors.Is(err, errUnsupportedType):
		// Safe to echo in full: the only variable part is a sniffed MIME type.
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, errIndexFailed):
		writeError(w, http.StatusInternalServerError, errIndexFailed.Error())
	default:
		writeError(w, http.StatusInternalServerError, errStoreFailed.Error())
	}
}

// PostMediaFetch copies an image that lives on someone else's server into the
// media library and returns it in the same shape as an upload.
//
// This is what makes pasting work. When an author pastes rich text from another
// site the clipboard carries <img> tags pointing at that site, and embedding
// them as-is would leave the published article hotlinking a third party: it
// breaks when they move the file, leaks our readers' referrers, and puts content
// we cannot vouch for on the page. Sideloading takes a copy up front instead.
//
// @Summary Sideload a remote image into the library
// @Tags media
// @Accept json
// @Produce json
// @Param request body models.MediaFetchRequest true "Remote image URL"
// @Success 201 {object} models.MediaUploadResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 413 {object} models.ErrorResponse
// @Failure 415 {object} models.ErrorResponse
// @Failure 502 {object} models.ErrorResponse
// @Failure 501 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/fetch [post]
func PostMediaFetch(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := CheckMediaStorage(); err != nil {
			writeStoreImageError(w, err)
			return
		}

		var body models.MediaFetchRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		target, err := url.Parse(strings.TrimSpace(body.URL))
		if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
			writeError(w, http.StatusBadRequest, "url must be an absolute http(s) URL")
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
		if err != nil {
			writeError(w, http.StatusBadRequest, "url could not be requested")
			return
		}
		req.Header.Set("Accept", "image/*")

		resp, err := remoteImageClient().Do(req)
		if err != nil {
			// The reason (blocked address, DNS failure, timeout) goes to the log
			// rather than the response: echoing it back would turn this endpoint
			// into a probe that reports what the server can reach.
			slog.Warn("media sideload: fetch failed", "url", target.String(), "error", err)
			writeError(w, http.StatusBadGateway, "could not fetch the image from that URL")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			slog.Warn("media sideload: unexpected status", "url", target.String(), "status", resp.StatusCode)
			writeError(w, http.StatusBadGateway, fmt.Sprintf("remote server returned %d", resp.StatusCode))
			return
		}

		// Buffer to a temp file: storeImage has to seek back after sniffing and a
		// response body cannot. Reading one byte past the cap is what detects an
		// oversized image -- Content-Length is supplied by the remote server and
		// so cannot be trusted as the guard.
		maxBytes := maxUploadBytes()
		tmp, err := os.CreateTemp("", "sideload-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, errStoreFailed.Error())
			return
		}
		defer func() {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}()

		written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			slog.Warn("media sideload: read failed", "url", target.String(), "error", err)
			writeError(w, http.StatusBadGateway, "could not read the image from that URL")
			return
		}
		if written > maxBytes {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("image exceeds maximum size of %d bytes", maxBytes))
			return
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, errStoreFailed.Error())
			return
		}

		stored, err := storeImage(r.Context(), conn, tmp, remoteFileName(target))
		if err != nil {
			writeStoreImageError(w, err)
			return
		}

		activity.LogRequest(r, "media_sideloaded", path.Base(stored.Path),
			"path", stored.Path, "source", target.String())
		writeJSON(w, http.StatusCreated, stored)
	})
}

// remoteImageClient builds the HTTP client used for sideloads. It is created per
// request rather than shared: sideloads are rare, and a fresh client keeps the
// connection pool from holding sockets open to arbitrary third-party hosts.
func remoteImageClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: safeDialControl}
	return &http.Client{
		Timeout:   remoteFetchTimeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRemoteRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing to follow redirect to scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// safeDialControl refuses to open a socket to anything but a public unicast
// address. This is the SSRF guard, and it deliberately sits at dial time rather
// than at URL-parse time: it sees the address actually being connected to, after
// DNS has resolved and on every hop of a redirect chain. Validating the
// hostname up front instead would miss a name that resolves to 127.0.0.1, a
// name that resolves differently on the second lookup (DNS rebinding), and a
// public URL that redirects to the cloud metadata endpoint.
func safeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("unexpected dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("unresolved dial address %q", address)
	}
	if !isPublicUnicast(ip) {
		return fmt.Errorf("refusing to connect to non-public address %s", ip)
	}
	return nil
}

// isPublicUnicast reports whether ip is routable on the public internet, and so
// is not something this server should be tricked into fetching on a caller's
// behalf. Everything internal is rejected: loopback, RFC1918, link-local
// (which covers the 169.254.169.254 cloud metadata endpoint), multicast, and
// the two ranges net.IP has no predicate for.
func isPublicUnicast(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// Carrier-grade NAT, 100.64.0.0/10.
		return !(v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127)
	}
	// IPv6 unique-local, fc00::/7.
	return ip[0]&0xfe != 0xfc
}

// remoteFileName derives an upload base name from a source URL. The result is
// still passed through sanitizeBaseName, so this only has to produce something
// meaningful, not something safe.
func remoteFileName(u *url.URL) string {
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "pasted-image"
	}
	return base
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
	// Never hand back a name the indexer would mistake for a WordPress
	// derivative and skip (see wpDerivativeSuffix). "-" is already the separator
	// this function emits, so "-img" cannot collide with a slug shape it
	// produces for any other input.
	if wpDerivativeSuffix.MatchString(base) {
		base += "-img"
	}
	return base
}

// ensureTraversable best-effort widens directory permissions to 0775 so the
// media server can descend into directories this process created. Failures are
// ignored on purpose: the only way chmod fails here is that someone else owns
// the directory, which means it predates us and already has working modes.
func ensureTraversable(dirs ...string) {
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() == 0o775 {
			continue
		}
		if err := os.Chmod(dir, 0o775); err != nil {
			slog.Debug("media upload: could not widen directory mode", "dir", dir, "error", err)
		}
	}
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

	// os.CreateTemp always creates with mode 0600, and os.Rename moves the temp
	// file's *inode* onto the destination -- so the 0644 that reserveAndRename
	// uses to claim the name is discarded along with the file it created. Without
	// this the stored asset is readable only by the CMS's own uid, and the Nginx
	// worker serving /wp-content/ answers 403 for every uploaded image. Chmod is
	// not subject to the umask, which is what we want here.
	if err = tmp.Chmod(0o644); err != nil {
		return "", 0, err
	}

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

	// tmp.Sync() above made the *contents* durable; the directory entry carrying
	// the final name is separate metadata. Without this a crash between the
	// rename and the next metadata flush could leave the DB row pointing at a
	// name that never landed. Best effort: a failure here does not make the
	// stored file any less usable right now.
	syncDir(dir)
	return name, size, nil
}

// syncDir flushes a directory's entries so a rename into it survives a crash.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		slog.Debug("media upload: could not open directory to sync", "dir", dir, "error", err)
		return
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		slog.Debug("media upload: could not sync directory", "dir", dir, "error", err)
	}
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

// GetPublicGallery is the public site's photo-gallery feed. Unlike the
// editor-facing listing it is unauthenticated, so it is narrowed to images —
// the assets Nginx already serves publicly off the CephFS mount — and it does
// not accept the free-text search parameter, which would otherwise turn the
// endpoint into a lookup tool over editors' file names and captions.
//
// @Summary List gallery images for the public site
// @Tags media
// @Produce json
// @Param limit query int false "Max results" default(50)
// @Param offset query int false "Offset"
// @Success 200 {object} models.MediaGalleryResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/gallery [get]
func GetPublicGallery(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, limit, offset := listParams(r, 50)
		items, _, err := db.ListMedia(r.Context(), conn, models.MediaListParams{
			Limit:    limit,
			Offset:   offset,
			MimeType: "image/",
			SortBy:   models.MediaSortBy(r.URL.Query().Get("sort_by")),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		gallery := make([]models.MediaOverview, 0, len(items))
		for _, item := range items {
			gallery = append(gallery, models.MediaOverview{
				ID:       item.ID,
				Path:     item.Path,
				FileName: item.FileName,
				URL:      uploadURL(item.Path),
				MimeType: item.MimeType,
				Width:    item.Width,
				Height:   item.Height,
				AltText:  item.AltText,
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
					// The row is already gone, so a failed unlink leaves a file the
					// next reindex will re-adopt -- as a new row, without the alt
					// text and caption this one had. That looks like the delete
					// silently undid itself, so it has to be visible in the log.
					if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
						slog.Error("media delete: could not remove file; a reindex will re-add it",
							"path", item.Path, "error", err)
					} else {
						syncDir(filepath.Dir(abs))
					}
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
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/media/index [post]
func PostMediaIndex(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An index over an unmounted root would walk an empty tree and report a
		// clean "0 added", which reads as "the corpus is gone" rather than as the
		// mount failure it is.
		if err := CheckMediaStorage(); err != nil {
			writeStoreImageError(w, err)
			return
		}
		root := mediaRoot()

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
