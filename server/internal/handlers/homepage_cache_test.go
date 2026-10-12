package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"server/internal/metrics"
	"server/internal/middleware"
	"server/internal/models"

	dto "github.com/prometheus/client_model/go"
)

// fakeClock is a settable clock so expiry is tested without sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func testCacheConfig(clock *fakeClock) responseCacheConfig {
	return responseCacheConfig{
		fresh:       homepageFreshTTL,
		maxStale:    homepageMaxStale,
		loadTimeout: time.Second,
		now:         clock.Now,
	}
}

func sampleHomepage(tag string) models.HomepageResponse {
	published := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	return models.HomepageResponse{
		BreakingNews: models.BreakingNewsSettings{Enabled: true, Text: "Breaking " + tag, Items: []models.BreakingNewsItem{{Text: "Breaking " + tag}}},
		Carousel:     []models.HomepageCarouselSlide{{Enabled: true, Title: "Slide <" + tag + ">"}},
		DevelopingStories: []models.HomepageDevelopingStory{
			{Slug: "story", Title: "Story " + tag, Label: []models.HomepageLabel{}},
		},
		News: []models.ArticleListItem{{
			ID: 1, Title: "Lead " + tag, Slug: "lead-" + tag,
			Authors:    []models.AuthorSummary{{Name: "Reporter"}},
			Categories: []models.CategorySummary{{Name: "News", Slug: "news"}},
			Excerpt:    "Excerpt & more", PublishedDate: &published,
		}},
		Opinion: []models.ArticleListItem{}, Sports: []models.ArticleListItem{},
		Entertainment: []models.ArticleListItem{}, CAndP: []models.ArticleListItem{}, Columns: []models.ArticleListItem{},
	}
}

// countingAssembler stands in for the database assembly and records every
// request it is asked to assemble.
type countingAssembler struct {
	mu       sync.Mutex
	calls    int
	requests []*http.Request
	fn       func(r *http.Request, call int) (models.HomepageResponse, error)
}

func (a *countingAssembler) assemble(r *http.Request) (models.HomepageResponse, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.requests = append(a.requests, r)
	fn := a.fn
	a.mu.Unlock()
	if fn == nil {
		return sampleHomepage("v" + fmt.Sprint(call)), nil
	}
	return fn(r, call)
}

func (a *countingAssembler) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func serveHomepage(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// encodedLikeWriteJSON is the exact byte stream the uncached handler wrote.
func encodedLikeWriteJSON(t *testing.T, v any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, v)
	return rec.Body.String()
}

func TestHomepageCache_RepeatedCanonicalRequestsAssembleOnceWithinTTL(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))

	first := serveHomepage(t, h, "/v1/homepage")
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", first.Code, first.Body.String())
	}
	if got, want := first.Body.String(), encodedLikeWriteJSON(t, sampleHomepage("v1")); got != want {
		t.Fatalf("cached body differs from the uncached encoding\n got: %s\nwant: %s", got, want)
	}
	if got := first.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	for i := range 99 {
		clock.Advance(100 * time.Millisecond) // 9.9s total, inside the 15s TTL
		rec := serveHomepage(t, h, "/v1/homepage")
		if rec.Code != http.StatusOK || rec.Body.String() != first.Body.String() {
			t.Fatalf("request %d: status %d, body changed", i+2, rec.Code)
		}
	}
	if got := asm.count(); got != 1 {
		t.Fatalf("100 canonical requests inside the TTL ran %d assemblies, want 1", got)
	}
}

func TestHomepageCache_ReassemblesAfterFreshTTL(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))

	serveHomepage(t, h, "/v1/homepage")
	clock.Advance(homepageFreshTTL - time.Millisecond)
	if rec := serveHomepage(t, h, "/v1/homepage"); !strings.Contains(rec.Body.String(), "Lead v1") {
		t.Fatalf("just inside the TTL should still be the first assembly: %s", rec.Body.String())
	}
	clock.Advance(time.Millisecond)
	rec := serveHomepage(t, h, "/v1/homepage")
	if !strings.Contains(rec.Body.String(), "Lead v2") {
		t.Fatalf("at the TTL boundary the homepage should be reassembled: %s", rec.Body.String())
	}
	if got := asm.count(); got != 2 {
		t.Fatalf("assemblies = %d, want 2", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != publicReadCacheControl {
		t.Errorf("a freshly assembled response should carry the full bound: %q", got)
	}
}

func TestHomepageCache_ConcurrentColdLoadsShareOneLoad(t *testing.T) {
	clock := newFakeClock()
	release := make(chan struct{})
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		loads.Add(1)
		<-release
		return "shared-body", nil
	})

	const callers = 25
	results := make(chan cachedResponse, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			res, err := c.get(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- res
		})
	}
	waitFor(t, func() bool { return c.waiting() == callers })
	close(release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("caller failed: %v", err)
	}
	outcomes := map[cacheOutcome]int{}
	for res := range results {
		if res.body != "shared-body" {
			t.Fatalf("caller got %q", res.body)
		}
		outcomes[res.outcome]++
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("%d concurrent cold callers ran %d loads, want 1", callers, got)
	}
	if outcomes[cacheMiss] != 1 || outcomes[cacheShared] != callers-1 {
		t.Fatalf("outcomes = %v, want 1 miss and %d shared", outcomes, callers-1)
	}
}

func TestHomepageCache_ServesLastGoodWhenRefreshFailsForAtMostFiveMinutes(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{fn: func(r *http.Request, call int) (models.HomepageResponse, error) {
		if call == 1 {
			return sampleHomepage("good"), nil
		}
		return models.HomepageResponse{}, errors.New("db down")
	}}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))
	good := serveHomepage(t, h, "/v1/homepage").Body.String()

	clock.Advance(homepageFreshTTL + time.Second) // 16s: past fresh, refresh fails
	rec := serveHomepage(t, h, "/v1/homepage")
	if rec.Code != http.StatusOK || rec.Body.String() != good {
		t.Fatalf("failed refresh should serve the last good response, got %d %s", rec.Code, rec.Body.String())
	}
	// The data is 16s old, so downstream may keep it fresh for the 44s left of
	// the 60s bound, not a full 60s from now.
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=44, stale-while-revalidate=300"; got != want {
		t.Errorf("stale Cache-Control = %q, want %q", got, want)
	}

	// Errors are not cached: every request past the fresh window retries.
	before := asm.count()
	serveHomepage(t, h, "/v1/homepage")
	if asm.count() != before+1 {
		t.Fatalf("a failed refresh must not be cached; assemblies %d -> %d", before, asm.count())
	}

	clock.Advance(homepageMaxStale - homepageFreshTTL - 2*time.Second) // 4m59s since the good load
	rec = serveHomepage(t, h, "/v1/homepage")
	if rec.Code != http.StatusOK || rec.Body.String() != good {
		t.Fatalf("still inside the stale bound, got %d", rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=0, stale-while-revalidate=61"; got != want {
		t.Errorf("Cache-Control at 299s = %q, want %q", got, want)
	}

	clock.Advance(time.Second) // exactly 5m since the good load
	rec = serveHomepage(t, h, "/v1/homepage")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("past the stale bound the error must surface, got %d %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), encodedLikeWriteJSON(t, models.ErrorResponse{Error: "db down"}); got != want {
		t.Errorf("error body = %q, want %q", got, want)
	}
}

func TestHomepageCache_ColdFailureReturns500AndIsNotCached(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{fn: func(r *http.Request, call int) (models.HomepageResponse, error) {
		if call == 1 {
			return models.HomepageResponse{}, errors.New("connection refused")
		}
		return sampleHomepage("recovered"), nil
	}}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))

	rec := serveHomepage(t, h, "/v1/homepage")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("cold failure status = %d, want 500", rec.Code)
	}
	if got, want := rec.Body.String(), encodedLikeWriteJSON(t, models.ErrorResponse{Error: "connection refused"}); got != want {
		t.Errorf("cold failure body = %q, want %q (the existing writeError shape)", got, want)
	}
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("an error must not be marked cacheable, got Cache-Control %q", got)
	}

	rec = serveHomepage(t, h, "/v1/homepage")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Lead recovered") {
		t.Fatalf("the next request must retry rather than replay the error: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHomepageCache_EmptyLoadIsAnErrorNotASuccess(t *testing.T) {
	clock := newFakeClock()
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		loads.Add(1)
		return "", nil
	})
	if _, err := c.get(context.Background()); err == nil {
		t.Fatal("an empty body must not be returned as a success")
	}
	if _, err := c.get(context.Background()); err == nil {
		t.Fatal("an empty body must not be cached")
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("loads = %d, want 2 (nothing cached)", got)
	}
}

func TestHomepageCache_NonCanonicalRequestsBypassTheCache(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{fn: func(r *http.Request, call int) (models.HomepageResponse, error) {
		// The shape is whatever the request asked for, so a mixed-up cache
		// entry is visible in the body.
		return sampleHomepage("q=" + r.URL.RawQuery), nil
	}}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))

	canonical := serveHomepage(t, h, "/v1/homepage").Body.String()
	if !strings.Contains(canonical, `"Lead q="`) {
		t.Fatalf("canonical load should assemble the query-less request: %s", canonical)
	}

	for _, target := range []string{
		"/v1/homepage?offset=20",
		"/v1/homepage?page=2",
		"/v1/homepage?excerpt_words=10",
		"/v1/homepage?excerpt_words=50",
		"/v1/homepage?sort_by=title",
		"/v1/homepage?title=foo",
		"/v1/homepage?limit=5",
	} {
		before := asm.count()
		rec := serveHomepage(t, h, target)
		query := strings.TrimPrefix(target, "/v1/homepage?")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"Lead q=`+query+`"`) {
			t.Fatalf("%s got the wrong shape: %s", target, rec.Body.String())
		}
		if asm.count() != before+1 {
			t.Fatalf("%s should assemble its own response", target)
		}
		if got := rec.Header().Get("Cache-Control"); got != publicReadCacheControl {
			t.Errorf("%s Cache-Control = %q, want unchanged %q", target, got, publicReadCacheControl)
		}
	}

	// None of the bypassed shapes leaked into the canonical entry.
	before := asm.count()
	if got := serveHomepage(t, h, "/v1/homepage").Body.String(); got != canonical {
		t.Fatalf("canonical entry changed after non-canonical requests: %s", got)
	}
	if asm.count() != before {
		t.Fatal("canonical request inside the TTL should still be a hit")
	}
}

func TestHomepageCache_SharedLoadUsesCanonicalAnonymousRequest(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))

	req := httptest.NewRequest(http.MethodGet, "/v1/homepage", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleEditor}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	asm.mu.Lock()
	loaded := asm.requests[0]
	asm.mu.Unlock()
	if loaded == req {
		t.Fatal("the shared load must not run on the triggering request")
	}
	if loaded.URL.Path != "/v1/homepage" || loaded.URL.RawQuery != "" || loaded.Method != http.MethodGet {
		t.Fatalf("shared load request = %s %s", loaded.Method, loaded.URL)
	}
	if _, ok := middleware.UserFromContext(loaded.Context()); ok {
		t.Fatal("the shared, public entry must never be assembled with a caller's identity")
	}
}

func TestHomepageCache_DisconnectedWaiterDoesNotCancelSharedLoad(t *testing.T) {
	clock := newFakeClock()
	release := make(chan struct{})
	loadCtxErr := make(chan error, 1)
	type ctxKey struct{}
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		if ctx.Value(ctxKey{}) != nil {
			t.Error("the shared load must not inherit a caller's context")
		}
		<-release
		loadCtxErr <- ctx.Err()
		return "body", nil
	})

	firstCtx, cancelFirst := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "first"))
	firstErr := make(chan error, 1)
	go func() {
		_, err := c.get(firstCtx)
		firstErr <- err
	}()
	waitFor(t, func() bool { return c.waiting() == 1 })

	secondRes := make(chan cachedResponse, 1)
	go func() {
		res, err := c.get(context.Background())
		if err != nil {
			t.Errorf("second caller: %v", err)
		}
		secondRes <- res
	}()
	waitFor(t, func() bool { return c.waiting() == 2 })

	cancelFirst()
	select {
	case err := <-firstErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("disconnected waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a disconnected waiter must return without waiting for the load")
	}

	close(release)
	if err := <-loadCtxErr; err != nil {
		t.Fatalf("the shared load's context was cancelled by a departing waiter: %v", err)
	}
	if res := <-secondRes; res.body != "body" {
		t.Fatalf("remaining waiter got %q", res.body)
	}
}

func TestHomepageCache_AbandonedLoadStillCompletesAndPopulates(t *testing.T) {
	clock := newFakeClock()
	release := make(chan struct{})
	done := make(chan struct{})
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		loads.Add(1)
		defer close(done)
		<-release
		return "body", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.get(ctx)
		errCh <- err
	}()
	waitFor(t, func() bool { return c.waiting() == 1 })
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(release)
	<-done
	waitFor(t, func() bool { return c.waiting() == 0 && !c.loading() })

	res, err := c.get(context.Background())
	if err != nil || res.body != "body" || res.outcome != cacheHit {
		t.Fatalf("after an abandoned load: %+v, %v; want a hit", res, err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loads = %d, want 1", loads.Load())
	}
}

func TestHomepageCache_LoadIsBoundedByItsOwnTimeout(t *testing.T) {
	clock := newFakeClock()
	cfg := testCacheConfig(clock)
	cfg.loadTimeout = 50 * time.Millisecond
	exited := make(chan struct{})
	c := newResponseCache(cfg, func(ctx context.Context) (string, error) {
		defer close(exited)
		<-ctx.Done() // a hung database
		return "", ctx.Err()
	})

	start := time.Now()
	_, err := c.get(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the load's deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hung load held the caller for %v", elapsed)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the load goroutine did not exit after its deadline")
	}
	waitFor(t, func() bool { return !c.loading() })
}

func TestHomepageCache_LoaderPanicIsAnErrorNotACrash(t *testing.T) {
	clock := newFakeClock()
	var loads atomic.Int32
	c := newResponseCache(testCacheConfig(clock), func(ctx context.Context) (string, error) {
		if loads.Add(1) == 1 {
			panic("boom")
		}
		return "body", nil
	})
	if _, err := c.get(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the recovered panic", err)
	}
	if res, err := c.get(context.Background()); err != nil || res.body != "body" {
		t.Fatalf("after a panic the next call should retry: %+v %v", res, err)
	}
}

func TestHomepageCache_CachedBodyIsIndependentOfAssembledValues(t *testing.T) {
	clock := newFakeClock()
	shared := sampleHomepage("original")
	h := newHomepageHandler(func(r *http.Request) (models.HomepageResponse, error) {
		return shared, nil
	}, testCacheConfig(clock))

	first := serveHomepage(t, h, "/v1/homepage").Body.String()
	// Mutate everything the assembled value shares by reference.
	shared.News[0].Title = "MUTATED"
	shared.News[0].Authors[0].Name = "MUTATED"
	shared.Carousel[0].Title = "MUTATED"
	shared.BreakingNews.Items[0].Text = "MUTATED"

	second := serveHomepage(t, h, "/v1/homepage").Body.String()
	if second != first || strings.Contains(second, "MUTATED") {
		t.Fatalf("a hit reflected a later mutation of the assembled value:\n%s", second)
	}
}

func TestHomepageCacheControl_NeverExtendsTheOriginBound(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{0, publicReadCacheControl},
		{999 * time.Millisecond, publicReadCacheControl},
		{time.Second, "public, max-age=59, stale-while-revalidate=300"},
		{14 * time.Second, "public, max-age=46, stale-while-revalidate=300"},
		{60 * time.Second, "public, max-age=0, stale-while-revalidate=300"},
		{100 * time.Second, "public, max-age=0, stale-while-revalidate=260"},
		{homepageMaxStale, "public, max-age=0, stale-while-revalidate=60"},
		{-time.Second, publicReadCacheControl}, // clock step backwards
	}
	for _, tc := range cases {
		if got := homepageCacheControl(tc.age); got != tc.want {
			t.Errorf("homepageCacheControl(%v) = %q, want %q", tc.age, got, tc.want)
		}
	}

	// Whatever the age, a downstream cache can keep the data fresh no later
	// than 60s, and serve it stale no later than 360s, after it was assembled:
	// the same bound an uncached response has.
	for age := time.Duration(0); age <= homepageMaxStale; age += 250 * time.Millisecond {
		var maxAge, swr int
		if _, err := fmt.Sscanf(homepageCacheControl(age), "public, max-age=%d, stale-while-revalidate=%d", &maxAge, &swr); err != nil {
			t.Fatalf("unparseable header at %v: %v", age, err)
		}
		// Age is truncated to whole seconds (the Age header's resolution), so
		// the bound holds to within a second. Once max-age is 0 the response
		// arrives already stale and there is no fresh lifetime to extend.
		if maxAge > 0 && age+time.Duration(maxAge)*time.Second >= time.Duration(publicReadMaxAge+1)*time.Second {
			t.Fatalf("age %v: fresh until %ds after assembly, beyond %ds", age, int(age.Seconds())+maxAge, publicReadMaxAge)
		}
		if limit := time.Duration(publicReadMaxAge+publicReadStaleWhileRevalidate) * time.Second; age+time.Duration(maxAge+swr)*time.Second > limit+time.Second {
			t.Fatalf("age %v: servable until %v after assembly, beyond %v", age, age+time.Duration(maxAge+swr)*time.Second, limit)
		}
	}
}

func TestHomepageCache_HitHeaderAccountsForTimeInCache(t *testing.T) {
	clock := newFakeClock()
	h := newHomepageHandler((&countingAssembler{}).assemble, testCacheConfig(clock))

	if got := serveHomepage(t, h, "/v1/homepage").Header().Get("Cache-Control"); got != publicReadCacheControl {
		t.Fatalf("miss Cache-Control = %q, want %q", got, publicReadCacheControl)
	}
	clock.Advance(10 * time.Second)
	rec := serveHomepage(t, h, "/v1/homepage")
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=50, stale-while-revalidate=300"; got != want {
		t.Fatalf("hit 10s after assembly: Cache-Control = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Age"); got != "" {
		t.Fatalf("Age must not be sent alongside a reduced max-age (it would be double-counted): %q", got)
	}
}

func TestHomepageCache_RecordsBoundedOutcomeMetrics(t *testing.T) {
	clock := newFakeClock()
	asm := &countingAssembler{fn: func(r *http.Request, call int) (models.HomepageResponse, error) {
		if call >= 3 {
			return models.HomepageResponse{}, errors.New("db down")
		}
		return sampleHomepage("m"), nil
	}}
	h := newHomepageHandler(asm.assemble, testCacheConfig(clock))
	read := func(outcome string) float64 {
		var m dto.Metric
		if err := metrics.HomepageCacheRequests.WithLabelValues(outcome).Write(&m); err != nil {
			t.Fatalf("read %s counter: %v", outcome, err)
		}
		return m.GetCounter().GetValue()
	}
	before := map[string]float64{}
	for _, o := range []string{"miss", "hit", "bypass", "stale", "error"} {
		before[o] = read(o)
	}

	serveHomepage(t, h, "/v1/homepage")           // miss (call 1)
	serveHomepage(t, h, "/v1/homepage")           // hit
	serveHomepage(t, h, "/v1/homepage?offset=20") // bypass (call 2)
	clock.Advance(homepageFreshTTL)               //
	serveHomepage(t, h, "/v1/homepage")           // stale (call 3 fails)
	clock.Advance(homepageMaxStale)               //
	serveHomepage(t, h, "/v1/homepage")           // error (call 4 fails, nothing servable)

	for o, want := range map[string]float64{"miss": 1, "hit": 1, "bypass": 1, "stale": 1, "error": 1} {
		if got := read(o) - before[o]; got != want {
			t.Errorf("outcome %q incremented by %v, want %v", o, got, want)
		}
	}

	// A caller that disconnects mid-load is not a backend error.
	release := make(chan struct{})
	slow := newHomepageHandler(func(*http.Request) (models.HomepageResponse, error) {
		<-release
		return sampleHomepage("slow"), nil
	}, testCacheConfig(clock))
	defer close(release)
	canceledBefore, errorBefore := read("canceled"), read("error")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	slow.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/homepage", nil).WithContext(ctx))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("canceled caller status = %d", rec.Code)
	}
	if read("canceled")-canceledBefore != 1 || read("error") != errorBefore {
		t.Errorf("a canceled caller should count as canceled, not error")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// BenchmarkHomepageCanonicalRequests shows the request-count effect: with a
// 5ms stand-in assembly, b.N canonical requests inside one TTL cost one
// assembly rather than b.N.
func BenchmarkHomepageCanonicalRequests(b *testing.B) {
	for _, mode := range []string{"cached", "uncached"} {
		b.Run(mode, func(b *testing.B) {
			clock := newFakeClock()
			var assemblies atomic.Int64
			assemble := func(r *http.Request) (models.HomepageResponse, error) {
				assemblies.Add(1)
				time.Sleep(5 * time.Millisecond)
				return sampleHomepage("bench"), nil
			}
			target := "/v1/homepage"
			if mode == "uncached" {
				target = "/v1/homepage?offset=0" // any query bypasses the cache
			}
			h := newHomepageHandler(assemble, responseCacheConfig{fresh: homepageFreshTTL, maxStale: homepageMaxStale, loadTimeout: time.Second, now: clock.Now})
			req := httptest.NewRequest(http.MethodGet, target, nil)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatal(rec.Code)
				}
			}
			b.ReportMetric(float64(assemblies.Load())/float64(b.N), "assemblies/op")
		})
	}
}
