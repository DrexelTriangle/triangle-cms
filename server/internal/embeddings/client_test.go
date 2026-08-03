package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func stubSidecar(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Model: "test-model", Dimensions: 3})
		case "/embed":
			if calls != nil {
				calls.Add(1)
			}
			var request embedRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			vectors := make([][]float32, len(request.Texts))
			for i := range request.Texts {
				// Encode the requested kind into the vector so tests can assert the
				// query/document distinction actually reaches the sidecar.
				kind := float32(0)
				if request.Kind == KindQuery {
					kind = 1
				}
				vectors[i] = []float32{kind, float32(i), float32(len(request.Texts[i]))}
			}
			_ = json.NewEncoder(w).Encode(embedResponse{Model: "test-model", Dimensions: 3, Vectors: vectors})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// A deployment without the sidecar has to run, not error out at startup. Every
// entry point returns ErrDisabled so callers can branch on one thing.
func TestDisabledClient(t *testing.T) {
	client := New("", time.Second)

	if client.Enabled() {
		t.Error("a client with no URL reports itself enabled")
	}
	if _, err := client.EmbedQuery(context.Background(), "anything"); !errors.Is(err, ErrDisabled) {
		t.Errorf("EmbedQuery error = %v, want ErrDisabled", err)
	}
	if _, _, err := client.Embed(context.Background(), []string{"a"}, KindDocument); !errors.Is(err, ErrDisabled) {
		t.Errorf("Embed error = %v, want ErrDisabled", err)
	}
	if _, _, err := client.Model(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Errorf("Model error = %v, want ErrDisabled", err)
	}
}

// BGE is asymmetric, so embedding a query as a document silently costs
// retrieval quality. The kind has to survive the round trip.
func TestEmbedSendsTheRequestedKind(t *testing.T) {
	server := stubSidecar(t, nil)
	client := New(server.URL, time.Second)

	vectors, model, err := client.Embed(context.Background(), []string{"a", "bb"}, KindDocument)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if model != "test-model" {
		t.Errorf("model = %q, want test-model", model)
	}
	if len(vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vectors))
	}
	if vectors[0][0] != 0 {
		t.Error("document request arrived as a query")
	}

	queryVectors, _, err := client.Embed(context.Background(), []string{"a"}, KindQuery)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if queryVectors[0][0] != 1 {
		t.Error("query request arrived as a document")
	}
}

// The cache exists to keep the sidecar off the request path for repeated
// searches, which is most of them.
func TestEmbedQueryCachesByNormalizedQuery(t *testing.T) {
	var calls atomic.Int64
	server := stubSidecar(t, &calls)
	client := New(server.URL, time.Second)

	first, err := client.EmbedQuery(context.Background(), "tuition freeze")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	for _, variant := range []string{"tuition freeze", "  Tuition Freeze  ", "TUITION FREEZE"} {
		got, err := client.EmbedQuery(context.Background(), variant)
		if err != nil {
			t.Fatalf("EmbedQuery(%q): %v", variant, err)
		}
		if got[2] != first[2] {
			t.Errorf("EmbedQuery(%q) returned a different vector than the original", variant)
		}
	}

	if calls.Load() != 1 {
		t.Errorf("sidecar called %d times, want 1: case and whitespace should hit the cache", calls.Load())
	}
}

func TestEmbedQueryCacheEvictsOldestEntries(t *testing.T) {
	var calls atomic.Int64
	server := stubSidecar(t, &calls)
	client := New(server.URL, time.Second)

	for i := 0; i < queryCacheSize+10; i++ {
		if _, err := client.EmbedQuery(context.Background(), "query-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("EmbedQuery: %v", err)
		}
	}
	if got := client.order.Len(); got != queryCacheSize {
		t.Errorf("cache holds %d entries, want it bounded at %d", got, queryCacheSize)
	}
	if len(client.cache) != client.order.Len() {
		t.Errorf("cache map (%d) and eviction list (%d) disagree; entries are leaking", len(client.cache), client.order.Len())
	}

	// The oldest queries should have been evicted, so re-asking costs a call.
	before := calls.Load()
	if _, err := client.EmbedQuery(context.Background(), "query-0"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if calls.Load() != before+1 {
		t.Error("the oldest cache entry was not evicted")
	}
}

// Search treats any error as "serve lexical results", so errors must surface
// rather than be swallowed into a zero vector that would poison the ranking.
func TestEmbedQueryReportsSidecarFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	if _, err := New(server.URL, time.Second).EmbedQuery(context.Background(), "anything"); err == nil {
		t.Fatal("EmbedQuery succeeded against a sidecar that returned 503")
	}
}

// A short-count response would otherwise index out of range in the reconciler,
// which pairs vectors to articles positionally.
func TestEmbedRejectsAShortResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{
			Model: "test-model", Dimensions: 3, Vectors: [][]float32{{1, 2, 3}},
		})
	}))
	t.Cleanup(server.Close)

	if _, _, err := New(server.URL, time.Second).Embed(context.Background(), []string{"a", "b"}, KindDocument); err == nil {
		t.Fatal("Embed accepted 1 vector for 2 texts")
	}
}

func TestEmbedQueryIgnoresBlankQueries(t *testing.T) {
	var calls atomic.Int64
	server := stubSidecar(t, &calls)

	vector, err := New(server.URL, time.Second).EmbedQuery(context.Background(), "   ")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if vector != nil {
		t.Errorf("EmbedQuery returned %v for a blank query, want nil", vector)
	}
	if calls.Load() != 0 {
		t.Error("a blank query reached the sidecar")
	}
}
