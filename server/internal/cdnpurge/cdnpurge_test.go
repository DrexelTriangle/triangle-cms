package cdnpurge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCloudflare struct {
	t      *testing.T
	mu     sync.Mutex
	calls  [][]string
	status []int // per call; 200 once exhausted
	body   string
	server *httptest.Server
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	f := &fakeCloudflare{t: t}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/zone-1/purge_cache" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		var payload struct {
			Files []string `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, payload.Files)
		status := http.StatusOK
		if len(f.status) > 0 {
			status, f.status = f.status[0], f.status[1:]
		}
		body := f.body
		f.mu.Unlock()
		w.WriteHeader(status)
		if body == "" {
			body = fmt.Sprintf(`{"success":%t}`, status == http.StatusOK)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCloudflare) snapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func newTestPurger(t *testing.T, f *fakeCloudflare, before func()) *Purger {
	t.Helper()
	p, err := New(Config{
		ZoneID: "zone-1", APIToken: "tok", SiteURL: "https://www.thetriangle.org",
		Endpoint: f.server.URL, Delay: 10 * time.Millisecond, BeforeFlush: before,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = time.Millisecond
	return p
}

func TestNilPurgerIsInert(t *testing.T) {
	var p *Purger
	if p.Enabled() {
		t.Fatal("nil purger reports enabled")
	}
	p.Enqueue("/x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx) // must return, not panic
}

func TestNewRejectsIncompleteConfig(t *testing.T) {
	for _, cfg := range []Config{
		{APIToken: "t", SiteURL: "https://www.thetriangle.org"},
		{ZoneID: "z", SiteURL: "https://www.thetriangle.org"},
		{ZoneID: "z", APIToken: "t", SiteURL: "www.thetriangle.org"},
		{ZoneID: "z", APIToken: "t", SiteURL: "ftp://www.thetriangle.org"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestEnqueueResolvesPathsAndDropsForeignHosts(t *testing.T) {
	f := newFakeCloudflare(t)
	p := newTestPurger(t, f, nil)
	p.Enqueue(
		"/article/a",
		"/article/a", // duplicate
		"https://www.thetriangle.org/wp-content/uploads/2026/11/x.jpg",
		"https://WWW.thetriangle.org/news#frag",
		"https://delta.thetriangle.org/media/y.jpg", // other host: Cloudflare would reject the batch
		"https://example.com/z",
		"",
		"  ",
	)
	p.flush(context.Background(), true)

	calls := f.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want one", calls)
	}
	want := []string{
		"https://www.thetriangle.org/article/a",
		"https://www.thetriangle.org/news",
		"https://www.thetriangle.org/wp-content/uploads/2026/11/x.jpg",
	}
	if !slices.Equal(calls[0], want) {
		t.Fatalf("purged %v, want %v", calls[0], want)
	}
}

func TestFlushBatchesAtCloudflareLimit(t *testing.T) {
	f := newFakeCloudflare(t)
	p := newTestPurger(t, f, nil)
	for i := range BatchSize + 5 {
		p.Enqueue(fmt.Sprintf("/article/a%02d", i))
	}
	p.flush(context.Background(), true)
	calls := f.snapshot()
	if len(calls) != 2 || len(calls[0]) != BatchSize || len(calls[1]) != 5 {
		t.Fatalf("batch sizes = %d calls %v", len(calls), calls)
	}
}

func TestFlushRetriesServerErrorOnce(t *testing.T) {
	f := newFakeCloudflare(t)
	f.status = []int{http.StatusBadGateway}
	p := newTestPurger(t, f, nil)
	p.Enqueue("/article/a")
	p.flush(context.Background(), true)
	if calls := f.snapshot(); len(calls) != 2 {
		t.Fatalf("calls = %d, want a retry after the 502", len(calls))
	}
	if n := p.pendingCount(); n != 0 {
		t.Fatalf("%d urls requeued after the retry succeeded", n)
	}
}

func TestFlushRequeuesWhenRetryAlsoFails(t *testing.T) {
	f := newFakeCloudflare(t)
	f.status = []int{http.StatusTooManyRequests, http.StatusTooManyRequests}
	p := newTestPurger(t, f, nil)
	p.Enqueue("/article/a")
	p.flush(context.Background(), true)
	if n := p.pendingCount(); n != 1 {
		t.Fatalf("pending = %d, want the rate-limited url requeued", n)
	}
}

func TestFlushDoesNotRequeueClientError(t *testing.T) {
	f := newFakeCloudflare(t)
	f.status = []int{http.StatusForbidden}
	p := newTestPurger(t, f, nil)
	p.Enqueue("/article/a")
	p.flush(context.Background(), true)
	if calls := f.snapshot(); len(calls) != 1 {
		t.Fatalf("calls = %d, want no retry of a 403", len(calls))
	}
	if n := p.pendingCount(); n != 0 {
		t.Fatalf("pending = %d, a 403 would only fail again", n)
	}
}

func TestSuccessFalseIsAFailure(t *testing.T) {
	f := newFakeCloudflare(t)
	f.body = `{"success":false,"errors":[{"code":1234}]}`
	p := newTestPurger(t, f, nil)
	if err := p.send(context.Background(), []string{"https://www.thetriangle.org/"}); err == nil {
		t.Fatal("200 with success:false was treated as purged")
	}
}

func TestRunDebouncesAndCallsBeforeFlush(t *testing.T) {
	f := newFakeCloudflare(t)
	var before atomic.Int32
	p := newTestPurger(t, f, func() { before.Add(1) })
	p.delay = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	p.Enqueue("/article/a")
	p.Enqueue("/article/b") // inside the window: same flush
	deadline := time.Now().Add(2 * time.Second)
	for len(f.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	calls := f.snapshot()
	if len(calls) != 1 || len(calls[0]) != 2 {
		t.Fatalf("calls = %v, want both urls in one call", calls)
	}
	if before.Load() != 1 {
		t.Fatalf("BeforeFlush ran %d times, want 1", before.Load())
	}
}

func TestRunFlushesQueueOnShutdown(t *testing.T) {
	f := newFakeCloudflare(t)
	p := newTestPurger(t, f, nil)
	p.delay = time.Hour // the window never closes on its own

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	p.Enqueue("/article/retraction")
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done

	if calls := f.snapshot(); len(calls) != 1 {
		t.Fatalf("calls = %v, want the queued url sent on shutdown", calls)
	}
}

func TestShutdownDrainDoesNotRequeue(t *testing.T) {
	f := newFakeCloudflare(t)
	f.status = []int{http.StatusBadGateway, http.StatusBadGateway}
	p := newTestPurger(t, f, nil)
	p.Enqueue("/article/a")
	p.drainOnShutdown()
	if n := p.pendingCount(); n != 0 {
		t.Fatalf("pending = %d after the final drain; nothing would ever send it", n)
	}
}

func TestEnqueueCapsQueue(t *testing.T) {
	f := newFakeCloudflare(t)
	p := newTestPurger(t, f, nil)
	for i := range MaxQueued + 10 {
		p.Enqueue(fmt.Sprintf("/article/%d", i))
	}
	if n := p.pendingCount(); n != MaxQueued {
		t.Fatalf("pending = %d, want capped at %d", n, MaxQueued)
	}
}

func (p *Purger) pendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}
