package cdnpurge

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingTransport answers every warm request locally and records it, so no
// test ever reaches the real site.
type recordingTransport struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.reqs = append(rt.reqs, r)
	rt.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: r}, nil
}

func (rt *recordingTransport) urls() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var out []string
	for _, r := range rt.reqs {
		out = append(out, r.URL.String())
	}
	slices.Sort(out)
	return out
}

func TestWarmable(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://www.thetriangle.org/":                                 true,
		"https://www.thetriangle.org":                                  true,
		"https://www.thetriangle.org/article/fork":                     true,
		"https://www.thetriangle.org/news":                             true,
		"https://www.thetriangle.org/author/jane-doe":                  true,
		"https://www.thetriangle.org/article/":                         false, // every article: a prefix, not a page
		"https://www.thetriangle.org/wp-content/uploads/2026/11/a.jpg": false, // media fills on read
	} {
		if got := warmable(raw); got != want {
			t.Errorf("warmable(%q) = %v, want %v", raw, got, want)
		}
	}
}

// After an accepted purge the purged pages are re-requested through the edge,
// with the warmer's User-Agent; media and whole-section prefixes are not.
func TestFlushWarmsPurgedPages(t *testing.T) {
	f := newFakeCloudflare(t)
	rt := &recordingTransport{}
	p, err := New(Config{
		ZoneID: "zone-1", APIToken: "tok", SiteURL: "https://www.thetriangle.org",
		Endpoint: f.server.URL, WarmClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = time.Millisecond
	p.warmer.delay = 0

	p.Enqueue("/", "/article/fork", "/news", "/article/", "/wp-content/uploads/a.jpg")
	p.flush(context.Background(), true)

	want := []string{
		"https://www.thetriangle.org/",
		"https://www.thetriangle.org/article/fork",
		"https://www.thetriangle.org/news",
	}
	deadline := time.Now().Add(2 * time.Second)
	for !slices.Equal(rt.urls(), want) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := rt.urls(); !slices.Equal(got, want) {
		t.Fatalf("warmed %v, want %v", got, want)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, r := range rt.reqs {
		if r.Method != http.MethodGet || r.Header.Get("User-Agent") != warmUserAgent {
			t.Errorf("%s %s with UA %q", r.Method, r.URL, r.Header.Get("User-Agent"))
		}
	}
}

// A refused purge evicted nothing, so there is nothing to warm.
func TestFailedPurgeWarmsNothing(t *testing.T) {
	f := newFakeCloudflare(t)
	f.status = []int{http.StatusForbidden}
	rt := &recordingTransport{}
	p, err := New(Config{
		ZoneID: "zone-1", APIToken: "tok", SiteURL: "https://www.thetriangle.org",
		Endpoint: f.server.URL, WarmClient: &http.Client{Transport: rt},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.warmer.delay = 0

	p.Enqueue("/article/fork")
	p.flush(context.Background(), true)
	time.Sleep(100 * time.Millisecond)
	if got := rt.urls(); len(got) != 0 {
		t.Fatalf("warmed %v after a refused purge", got)
	}
}
