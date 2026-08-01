package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"server/internal/models"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func uploadRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(mediaFormField, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// The rejection paths all answer before the handler reaches the database, so a
// nil connection is enough to exercise them.

func TestPostMedia_RejectsNonImage(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	rec := httptest.NewRecorder()
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "notes.txt", []byte("this is plainly text, not an image at all")))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body = %s", rec.Code, rec.Body.String())
	}
}

func TestPostMedia_NotConfigured(t *testing.T) {
	t.Setenv("MEDIA_ROOT", "")
	rec := httptest.NewRecorder()
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "pic.png", pngBytes(t, 2, 2)))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestPostMedia_RejectsOversizeUpload(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	t.Setenv("MEDIA_MAX_UPLOAD_BYTES", "1024")
	rec := httptest.NewRecorder()
	// Comfortably past the limit plus the multipart-framing slack, so this is
	// the size check firing and not a boundary-arithmetic accident.
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "big.png", bytes.Repeat([]byte("a"), 64<<10)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
}

// The size limit is enforced by MaxBytesReader, not by ParseMultipartForm's
// argument -- that one only decides how much stays in RAM before spilling to a
// temp file. A body well past multipartMemoryBytes but under the limit must
// therefore parse normally; reaching the 415 content-type check proves it did.
func TestPostMedia_AcceptsBodyLargerThanMultipartMemory(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	t.Setenv("MEDIA_MAX_UPLOAD_BYTES", strconv.FormatInt(multipartMemoryBytes*4, 10))
	rec := httptest.NewRecorder()
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "notes.txt", bytes.Repeat([]byte("a"), int(multipartMemoryBytes)+1<<20)))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (413 means the size cap fired early); body = %s", rec.Code, rec.Body.String())
	}
}

func TestMaxUploadBytes(t *testing.T) {
	// The migrated WordPress corpus holds camera originals near 77 MiB, so a
	// default below that rejects files the newsroom already has.
	if defaultMaxUploadBytes < 80<<20 {
		t.Fatalf("defaultMaxUploadBytes = %d, too small for the legacy corpus", defaultMaxUploadBytes)
	}

	t.Run("default when unset", func(t *testing.T) {
		t.Setenv("MEDIA_MAX_UPLOAD_BYTES", "")
		if got := maxUploadBytes(); got != defaultMaxUploadBytes {
			t.Fatalf("maxUploadBytes = %d, want %d", got, defaultMaxUploadBytes)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("MEDIA_MAX_UPLOAD_BYTES", "4096")
		if got := maxUploadBytes(); got != 4096 {
			t.Fatalf("maxUploadBytes = %d, want 4096", got)
		}
	})

	t.Run("falls back on garbage", func(t *testing.T) {
		t.Setenv("MEDIA_MAX_UPLOAD_BYTES", "90MiB")
		if got := maxUploadBytes(); got != defaultMaxUploadBytes {
			t.Fatalf("maxUploadBytes = %d, want the default %d", got, defaultMaxUploadBytes)
		}
	})
}

func TestPostMediaIndex_NotConfigured(t *testing.T) {
	t.Setenv("MEDIA_ROOT", "")
	rec := httptest.NewRecorder()
	PostMediaIndex(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/media/index", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestSanitizeBaseName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"slugifies and drops extension", "My Photo (final).PNG", "my-photo-final"},
		// Everything before the last separator is discarded, so a traversal
		// attempt collapses to the bare file name.
		{"discards directory components", "../../etc/passwd.png", "passwd"},
		{"discards windows separators", `C:\Users\me\pic.png`, "pic"},
		{"falls back when nothing survives", "!!!.png", "file"},
		{"truncates very long names", strings.Repeat("a", 300) + ".png", strings.Repeat("a", maxBaseNameLen)},
		// A name shaped like a WordPress derivative would be skipped by the
		// indexer, so a table rebuild would never re-adopt the file.
		{"defuses the wp derivative shape", "poster-1920x1080.jpg", "poster-1920x1080-img"},
		{"leaves a non-terminal size alone", "poster-1920x1080-crop.jpg", "poster-1920x1080-crop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeBaseName(tt.in); got != tt.want {
				t.Fatalf("sanitizeBaseName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// storeUpload is where the corpus-safety guarantees live: it must never
// overwrite an existing asset and must not leave partial temp files behind.
func TestStoreUpload_NeverClobbers(t *testing.T) {
	dir := t.TempDir()
	content := pngBytes(t, 4, 4)

	first, _, err := storeUpload(dir, "pic", ".png", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, size, err := storeUpload(dir, "pic", ".png", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second store: %v", err)
	}

	if first != "pic.png" || second != "pic-1.png" {
		t.Fatalf("names = %q, %q; want pic.png, pic-1.png", first, second)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected exactly 2 files, got %d", len(entries))
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}

// The mount guard. An empty MEDIA_ROOT standing in for an unmounted CephFS must
// not accept uploads: they would land on local disk and be shadowed -- lost --
// as soon as the mount was repaired.
func TestCheckMediaStorage(t *testing.T) {
	t.Run("passes when the sentinel is unset", func(t *testing.T) {
		t.Setenv("MEDIA_ROOT", t.TempDir())
		t.Setenv("MEDIA_ROOT_SENTINEL", "")
		if err := CheckMediaStorage(); err != nil {
			t.Fatalf("CheckMediaStorage() = %v, want nil", err)
		}
	})

	t.Run("fails on an unmounted-looking root", func(t *testing.T) {
		t.Setenv("MEDIA_ROOT", t.TempDir())
		t.Setenv("MEDIA_ROOT_SENTINEL", "wp-content/uploads")
		if err := CheckMediaStorage(); !errors.Is(err, errMediaUnavailable) {
			t.Fatalf("CheckMediaStorage() = %v, want errMediaUnavailable", err)
		}
	})

	t.Run("passes once the sentinel exists", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "wp-content", "uploads"), 0o775); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Setenv("MEDIA_ROOT", root)
		t.Setenv("MEDIA_ROOT_SENTINEL", "wp-content/uploads")
		if err := CheckMediaStorage(); err != nil {
			t.Fatalf("CheckMediaStorage() = %v, want nil", err)
		}
	})

	t.Run("reports unconfigured separately", func(t *testing.T) {
		t.Setenv("MEDIA_ROOT", "")
		if err := CheckMediaStorage(); !errors.Is(err, errMediaNotConfigured) {
			t.Fatalf("CheckMediaStorage() = %v, want errMediaNotConfigured", err)
		}
	})
}

// A 503 rather than a 500 or a silent success: the bytes were never written and
// the upload is worth retrying once the mount is back.
func TestPostMedia_UnavailableStorageIs503(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	t.Setenv("MEDIA_ROOT_SENTINEL", "wp-content/uploads")

	rec := httptest.NewRecorder()
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "pic.png", pngBytes(t, 2, 2)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}

func TestUploadURL(t *testing.T) {
	rel := "wp-content/uploads/2026/07/pic.png"

	t.Run("root-relative when unset", func(t *testing.T) {
		t.Setenv("MEDIA_BASE_URL", "")
		if got := uploadURL(rel); got != "/"+rel {
			t.Fatalf("uploadURL = %q, want %q", got, "/"+rel)
		}
	})

	t.Run("joined to base without doubling the slash", func(t *testing.T) {
		t.Setenv("MEDIA_BASE_URL", "https://media.example.org/")
		if got, want := uploadURL(rel), "https://media.example.org/"+rel; got != want {
			t.Fatalf("uploadURL = %q, want %q", got, want)
		}
	})
}

func TestResolveMediaPath(t *testing.T) {
	root := t.TempDir()

	t.Run("resolves a stored path", func(t *testing.T) {
		got, ok := resolveMediaPath(root, "wp-content/uploads/2026/07/pic.png")
		if !ok {
			t.Fatal("expected the path to resolve")
		}
		want := filepath.Join(root, "wp-content", "uploads", "2026", "07", "pic.png")
		if got != want {
			t.Fatalf("resolveMediaPath = %q, want %q", got, want)
		}
	})

	// A stored path is always server-generated, so this is defence in depth
	// against a hand-edited row rather than reachable user input. Traversal is
	// neutralized rather than rejected: cleaning against a leading "/" collapses
	// the ".." segments away, so the result must still land inside root.
	for _, traversal := range []string{"../outside.png", "wp-content/../../outside.png", "./../../../etc/passwd"} {
		t.Run("contains "+traversal, func(t *testing.T) {
			got, ok := resolveMediaPath(root, traversal)
			if !ok {
				return // refusing outright is also acceptable
			}
			if !strings.HasPrefix(got, filepath.Clean(root)+string(os.PathSeparator)) {
				t.Fatalf("resolveMediaPath(%q) = %q, which escapes root %q", traversal, got, root)
			}
		})
	}

	for _, empty := range []string{"", "/", "   ", "..", "../.."} {
		t.Run("refuses empty "+empty, func(t *testing.T) {
			if _, ok := resolveMediaPath(root, empty); ok {
				t.Fatalf("expected %q to be refused", empty)
			}
		})
	}
}

func TestBoolParam(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"", false},
		{"keep_file", true},
		{"keep_file=", true},
		{"keep_file=true", true},
		{"keep_file=1", true},
		{"keep_file=false", false},
		{"keep_file=0", false},
		{"keep_file=no", false},
	}

	for _, tt := range tests {
		t.Run("?"+tt.query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/v1/media/1?"+tt.query, nil)
			if got := boolParam(req, "keep_file"); got != tt.want {
				t.Fatalf("boolParam(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestMediaIDParam(t *testing.T) {
	tests := []struct {
		id     string
		want   int64
		wantOK bool
	}{
		{"1", 1, true},
		{"4096", 4096, true},
		{"0", 0, false},
		{"-3", 0, false},
		{"abc", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run("id="+tt.id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/media/"+tt.id, nil)
			req.SetPathValue("id", tt.id)
			got, ok := mediaIDParam(req)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("mediaIDParam(%q) = (%d, %v), want (%d, %v)", tt.id, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// The index job must survive the request that started it — that is the whole
// point of making it asynchronous — and must not allow two concurrent runs.
func TestMediaIndexJob_Lifecycle(t *testing.T) {
	job := &mediaIndexJob{}

	if got := job.status(); got.Running || got.StartedAt != nil || got.FinishedAt != nil {
		t.Fatalf("fresh job should be idle, got %+v", got)
	}

	if !job.tryStart() {
		t.Fatal("first tryStart should claim the job")
	}
	if job.tryStart() {
		t.Fatal("second tryStart must be refused while running")
	}

	running := job.status()
	if !running.Running || running.StartedAt == nil {
		t.Fatalf("running status = %+v, want Running with StartedAt", running)
	}
	if running.FinishedAt != nil {
		t.Fatal("FinishedAt must be unset while running")
	}

	job.setProgress(models.MediaIndexResponse{Walked: 1200, Scanned: 40, Added: 12})
	if got := job.status().Progress; got.Walked != 1200 || got.Added != 12 {
		t.Fatalf("progress = %+v, want walked 1200 / added 12", got)
	}

	job.finish(models.MediaIndexResponse{Walked: 2000, Scanned: 80, Added: 20, Skipped: 60}, nil)
	done := job.status()
	if done.Running || done.FinishedAt == nil || done.Error != "" {
		t.Fatalf("finished status = %+v, want idle with FinishedAt and no error", done)
	}
	if done.Progress.Added != 20 || done.Progress.Skipped != 60 {
		t.Fatalf("final progress = %+v", done.Progress)
	}

	// A completed run must not block the next one, and starting again clears the
	// previous result rather than reporting stale counts as current.
	if !job.tryStart() {
		t.Fatal("a finished job should be startable again")
	}
	if got := job.status(); got.Progress.Added != 0 || got.FinishedAt != nil {
		t.Fatalf("restarted job should reset counters, got %+v", got)
	}
}

func TestMediaIndexJob_RecordsFailure(t *testing.T) {
	job := &mediaIndexJob{}
	job.tryStart()
	job.finish(models.MediaIndexResponse{Walked: 10}, context.DeadlineExceeded)

	got := job.status()
	if got.Running {
		t.Fatal("job should not be running after finish")
	}
	if got.Error != context.DeadlineExceeded.Error() {
		t.Fatalf("Error = %q, want %q", got.Error, context.DeadlineExceeded.Error())
	}
}

func TestPostMediaIndex_SecondRequestConflicts(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())

	// Claim the shared job so the handler sees a run already in flight, then
	// release it so later tests are unaffected.
	if !mediaIndexer.tryStart() {
		t.Fatal("expected the package job to be idle at test start")
	}
	t.Cleanup(func() { mediaIndexer.finish(models.MediaIndexResponse{}, nil) })

	rec := httptest.NewRecorder()
	PostMediaIndex(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/media/index", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetMediaIndexStatus_ReportsIdle(t *testing.T) {
	rec := httptest.NewRecorder()
	GetMediaIndexStatus().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/media/index", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var status models.MediaIndexStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.Running {
		t.Fatal("expected the shared job to be idle")
	}
}

// storeUpload renames a temp file into place, and os.CreateTemp hardcodes mode
// 0600. If that is not widened before the rename, the stored asset ends up
// readable only by the CMS's own uid -- the file is there, but the media server
// answers 403 for every uploaded image. Pin the mode so that cannot regress.
func TestStoreUpload_StoresWorldReadableFile(t *testing.T) {
	dir := t.TempDir()

	name, size, err := storeUpload(dir, "photo", ".png", bytes.NewReader(pngBytes(t, 4, 4)))
	if err != nil {
		t.Fatalf("storeUpload: %v", err)
	}
	if size == 0 {
		t.Fatal("storeUpload reported a zero-byte write")
	}

	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("stat stored file: %v", err)
	}
	// Chmod is not masked by the umask, so this is an exact comparison.
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("stored file mode = %#o, want 0644 (0600 means Nginx will 403 it)", perm)
	}
}

func TestEnsureTraversable_WidensNarrowDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026", "08")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ensureTraversable(filepath.Dir(dir), dir)

	for _, target := range []string{filepath.Dir(dir), dir} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat %s: %v", target, err)
		}
		if perm := info.Mode().Perm(); perm != 0o775 {
			t.Fatalf("%s mode = %#o, want 0775", target, perm)
		}
	}
}
