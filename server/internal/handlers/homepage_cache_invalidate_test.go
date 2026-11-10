package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHomepageCache_InvalidateForcesReloadWithinFreshTTL(t *testing.T) {
	clock := newFakeClock()
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		n := loads.Add(1)
		return []string{"", "v1", "v2"}[n], nil
	})

	if res, err := c.get(context.Background()); err != nil || res.body != "v1" {
		t.Fatalf("first get = %q, %v", res.body, err)
	}
	clock.Advance(time.Second) // well inside the fresh TTL
	c.invalidate()
	res, err := c.get(context.Background())
	if err != nil || res.body != "v2" || res.outcome != cacheMiss {
		t.Fatalf("get after invalidate = %q (%s), %v; want a fresh load of v2", res.body, res.outcome, err)
	}
	if res, _ := c.get(context.Background()); res.outcome != cacheHit || res.body != "v2" {
		t.Fatalf("the reload was not cached: %q (%s)", res.body, res.outcome)
	}
}

// The race this guards: a load reads the database, an editor saves, the load
// finishes. Its body predates the save and must not be served as fresh.
func TestHomepageCache_LoadInFlightAtInvalidateIsNotKeptOrJoined(t *testing.T) {
	clock := newFakeClock()
	release := make(chan struct{})
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		if loads.Add(1) == 1 {
			<-release
			return "before-save", nil
		}
		return "after-save", nil
	})

	first := make(chan cachedResponse, 1)
	go func() {
		res, _ := c.get(context.Background())
		first <- res
	}()
	waitFor(t, func() bool { return c.waiting() == 1 })

	c.invalidate()

	// A caller arriving after the invalidate must start its own load rather
	// than wait on the one that read the old data.
	res, err := c.get(context.Background())
	if err != nil || res.body != "after-save" {
		t.Fatalf("post-invalidate get = %q, %v; want after-save", res.body, err)
	}

	close(release)
	if got := <-first; got.body != "before-save" {
		t.Fatalf("the waiter already on the old load got %q", got.body)
	}
	// The old load finishing last must not overwrite the newer body.
	if res, _ := c.get(context.Background()); res.body != "after-save" || res.outcome != cacheHit {
		t.Fatalf("after the stale load finished, get = %q (%s); want cached after-save", res.body, res.outcome)
	}
}

func TestHomepageCache_InvalidateKeepsLastGoodAsStaleFallback(t *testing.T) {
	clock := newFakeClock()
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		if loads.Add(1) == 1 {
			return "good", nil
		}
		return "", errors.New("database down")
	})
	if _, err := c.get(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.invalidate()
	res, err := c.get(context.Background())
	if err != nil || res.body != "good" || res.outcome != cacheStale {
		t.Fatalf("get = %q (%s), %v; want the last good body as stale", res.body, res.outcome, err)
	}
}

func TestInvalidateHomepageCaches_ReachesRegisteredHandlerCache(t *testing.T) {
	homepageCachesMu.Lock()
	before := len(homepageCaches)
	homepageCachesMu.Unlock()

	_ = newHomepageHandler(nil, homepageCacheConfig)

	homepageCachesMu.Lock()
	registered := homepageCaches[before:]
	homepageCachesMu.Unlock()
	if len(registered) != 1 {
		t.Fatalf("newHomepageHandler registered %d caches, want 1", len(registered))
	}
	c := registered[0]
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()

	InvalidateHomepageCaches()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen+1 {
		t.Fatalf("gen = %d after InvalidateHomepageCaches, want %d", c.gen, gen+1)
	}
}
