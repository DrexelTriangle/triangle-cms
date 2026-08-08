package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonBody is comfortably over minCompressSize and as redundant as real article
// JSON, so a test that asserts "smaller than the original" is not asserting a
// coin flip.
func jsonBody(n int) string {
	return `{"articles":[` + strings.Repeat(`{"title":"Dragons win again","excerpt":"The Drexel Dragons"},`, n) + `]}`
}

func handlerWriting(contentType, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	})
}

func serve(t *testing.T, h http.Handler, acceptEncoding string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/articles", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	Compression(h).ServeHTTP(rec, req)
	return rec.Result()
}

func TestCompressionGzipsJSONForAcceptingClients(t *testing.T) {
	body := jsonBody(50)
	res := serve(t, handlerWriting("application/json", body), "gzip, deflate, br")

	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to contain Accept-Encoding", got)
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(raw) >= len(body) {
		t.Errorf("compressed body is %d bytes, not smaller than the %d-byte original", len(raw), len(body))
	}

	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("open gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if string(decoded) != body {
		t.Errorf("round-tripped body does not match the original")
	}
}

func TestCompressionSkipsClientsThatDidNotAskForIt(t *testing.T) {
	body := jsonBody(50)
	res := serve(t, handlerWriting("application/json", body), "")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	raw, _ := io.ReadAll(res.Body)
	if string(raw) != body {
		t.Errorf("body was altered for a client that did not accept gzip")
	}
	// The header still has to be there, or a shared cache keyed on the URL
	// alone will hand this identity response to a gzip client and vice versa.
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to contain Accept-Encoding", got)
	}
}

func TestCompressionSkipsAlreadyCompressedContentTypes(t *testing.T) {
	// Bytes that do not compress, standing in for a JPEG out of the media
	// library: the point is that the middleware never looks at them.
	body := strings.Repeat("\x89PNG\r\n\x1a\n\xde\xad\xbe\xef", 200)
	res := serve(t, handlerWriting("image/jpeg", body), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for image/jpeg", got)
	}
	raw, _ := io.ReadAll(res.Body)
	if string(raw) != body {
		t.Errorf("image body was altered")
	}
}

func TestCompressionSkipsShortBodies(t *testing.T) {
	body := `{"error":"not found"}`
	res := serve(t, handlerWriting("application/json", body), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for a body under minCompressSize", got)
	}
	raw, _ := io.ReadAll(res.Body)
	if string(raw) != body {
		t.Errorf("short body = %q, want %q", raw, body)
	}
}

func TestCompressionPreservesStatusCode(t *testing.T) {
	body := jsonBody(50)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, body)
	})
	res := serve(t, h, "gzip")

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusCreated)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
}

// A stale Content-Length is the difference between a whole response and one the
// client truncates at the uncompressed length.
func TestCompressionDropsContentLength(t *testing.T) {
	body := jsonBody(50)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "999999")
		_, _ = io.WriteString(w, body)
	})
	res := serve(t, h, "gzip")

	if got := res.Header.Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want it removed once the body is gzipped", got)
	}
}

func TestCompressionLeavesHandlerEncodedBodiesAlone(t *testing.T) {
	body := jsonBody(50)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "identity")
		_, _ = io.WriteString(w, body)
	})
	res := serve(t, h, "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "identity" {
		t.Fatalf("Content-Encoding = %q, want the handler's own identity to survive", got)
	}
	raw, _ := io.ReadAll(res.Body)
	if string(raw) != body {
		t.Errorf("body was double-encoded")
	}
}

func TestCompressionHandlesEmptyResponses(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	res := serve(t, h, "gzip")

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}
	raw, _ := io.ReadAll(res.Body)
	if len(raw) != 0 {
		t.Errorf("body = %q, want empty", raw)
	}
}
