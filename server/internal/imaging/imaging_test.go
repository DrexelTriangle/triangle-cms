package imaging

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	db "server/internal/database"
	"server/internal/models"
)

// stubSidecar renders by echoing the width it would produce: never wider than
// sourceWidth, the way the real sidecar's size="down" behaves.
func stubSidecar(t *testing.T, sourceWidth int, status int) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(Recipe{Name: "webp-test-v1", Format: "webp", Widths: []int{480, 960, 1600, 2400}})
		case "/render":
			if _, err := io.ReadAll(r.Body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"stub failure"}`))
				return
			}
			width, _ := strconv.Atoi(r.URL.Query().Get("width"))
			if width > sourceWidth {
				width = sourceWidth
			}
			w.Header().Set("X-Image-Width", strconv.Itoa(width))
			w.Header().Set("X-Image-Height", strconv.Itoa(width/2))
			_, _ = w.Write([]byte("webp:" + strconv.Itoa(width)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestClientDisabledWithoutURL(t *testing.T) {
	client := New("", time.Second)
	if client.Enabled() {
		t.Fatal("expected an empty URL to disable the client")
	}
	if _, err := client.Recipe(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if _, err := client.Render(context.Background(), []byte("x"), 480); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestClientRenderReportsActualDimensions(t *testing.T) {
	server := stubSidecar(t, 1000, http.StatusOK)
	client := New(server.URL, time.Second)

	got, err := client.Render(context.Background(), []byte("original"), 1600)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got.Width != 1000 || got.Height != 500 || string(got.Data) != "webp:1000" {
		t.Fatalf("unexpected rendition %+v (%q)", got, got.Data)
	}
}

func TestClientRenderClassifiesFailures(t *testing.T) {
	for _, tc := range []struct {
		status        int
		unprocessable bool
	}{
		{http.StatusUnprocessableEntity, true},
		{http.StatusRequestEntityTooLarge, true},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	} {
		server := stubSidecar(t, 1000, tc.status)
		_, err := New(server.URL, time.Second).Render(context.Background(), []byte("x"), 480)
		var unprocessable *UnprocessableError
		if got := errors.As(err, &unprocessable); got != tc.unprocessable {
			t.Errorf("status %d: unprocessable = %v, want %v (err %v)", tc.status, got, tc.unprocessable, err)
		}
		if tc.unprocessable && unprocessable.Detail != "stub failure" {
			t.Errorf("status %d: detail = %q, want the sidecar's detail", tc.status, unprocessable.Detail)
		}
	}
}

func TestVariantPath(t *testing.T) {
	for _, tc := range []struct{ media, want string }{
		{"wp-content/uploads/2026/08/staff.jpg", "wp-content/variants/r1/2026/08/staff.jpg.960w.webp"},
		{"wp-content/uploads/2026/08/staff.png", "wp-content/variants/r1/2026/08/staff.png.960w.webp"},
		{"wp-content/uploads/../../../etc/passwd", "wp-content/variants/r1/etc/passwd.960w.webp"},
	} {
		if got := variantPath("r1", tc.media, 960); got != tc.want {
			t.Errorf("variantPath(%q) = %q, want %q", tc.media, got, tc.want)
		}
	}
}

func TestValidateRecipe(t *testing.T) {
	ok := Recipe{Name: "webp-q80-v1", Format: "webp", Widths: []int{480, 960}}
	if err := validateRecipe(ok); err != nil {
		t.Fatalf("valid recipe rejected: %v", err)
	}
	for name, bad := range map[string]Recipe{
		"traversal":  {Name: "../x", Format: "webp", Widths: []int{480}},
		"slash":      {Name: "a/b", Format: "webp", Widths: []int{480}},
		"empty":      {Name: "", Format: "webp", Widths: []int{480}},
		"format":     {Name: "r1", Format: "avif", Widths: []int{480}},
		"no widths":  {Name: "r1", Format: "webp"},
		"descending": {Name: "r1", Format: "webp", Widths: []int{960, 480}},
	} {
		if err := validateRecipe(bad); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestRenderStopsAtOriginalWidthAndWritesReadableFiles(t *testing.T) {
	root := t.TempDir()
	mediaPath := "wp-content/uploads/2026/08/staff.jpg"
	if err := os.MkdirAll(filepath.Join(root, "wp-content/uploads/2026/08"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, mediaPath), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := stubSidecar(t, 1000, http.StatusOK)
	client := New(server.URL, time.Second)
	recipe, err := client.Recipe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := NewReconciler(nil, client, root, nil)

	variants, err := r.render(context.Background(), recipe, db.RenditionSource{MediaID: 1, Path: mediaPath})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// 480 and 960 fit; 1600 comes back at the original's 1000; 2400 is never
	// asked for, because it could only produce the same 1000px image again.
	wantWidths := []int{480, 960, 1000}
	if len(variants) != len(wantWidths) {
		t.Fatalf("got %d variants %+v, want widths %v", len(variants), variants, wantWidths)
	}
	for i, variant := range variants {
		if variant.Width != wantWidths[i] {
			t.Errorf("variant %d width = %d, want %d", i, variant.Width, wantWidths[i])
		}
		info, err := os.Stat(filepath.Join(root, variant.Path))
		if err != nil {
			t.Fatalf("variant %s not written: %v", variant.Path, err)
		}
		// Nginx runs as a different user; a 0600 file is a 403.
		if info.Mode().Perm() != 0o644 {
			t.Errorf("variant %s mode = %v, want 0644", variant.Path, info.Mode().Perm())
		}
		if info.Size() != variant.SizeBytes {
			t.Errorf("variant %s size = %d, recorded %d", variant.Path, info.Size(), variant.SizeBytes)
		}
	}

	leftovers, _ := filepath.Glob(filepath.Join(root, "wp-content/variants/webp-test-v1/2026/08/.rendition-*"))
	if len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

func TestRenderMissingOriginalIsPermanent(t *testing.T) {
	server := stubSidecar(t, 1000, http.StatusOK)
	client := New(server.URL, time.Second)
	recipe, _ := client.Recipe(context.Background())
	r := NewReconciler(nil, client, t.TempDir(), nil)

	_, err := r.render(context.Background(), recipe, db.RenditionSource{MediaID: 1, Path: "wp-content/uploads/gone.jpg"})
	if !errors.Is(err, errUnusableSource) {
		t.Fatalf("expected errUnusableSource, got %v", err)
	}
}

func TestRemoveFilesStaysInsideVariantsTree(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "wp-content/uploads/keep.jpg")
	variant := filepath.Join(root, "wp-content/variants/r0/keep.jpg.480w.webp")
	for _, p := range []string{original, variant} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := NewReconciler(nil, New("", time.Second), root, nil)
	r.removeFiles([]db.RenditionVariant{
		{Path: "wp-content/uploads/keep.jpg"},
		{Path: "wp-content/variants/../uploads/keep.jpg"},
		{Path: "wp-content/variants/r0/keep.jpg.480w.webp"},
	}, nil)

	if _, err := os.Stat(original); err != nil {
		t.Fatalf("an original was deleted: %v", err)
	}
	if _, err := os.Stat(variant); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale variant was not deleted: %v", err)
	}
}

func TestIndexForURL(t *testing.T) {
	index := NewIndex(nil, "https://delta.example")
	index.byPath = map[string][]models.ImageVariant{
		"wp-content/uploads/2026/08/staff photo.jpg": {{URL: "u", Width: 480, Height: 240}},
	}

	for _, imageURL := range []string{
		"https://delta.example/wp-content/uploads/2026/08/staff%20photo.jpg",
		"https://www.thetriangle.org/wp-content/uploads/2026/08/staff%20photo.jpg?ver=2",
		"/wp-content/uploads/2026/08/staff%20photo.jpg",
	} {
		if got := index.ForURL(imageURL); len(got) != 1 {
			t.Errorf("ForURL(%q) = %v, want the stored variant", imageURL, got)
		}
	}
	for _, imageURL := range []string{"", "https://elsewhere.example/photo.jpg", "https://delta.example/wp-content/uploads/other.jpg"} {
		if got := index.ForURL(imageURL); got != nil {
			t.Errorf("ForURL(%q) = %v, want nil", imageURL, got)
		}
	}

	var unset *Index
	if got := unset.ForURL("https://delta.example/wp-content/uploads/x.jpg"); got != nil {
		t.Errorf("nil index returned %v", got)
	}
}

func TestIndexForContent(t *testing.T) {
	ladder := []models.ImageVariant{
		{URL: "a-480", Width: 480, Height: 320},
		{URL: "a-2400", Width: 2400, Height: 1600},
	}
	index := NewIndex(nil, "https://delta.example")
	index.byPath = map[string][]models.ImageVariant{
		"wp-content/uploads/2026/08/lead.jpg":  ladder,
		"wp-content/uploads/2026/07/photo.jpg": ladder,
		"wp-content/uploads/2026/07/a&b.jpg":   ladder,
	}

	body := `
		<p>intro</p>
		<figure><img class="x" src="https://delta.example/wp-content/uploads/2026/08/lead.jpg?ver=1" alt=""></figure>
		[caption]<img src='https://www.thetriangle.org/wp-content/uploads/2026/07/photo-1024x683.jpg' />[/caption]
		<img src="https://delta.example/wp-content/uploads/2026/07/photo-150x150.jpg">
		<img src="https://delta.example/wp-content/uploads/2026/07/a&amp;b.jpg">
		<img src="https://delta.example/wp-content/uploads/2026/07/unrendered.jpg">
		<img src="https://elsewhere.example/hotlinked.jpg">
		<img src="https://delta.example/wp-content/uploads/2026/08/lead.jpg">`

	got := index.ForContent(body)

	for _, key := range []string{
		"wp-content/uploads/2026/08/lead.jpg",
		// A WordPress resized copy resolves to its original's renditions...
		"wp-content/uploads/2026/07/photo-1024x683.jpg",
		// ...and entity-encoded URLs are decoded before lookup.
		"wp-content/uploads/2026/07/a&b.jpg",
	} {
		if len(got[key]) != len(ladder) {
			t.Errorf("missing renditions for %q: %v", key, got[key])
		}
	}
	// ...but a square crop of a 3:2 photo does not: substituting the full
	// photo would change what the reader sees.
	if _, ok := got["wp-content/uploads/2026/07/photo-150x150.jpg"]; ok {
		t.Error("a cropped thumbnail was matched to its uncropped original")
	}
	if len(got) != 3 {
		t.Errorf("got %d keys, want 3: %v", len(got), got)
	}

	if got := index.ForContent("<p>no images</p>"); got != nil {
		t.Errorf("expected nil for a body without renditions, got %v", got)
	}
	var unset *Index
	if got := unset.ForContent(body); got != nil {
		t.Errorf("nil index returned %v", got)
	}
}
