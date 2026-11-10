package cdnpurge

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"server/internal/metrics"
)

// After a purge the next reader of each page pays for a fresh render, and for
// the homepage that reader is very likely to arrive within seconds. The warmer
// is that first reader instead: once a purge is accepted it requests each
// purged page through Cloudflare, so the copy is back in the cache before a
// person asks for it.
//
// It warms only the Cloudflare location its own requests reach (Delta's), which
// for a Philadelphia newsroom is the one most readers hit; others fill on their
// first visit, as before. It never blocks or fails a purge, and stays small:
// a few requests at a time, a short delay, a per-request timeout.

const (
	// warmDelay lets the purge settle across the edge before the re-request.
	warmDelay = 2 * time.Second
	// warmConcurrency bounds requests in flight at once; a footer edit can
	// purge many pages, and the warmer must never become a load spike itself.
	warmConcurrency = 4
	warmTimeout     = 15 * time.Second
	// warmUserAgent identifies the warmer in the origin's and Cloudflare's logs.
	// Verified not to be challenged by the zone's browser check.
	warmUserAgent = "TriangleCMS-CacheWarmer/1 (+https://www.thetriangle.org)"
	// maxWarmPerFlush caps one flush's warming; past it, the rest fill on read.
	maxWarmPerFlush = 100
)

type warmer struct {
	client *http.Client
	delay  time.Duration
	sem    chan struct{}
}

func newWarmer(client *http.Client) *warmer {
	if client == nil {
		client = &http.Client{Timeout: warmTimeout}
	}
	return &warmer{client: client, delay: warmDelay, sem: make(chan struct{}, warmConcurrency)}
}

// warmable reports whether a purged URL is a single page worth re-requesting:
// the homepage, or a path that is neither a file (media, which is cheap to
// fill and often large) nor a prefix of many pages ("/article/").
func warmable(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := u.EscapedPath()
	switch {
	case p == "" || p == "/":
		return true
	case strings.HasSuffix(p, "/"):
		return false
	case path.Ext(p) != "":
		return false
	}
	return true
}

// warm re-requests the warmable URLs in the background.
func (w *warmer) warm(urls []string) {
	var pages []string
	seen := map[string]bool{}
	for _, u := range urls {
		if warmable(u) && !seen[u] {
			seen[u] = true
			pages = append(pages, u)
		}
	}
	if len(pages) > maxWarmPerFlush {
		pages = pages[:maxWarmPerFlush]
	}
	if len(pages) == 0 {
		return
	}
	go func() {
		time.Sleep(w.delay)
		for _, u := range pages {
			w.sem <- struct{}{}
			go func(u string) {
				defer func() { <-w.sem }()
				w.fetch(u)
			}(u)
		}
	}()
}

func (w *warmer) fetch(u string) {
	ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", warmUserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		metrics.CDNWarmRequests.WithLabelValues("failed").Inc()
		slog.Debug("cache warm failed", "url", u, "error", err)
		return
	}
	// Read it through: the edge stores the response as it streams to us.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	result := "warmed"
	if resp.StatusCode/100 != 2 {
		result = "failed"
	}
	metrics.CDNWarmRequests.WithLabelValues(result).Inc()
}
