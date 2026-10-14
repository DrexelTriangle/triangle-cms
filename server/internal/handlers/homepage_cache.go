package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"server/internal/metrics"
	"server/internal/models"
)

// The homepage is the most requested read and the most expensive one: settings,
// developing stories, six section listings with their authors and the featured
// lead, assembled serially on every request (~170ms at the median). Every
// visitor sees the same bytes, and Scalene's server-side fetch has no HTTP
// cache, so without a cache here each page render pays the whole assembly.
//
// homepageFreshTTL is how long one assembly is reused. It is shorter than the
// settings cache's 30s because nothing invalidates it: a pinned lead or a
// breaking banner reaches the site at most 15s after the save, which is less
// than the 60s the response already tells downstream caches they may keep it.
//
// homepageMaxStale bounds how long the last good homepage stands in for a
// failing database, measured from the load that produced it. Past it the error
// surfaces as it did before the cache existed.
//
// homepageLoadTimeout bounds a shared load. The load cannot run on any one
// caller's context (see responseCache), so it gets its own deadline instead;
// this is what keeps a hung database from leaking a goroutine per refresh.
const (
	homepageFreshTTL    = 15 * time.Second
	homepageMaxStale    = 5 * time.Minute
	homepageLoadTimeout = 10 * time.Second
)

var homepageCacheConfig = responseCacheConfig{
	fresh:       homepageFreshTTL,
	maxStale:    homepageMaxStale,
	loadTimeout: homepageLoadTimeout,
	now:         time.Now,
}

// cacheOutcome is how a request was answered; the values are the metric's
// label set, so it must stay a fixed, small set.
type cacheOutcome string

const (
	cacheHit      cacheOutcome = "hit"      // fresh entry, no load
	cacheMiss     cacheOutcome = "miss"     // this request started the load
	cacheShared   cacheOutcome = "shared"   // joined a load another request started
	cacheStale    cacheOutcome = "stale"    // load failed, served the last good entry
	cacheBypass   cacheOutcome = "bypass"   // non-canonical request, assembled directly
	cacheError    cacheOutcome = "error"    // load failed with nothing servable
	cacheCanceled cacheOutcome = "canceled" // caller left before the load finished
)

type responseCacheConfig struct {
	fresh       time.Duration
	maxStale    time.Duration
	loadTimeout time.Duration
	now         func() time.Time
}

// cachedResponse is one answer from the cache. body is a string, not a []byte
// or the assembled struct, so every caller can share it without a copy: Go
// strings are immutable, so no caller can change what the next one is served.
type cachedResponse struct {
	body     string
	loadedAt time.Time
	outcome  cacheOutcome
}

// responseCache holds one encoded response: the last successful load, reused
// while fresh and kept as a fallback for maxStale after that.
//
// Concurrent misses share one load. That load runs in its own goroutine on a
// context derived from context.Background with loadTimeout, never on a
// caller's: if it ran on the first caller's request context, that caller
// disconnecting would cancel the database queries every other waiter is
// waiting on. Each caller waits on its own context instead, so a caller that
// leaves returns at once and the load carries on for the rest (and still fills
// the cache if they all leave). The load is bounded by loadTimeout either way.
type responseCache struct {
	cfg  responseCacheConfig
	load func(context.Context) (string, error)

	mu       sync.Mutex
	body     string // last successful load; "" until there is one
	loadedAt time.Time
	call     *cacheCall // the load in flight, if any
}

type cacheCall struct {
	done    chan struct{}
	waiters int // guarded by responseCache.mu

	// Written once before done is closed, read only after.
	body     string
	loadedAt time.Time
	err      error
}

var errEmptyCacheLoad = errors.New("homepage cache: load returned an empty response")

func newResponseCache(cfg responseCacheConfig, load func(context.Context) (string, error)) *responseCache {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &responseCache{cfg: cfg, load: load}
}

func (c *responseCache) get(ctx context.Context) (cachedResponse, error) {
	c.mu.Lock()
	if c.body != "" && c.cfg.now().Sub(c.loadedAt) < c.cfg.fresh {
		res := cachedResponse{body: c.body, loadedAt: c.loadedAt, outcome: cacheHit}
		c.mu.Unlock()
		return res, nil
	}
	outcome := cacheShared
	call := c.call
	if call == nil {
		call = &cacheCall{done: make(chan struct{})}
		c.call = call
		outcome = cacheMiss
		go c.run(call)
	}
	call.waiters++
	c.mu.Unlock()

	select {
	case <-call.done:
	case <-ctx.Done():
		c.mu.Lock()
		call.waiters--
		c.mu.Unlock()
		return cachedResponse{}, ctx.Err()
	}
	c.mu.Lock()
	call.waiters--
	c.mu.Unlock()

	if call.err == nil {
		return cachedResponse{body: call.body, loadedAt: call.loadedAt, outcome: outcome}, nil
	}
	c.mu.Lock()
	body, loadedAt := c.body, c.loadedAt
	c.mu.Unlock()
	if body != "" && c.cfg.now().Sub(loadedAt) < c.cfg.maxStale {
		return cachedResponse{body: body, loadedAt: loadedAt, outcome: cacheStale}, nil
	}
	return cachedResponse{}, call.err
}

// run performs one shared load and publishes it. The entry is timestamped
// with when the load started, not when it finished, because that is the
// oldest the data in it can be.
func (c *responseCache) run(call *cacheCall) {
	started := c.cfg.now()
	body, err := c.loadBounded()
	if err == nil && body == "" {
		err = errEmptyCacheLoad
	}
	if err != nil {
		slog.Warn("homepage cache load failed", "error", err)
	}

	c.mu.Lock()
	if err == nil {
		c.body, c.loadedAt = body, started
	}
	call.body, call.loadedAt, call.err = body, started, err
	c.call = nil
	c.mu.Unlock()
	close(call.done)
}

// loadBounded runs the loader on its own deadline. A panic is turned into an
// error: the load runs outside the request goroutine, where the Recovery
// middleware cannot catch it and a panic would take the process down.
func (c *responseCache) loadBounded() (body string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.loadTimeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			slog.Error("panic recovered", "error", p, "where", "homepage cache load", "stack_trace", string(debug.Stack()))
			body, err = "", fmt.Errorf("homepage cache load panicked: %v", p)
		}
	}()
	return c.load(ctx)
}

// waiting reports how many callers are blocked on the load in flight.
func (c *responseCache) waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.call == nil {
		return 0
	}
	return c.call.waiters
}

func (c *responseCache) loading() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.call != nil
}

// homepageCacheControl is publicReadCacheControl for a response whose data is
// already age old.
//
// A downstream cache counts its freshness from when it receives a response.
// Handing it the full max-age on a body that sat here for 14s would let it
// keep the homepage until 74s after the data was read, past the 60s bound
// publicReadCacheControl promises. Both lifetimes are shortened by the age
// instead, so fresh-until and servable-until land where they would have for a
// response assembled on the spot. An Age header would say the same thing but
// is not sent alongside this: a cache that honoured both would count the age
// twice.
//
// The age is truncated to whole seconds, the resolution of the Age header, so
// a response served within a second of its load carries the original header
// unchanged.
func homepageCacheControl(age time.Duration) string {
	elapsed := max(int(age/time.Second), 0)
	maxAge := max(publicReadMaxAge-elapsed, 0)
	staleWhileRevalidate := max(publicReadMaxAge+publicReadStaleWhileRevalidate-elapsed-maxAge, 0)
	return fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d", maxAge, staleWhileRevalidate)
}

// isCanonicalHomepageRequest reports whether a request is the one Scalene
// makes, a bare GET /v1/homepage. Only that request is cached.
//
// The homepage response is shaped by far more than offset and excerpt_words:
// queryArticles and articleQueryFilters read sort_by, sort_direction, title,
// type, slug and more from the URL. Rather than keep a cache key in step with
// every parameter those helpers might learn, any query string at all goes
// straight to the database, so a request can never be handed another shape.
func isCanonicalHomepageRequest(r *http.Request) bool {
	return r.URL.RawQuery == ""
}

// newHomepageHandler serves canonical requests from a responseCache over
// assemble and everything else from assemble directly.
//
// The cached entry is assembled from a synthetic bare request rather than from
// whichever request happened to miss: it carries no query string and no
// identity, so the shared entry is always the anonymous canonical homepage.
func newHomepageHandler(assemble func(*http.Request) (models.HomepageResponse, error), cfg responseCacheConfig) http.HandlerFunc {
	cache := newResponseCache(cfg, func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/homepage", nil)
		if err != nil {
			return "", err
		}
		homepage, err := assemble(req)
		if err != nil {
			return "", err
		}
		// Encoded exactly as writeJSON encodes, trailing newline included, so a
		// cached body is byte-identical to an uncached one.
		var body strings.Builder
		if err := json.NewEncoder(&body).Encode(homepage); err != nil {
			return "", err
		}
		return body.String(), nil
	})

	return func(w http.ResponseWriter, r *http.Request) {
		if !isCanonicalHomepageRequest(r) {
			metrics.HomepageCacheRequests.WithLabelValues(string(cacheBypass)).Inc()
			homepage, err := assemble(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			setAlwaysPublicCache(w)
			writeJSON(w, http.StatusOK, homepage)
			return
		}

		res, err := cache.get(r.Context())
		if err != nil {
			outcome := cacheError
			if r.Context().Err() != nil {
				outcome = cacheCanceled
			}
			metrics.HomepageCacheRequests.WithLabelValues(string(outcome)).Inc()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		metrics.HomepageCacheRequests.WithLabelValues(string(res.outcome)).Inc()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", homepageCacheControl(cache.cfg.now().Sub(res.loadedAt)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, res.body)
	}
}
