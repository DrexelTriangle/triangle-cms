// Package embeddings talks to the embedding sidecar.
//
// Two callers, with different tolerances. The reconciler embeds article bodies
// in the background, where slow is fine and wrong is not. Search embeds the
// user's query on the request path, where the opposite holds: a search that
// waits on a cold model is worse than one that quietly returns lexical results.
// Both go through here so the vectors are guaranteed to come from one model.
package embeddings

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Kind selects the sidecar's prefixing. BGE is asymmetric: a query embedded as
// a document still returns a vector, just a measurably worse one.
type Kind string

const (
	KindQuery    Kind = "query"
	KindDocument Kind = "document"
)

// ErrDisabled is returned when no sidecar is configured. Callers treat it as
// "skip the vector half", not as a failure.
var ErrDisabled = errors.New("embeddings: no sidecar configured")

// queryCacheSize bounds the query-embedding cache. Newsroom search traffic is
// heavily repeated (the same handful of terms, plus every prefix of whatever
// someone is currently typing) so a small cache removes most sidecar round
// trips from the request path.
const queryCacheSize = 512

type Client struct {
	baseURL string
	http    *http.Client

	mu    sync.Mutex
	cache map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	key    string
	vector []float32
}

// New returns a client for baseURL. An empty baseURL yields a disabled client
// whose calls all return ErrDisabled, which is how a deployment without the
// sidecar runs lexical-only search without any other code changing.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout},
		cache:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

// Enabled reports whether a sidecar is configured.
func (c *Client) Enabled() bool { return c != nil && c.baseURL != "" }

type healthResponse struct {
	Status     string `json:"status"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// Model reports which model the sidecar is serving. The reconciler needs this
// before it embeds anything, to ask the database which articles were embedded
// with a *different* model and therefore need redoing.
func (c *Client) Model(ctx context.Context) (string, int, error) {
	if !c.Enabled() {
		return "", 0, ErrDisabled
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return "", 0, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("embeddings: sidecar health returned %s", resp.Status)
	}

	var decoded healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", 0, err
	}
	return decoded.Model, decoded.Dimensions, nil
}

type embedRequest struct {
	Texts []string `json:"texts"`
	Kind  Kind     `json:"kind"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Dimensions int         `json:"dimensions"`
	Vectors    [][]float32 `json:"vectors"`
}

// Embed returns one vector per text, plus the model that produced them. The
// model name is not decoration: it is stored alongside each vector so a model
// swap is detectable and re-embeddable rather than silently mixing two
// incompatible vector spaces in the same index.
func (c *Client) Embed(ctx context.Context, texts []string, kind Kind) ([][]float32, string, error) {
	if !c.Enabled() {
		return nil, "", ErrDisabled
	}
	if len(texts) == 0 {
		return nil, "", nil
	}

	body, err := json.Marshal(embedRequest{Texts: texts, Kind: kind})
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("embeddings: sidecar returned %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var decoded embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, "", err
	}
	if len(decoded.Vectors) != len(texts) {
		return nil, "", fmt.Errorf("embeddings: asked for %d vectors, got %d", len(texts), len(decoded.Vectors))
	}

	return decoded.Vectors, decoded.Model, nil
}

// EmbedQuery embeds a single search query, caching the result. Errors are the
// caller's cue to fall back to lexical search, so they are returned rather than
// swallowed here.
func (c *Client) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}

	key := strings.ToLower(strings.TrimSpace(query))
	if key == "" {
		return nil, nil
	}
	if cached, ok := c.lookup(key); ok {
		return cached, nil
	}

	vectors, _, err := c.Embed(ctx, []string{query}, KindQuery)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embeddings: sidecar returned no vector for the query")
	}

	c.store(key, vectors[0])
	return vectors[0], nil
}

func (c *Client) lookup(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*cacheEntry).vector, true
}

func (c *Client) store(key string, vector []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.cache[key]; ok {
		c.order.MoveToFront(element)
		element.Value.(*cacheEntry).vector = vector
		return
	}

	c.cache[key] = c.order.PushFront(&cacheEntry{key: key, vector: vector})
	for c.order.Len() > queryCacheSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.cache, oldest.Value.(*cacheEntry).key)
	}
}
