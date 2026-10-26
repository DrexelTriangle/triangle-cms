package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"server/internal/models"
	"server/internal/scraper"
)

func instagramRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeds/instagram", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// stubDownloads replaces the network with the given bytes per URL path.
func stubDownloads(t *testing.T, files map[string][]byte) {
	t.Helper()
	previous := downloadRemote
	downloadRemote = func(_ context.Context, target *url.URL) (*os.File, error) {
		content, ok := files[target.Path]
		if !ok {
			return nil, os.ErrNotExist
		}
		tmp, err := os.CreateTemp(t.TempDir(), "stub-*")
		if err != nil {
			return nil, err
		}
		if _, err := tmp.Write(content); err != nil {
			return nil, err
		}
		if _, err := tmp.Seek(0, 0); err != nil {
			return nil, err
		}
		return tmp, nil
	}
	t.Cleanup(func() { downloadRemote = previous })
}

func fakeSidecar(t *testing.T, images []string) *scraper.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string][]string{"images": images})
	}))
	t.Cleanup(srv.Close)
	return scraper.New(srv.URL, time.Second)
}

func TestPostInstagramEmbedStoresEveryImage(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	lib := useFakeMediaLibrary(t)
	stubDownloads(t, map[string][]byte{"/a.jpg": pngBytes(t, 2, 2), "/b.jpg": pngBytes(t, 3, 3)})
	client := fakeSidecar(t, []string{"https://cdn.test/a.jpg", "https://cdn.test/b.jpg"})

	rec := httptest.NewRecorder()
	PostInstagramEmbed(nil, client).ServeHTTP(rec, instagramRequest(t, `{"url":"https://www.instagram.com/p/CUbHfhpswxt/"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out models.InstagramEmbedResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Media) != 2 || len(lib.calls) != 2 {
		t.Fatalf("media %v, library calls %v", out.Media, lib.calls)
	}
	for _, p := range out.Media {
		if !strings.HasPrefix(p, "/wp-content/uploads/") {
			t.Fatalf("path %q is not under uploads", p)
		}
	}
}

func TestPostInstagramEmbedRejectsOtherLinks(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	for _, bad := range []string{
		`{"url":"https://www.instagram.com/everydayastronaut/"}`,
		`{"url":"https://evil.test/p/abc/"}`,
		`{"url":"https://www.instagram.com/p/abc/?igsh=1"}`,
	} {
		rec := httptest.NewRecorder()
		PostInstagramEmbed(nil, fakeSidecar(t, nil)).ServeHTTP(rec, instagramRequest(t, bad))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", bad, rec.Code)
		}
	}
}

func TestPostInstagramEmbedWithoutSidecar(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	rec := httptest.NewRecorder()
	PostInstagramEmbed(nil, scraper.New("", time.Second)).ServeHTTP(rec, instagramRequest(t, `{"url":"https://www.instagram.com/p/abc/"}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestPostInstagramEmbedNoImages(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	rec := httptest.NewRecorder()
	PostInstagramEmbed(nil, fakeSidecar(t, []string{})).ServeHTTP(rec, instagramRequest(t, `{"url":"https://www.instagram.com/p/abc/"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestPostInstagramEmbedFailedDownload(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	useFakeMediaLibrary(t)
	stubDownloads(t, map[string][]byte{})
	rec := httptest.NewRecorder()
	PostInstagramEmbed(nil, fakeSidecar(t, []string{"https://cdn.test/missing.jpg"})).ServeHTTP(rec, instagramRequest(t, `{"url":"https://www.instagram.com/p/abc/"}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("could not copy")) {
		t.Fatalf("body %s", rec.Body.String())
	}
}
