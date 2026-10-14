package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
// argument, which only decides how much stays in RAM before spilling to a
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

// contentStem is the name storeUpload is expected to give bytes uploaded under
// base: the readable stem plus 128 bits of their SHA-256.
func contentStem(base string, content []byte) string {
	sum := sha256.Sum256(content)
	return base + "-" + hex.EncodeToString(sum[:])[:32]
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return got
}

// Cloudflare caches /wp-content/ for a long time, so a URL must only ever serve
// one set of bytes. Replacing an image means uploading it again, usually under
// the same file name, and that must land at a new URL rather than at (or next
// to, by an incrementing counter that a deleted file frees up again) the old one.
func TestStoreUpload_DifferentBytesSameNameGetDifferentPaths(t *testing.T) {
	dir := t.TempDir()
	original := pngBytes(t, 4, 4)
	replacement := pngBytes(t, 5, 5)

	first, _, err := storeUpload(dir, "pic", ".png", bytes.NewReader(original))
	if err != nil {
		t.Fatalf("store original: %v", err)
	}
	second, _, err := storeUpload(dir, "pic", ".png", bytes.NewReader(replacement))
	if err != nil {
		t.Fatalf("store replacement: %v", err)
	}

	if want := contentStem("pic", original) + ".png"; first != want {
		t.Fatalf("original stored as %q, want %q", first, want)
	}
	if want := contentStem("pic", replacement) + ".png"; second != want {
		t.Fatalf("replacement stored as %q, want %q", second, want)
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, first)), original) {
		t.Fatal("original file no longer holds the original bytes")
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, second)), replacement) {
		t.Fatal("replacement file does not hold the replacement bytes")
	}
}

// dirNames lists dir, failing on any leftover upload temp file: storeUpload
// must clean up after itself whichever way it resolves a name.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
		names = append(names, entry.Name())
	}
	return names
}

// Uploading the same bytes again, under the same name and in the same month,
// is the same asset: it reuses the one file (and so the one URL and library
// row) instead of minting a copy at a second URL.
func TestStoreUpload_IdenticalBytesReuseOneFile(t *testing.T) {
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

	if first != second {
		t.Fatalf("identical bytes stored twice: %q and %q", first, second)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
	if names := dirNames(t, dir); len(names) != 1 {
		t.Fatalf("expected exactly 1 file, got %v", names)
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, first)), content) {
		t.Fatal("stored file does not hold the uploaded bytes")
	}
}

// The mount guard. An empty MEDIA_ROOT standing in for an unmounted CephFS must
// not accept uploads: they would land on local disk and be shadowed, lost,
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

// Whatever already sits at the content-addressed name and is not these exact
// bytes is never overwritten or adopted: a legacy file that happens to share
// the name, a crash-stranded claim placeholder, a symlink. The upload moves on
// to the next name, and a repeat of it finds and reuses that one.
func TestStoreUpload_NeverOverwritesOrAdoptsForeignFileAtItsName(t *testing.T) {
	content := pngBytes(t, 4, 4)
	primary := contentStem("pic", content) + ".png"
	fallback := contentStem("pic", content) + "-1.png"

	// Same length as content, so a size check alone would wrongly match.
	sameSize := bytes.Repeat([]byte{0xAB}, len(content))

	cases := map[string]func(t *testing.T, dir string){
		"different bytes of the same size": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, primary), sameSize, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"stranded empty claim placeholder": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, primary), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"symlink to identical bytes": func(t *testing.T, dir string) {
			target := filepath.Join(t.TempDir(), "elsewhere.png")
			if err := os.WriteFile(target, content, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, primary)); err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			plant(t, dir)
			before, err := os.Lstat(filepath.Join(dir, primary))
			if err != nil {
				t.Fatal(err)
			}
			var beforeBytes []byte
			if before.Mode().IsRegular() {
				beforeBytes = readFile(t, filepath.Join(dir, primary))
			}

			got, _, err := storeUpload(dir, "pic", ".png", bytes.NewReader(content))
			if err != nil {
				t.Fatalf("storeUpload: %v", err)
			}
			if got != fallback {
				t.Fatalf("stored as %q, want %q", got, fallback)
			}
			if !bytes.Equal(readFile(t, filepath.Join(dir, got)), content) {
				t.Fatal("fallback name does not hold the uploaded bytes")
			}

			after, err := os.Lstat(filepath.Join(dir, primary))
			if err != nil {
				t.Fatalf("the planted file is gone: %v", err)
			}
			if after.Mode() != before.Mode() || after.Size() != before.Size() {
				t.Fatalf("the planted file changed: %v/%d -> %v/%d", before.Mode(), before.Size(), after.Mode(), after.Size())
			}
			if before.Mode().IsRegular() && !bytes.Equal(readFile(t, filepath.Join(dir, primary)), beforeBytes) {
				t.Fatal("the planted file's bytes were overwritten")
			}

			again, _, err := storeUpload(dir, "pic", ".png", bytes.NewReader(content))
			if err != nil {
				t.Fatalf("repeat storeUpload: %v", err)
			}
			if again != fallback {
				t.Fatalf("repeat upload stored as %q, want it to reuse %q", again, fallback)
			}
			if names := dirNames(t, dir); len(names) != 2 {
				t.Fatalf("expected the planted file and one upload, got %v", names)
			}
		})
	}
}

// Concurrent uploads under one client name must each end up at a URL serving
// exactly their own bytes, with nothing half-written or left behind. Identical
// bytes racing each other may occasionally both claim a name (one finds the
// other's empty claim placeholder and moves on), so that case pins only what
// matters: every name returned holds the bytes, and every name is theirs.
func TestStoreUpload_ConcurrentUploads(t *testing.T) {
	const workers = 16

	t.Run("different bytes", func(t *testing.T) {
		dir := t.TempDir()
		contents := make([][]byte, workers)
		for i := range contents {
			contents[i] = pngBytes(t, 2+i, 3)
		}
		names := make([]string, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		for i := range contents {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				names[i], _, errs[i] = storeUpload(dir, "pic", ".png", bytes.NewReader(contents[i]))
			}(i)
		}
		wg.Wait()

		seen := map[string]bool{}
		for i, name := range names {
			if errs[i] != nil {
				t.Fatalf("upload %d: %v", i, errs[i])
			}
			if seen[name] {
				t.Fatalf("two different uploads share %q", name)
			}
			seen[name] = true
			if !bytes.Equal(readFile(t, filepath.Join(dir, name)), contents[i]) {
				t.Fatalf("upload %d at %q does not hold its own bytes", i, name)
			}
		}
		if got := dirNames(t, dir); len(got) != workers {
			t.Fatalf("expected %d files, got %v", workers, got)
		}
	})

	t.Run("identical bytes", func(t *testing.T) {
		dir := t.TempDir()
		content := pngBytes(t, 6, 6)
		stem := contentStem("pic", content)
		names := make([]string, workers)
		errs := make([]error, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				names[i], _, errs[i] = storeUpload(dir, "pic", ".png", bytes.NewReader(content))
			}(i)
		}
		wg.Wait()

		for i, name := range names {
			if errs[i] != nil {
				t.Fatalf("upload %d: %v", i, errs[i])
			}
			if !strings.HasPrefix(name, stem) {
				t.Fatalf("upload %d stored as %q, want a %q name", i, name, stem)
			}
			if !bytes.Equal(readFile(t, filepath.Join(dir, name)), content) {
				t.Fatalf("upload %d at %q does not hold the bytes", i, name)
			}
		}
		for _, name := range dirNames(t, dir) {
			if !bytes.Equal(readFile(t, filepath.Join(dir, name)), content) {
				t.Fatalf("%q in the directory does not hold the bytes", name)
			}
		}
	})
}

// A file name is capped at 255 bytes on every filesystem the media tree has
// lived on, and media.path is a VARCHAR(512). The hash suffix must fit inside
// both however long the client's name was.
func TestStoreUpload_LongNameStaysWithinLimits(t *testing.T) {
	dir := t.TempDir()
	content := pngBytes(t, 4, 4)
	base := sanitizeBaseName(strings.Repeat("very-long-caption-words ", 40) + ".png")

	name, _, err := storeUpload(dir, base, ".webp", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("storeUpload: %v", err)
	}
	if len(name) > 255 {
		t.Fatalf("name is %d bytes, over the 255-byte limit: %q", len(name), name)
	}
	if want := contentStem(base, content) + ".webp"; name != want {
		t.Fatalf("name = %q, want %q", name, want)
	}
	if rel := "wp-content/uploads/2026/10/" + name; len(rel) > 512 {
		t.Fatalf("path is %d bytes, over media.path's 512", len(rel))
	}
	// The worst case is the random fallback after the numbered ones.
	worst := base + "-" + strings.Repeat("f", contentHashHexLen) + "-" + strings.Repeat("f", 12) + ".webp"
	if len(worst) > 255 {
		t.Fatalf("worst-case name is %d bytes", len(worst))
	}
	// A hash never looks like a WordPress derivative, so the indexer adopts it.
	if wpDerivativeSuffix.MatchString(strings.TrimSuffix(name, ".webp")) {
		t.Fatalf("%q looks like a WordPress derivative", name)
	}
}

// fakeMediaLibrary stands in for the media table behind recordMedia: one row
// per path, as the unique key enforces, handing back the existing id for a
// path it already has (what InsertOrGetMedia does against MariaDB).
type fakeMediaLibrary struct {
	mu       sync.Mutex
	ids      map[string]int64
	calls    []string
	failNext int
}

func useFakeMediaLibrary(t *testing.T) *fakeMediaLibrary {
	t.Helper()
	lib := &fakeMediaLibrary{ids: map[string]int64{}}
	previous := recordMedia
	recordMedia = func(_ context.Context, _ *sql.DB, relPath, _ string, _ int64, _, _ int) (int64, error) {
		lib.mu.Lock()
		defer lib.mu.Unlock()
		lib.calls = append(lib.calls, relPath)
		if lib.failNext > 0 {
			lib.failNext--
			return 0, errors.New("database unavailable")
		}
		if id, ok := lib.ids[relPath]; ok {
			return id, nil
		}
		id := int64(len(lib.ids) + 1)
		lib.ids[relPath] = id
		return id, nil
	}
	t.Cleanup(func() { recordMedia = previous })
	return lib
}

func postUpload(t *testing.T, filename string, content []byte) models.MediaUploadResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, filename, content))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload %s: status = %d, want 201; body = %s", filename, rec.Code, rec.Body.String())
	}
	var created models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return created
}

// Replacing an article's image is a fresh upload, almost always under the same
// file name. It must come back as a new asset at a new path, leaving the old
// file and row in place for the articles still using it and for rollback; and
// re-uploading bytes the library already has answers with that same asset.
func TestPostMedia_ReplacementGetsNewAssetAndKeepsTheOld(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	t.Setenv("MEDIA_BASE_URL", "")
	useFakeMediaLibrary(t)

	original := pngBytes(t, 4, 4)
	replacement := pngBytes(t, 8, 8)

	first := postUpload(t, "Lead Photo.png", original)
	second := postUpload(t, "Lead Photo.png", replacement)

	if first.Path == second.Path || first.ID == second.ID {
		t.Fatalf("replacement reused the original asset: %+v vs %+v", first, second)
	}
	if !strings.HasPrefix(path.Base(second.Path), "lead-photo-") {
		t.Fatalf("replacement path %q lost the readable stem", second.Path)
	}
	if second.URL != "/"+second.Path {
		t.Fatalf("url = %q, want it to follow the new path", second.URL)
	}
	if second.Width != 8 || second.Height != 8 {
		t.Fatalf("dimensions = %dx%d, want the replacement's 8x8", second.Width, second.Height)
	}
	if !bytes.Equal(readFile(t, filepath.Join(root, filepath.FromSlash(first.Path))), original) {
		t.Fatal("the original file was changed by the replacement upload")
	}

	again := postUpload(t, "lead photo (copy).PNG", original)
	if again.Path != path.Join(path.Dir(first.Path), contentStem("lead-photo-copy", original)+".png") {
		t.Fatalf("re-upload under another name stored as %q", again.Path)
	}
	repeat := postUpload(t, "Lead Photo.png", original)
	if repeat.Path != first.Path || repeat.ID != first.ID {
		t.Fatalf("identical re-upload = %+v, want the existing asset %+v", repeat, first)
	}
}

// The file is durable before its library row is written. If that write fails
// the upload reports the failure but keeps the file, and the retry must adopt
// that same file instead of leaving it orphaned beside a second copy.
func TestStoreImage_RetryAfterIndexFailureAdoptsTheStoredFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	lib := useFakeMediaLibrary(t)
	lib.failNext = 1
	content := pngBytes(t, 4, 4)

	_, err := storeImage(context.Background(), nil, bytes.NewReader(content), "pic.png")
	if !errors.Is(err, errIndexFailed) {
		t.Fatalf("first attempt err = %v, want errIndexFailed", err)
	}
	if len(lib.calls) != 1 {
		t.Fatalf("recordMedia calls = %v", lib.calls)
	}
	stored := filepath.Join(root, filepath.FromSlash(lib.calls[0]))
	if !bytes.Equal(readFile(t, stored), content) {
		t.Fatal("the file was not kept after the index failure")
	}

	got, err := storeImage(context.Background(), nil, bytes.NewReader(content), "pic.png")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got.Path != lib.calls[0] || got.ID != 1 {
		t.Fatalf("retry = %+v, want path %q with id 1", got, lib.calls[0])
	}
	if names := dirNames(t, filepath.Dir(stored)); len(names) != 1 {
		t.Fatalf("retry left %v, want the one file", names)
	}
}

// storeUpload renames a temp file into place, and os.CreateTemp hardcodes mode
// 0600. If that is not widened before the rename, the stored asset ends up
// readable only by the CMS's own uid: the file is there, but the media server
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
