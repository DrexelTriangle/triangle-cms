// Package cdnpurge evicts public-site pages from Cloudflare's cache when the
// content behind them changes.
//
// Scalene's pages tell Cloudflare it may keep them: an article for 30 days
// (src/layouts/ArticleLayout.astro), the homepage for a day (src/pages/
// index.astro), a section or author page for 5 minutes. Those edge lifetimes
// are only safe because this package purges on every write; without it a
// correction, a retraction or an unpublish would stay public that long.
//
// The contract with Scalene, which nothing in this repo can check: the
// homepage response carries "Cache-Tag: homepage" (HomepageCacheTag), because
// on this zone only prefix, tag and host purges evict HTML; a URL purge returns
// success and evicts nothing. If the homepage lost that header, its purges
// would silently do nothing and it would be up to a day stale. Verify with a
// cf-cache-status HIT, a purge, then a MISS -- never from success:true alone.
//
// Purges are queued, not sent inline: an editor's save must never wait on, or
// fail because of, Cloudflare. The queue collects URLs for a short window and
// then sends them in batches, which also folds a burst of autosaves into one
// call. The window doubles as a guard against replica lag: Scalene re-renders
// a purged page by reading through MaxScale, and a read that lands on a replica
// before the write has replicated would put the old page straight back in the
// cache for another 30 minutes.
//
// A nil *Purger is valid and does nothing, so callers never check whether
// purging is configured.
package cdnpurge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"server/internal/metrics"
)

const (
	// DefaultEndpoint is Cloudflare's API root.
	DefaultEndpoint = "https://api.cloudflare.com/client/v4"

	// DefaultDelay is how long the queue collects URLs before a flush. Long
	// enough to cover replica lag and fold an autosave burst; short enough that
	// an editor checking the site sees the change before they think to ask.
	DefaultDelay = 5 * time.Second

	// BatchSize is the most URLs one purge-by-URL call may carry on the Free,
	// Pro and Business plans.
	BatchSize = 30

	// MaxQueued bounds the pending set. A flush that keeps failing must not
	// grow memory without limit; past this, new URLs are dropped (and counted)
	// and expire from Cloudflare on their own TTL as they did before purging.
	MaxQueued = 2000

	requestTimeout = 15 * time.Second
	retryBackoff   = 2 * time.Second
)

// Config configures a Purger. ZoneID and APIToken are required; SiteURL is the
// public origin that relative paths are resolved against, and the only host
// whose URLs are sent.
type Config struct {
	ZoneID   string
	APIToken string
	SiteURL  string

	// BeforeFlush runs just before each flush. The CMS uses it to drop its own
	// homepage cache: a reload made right after the write may have read a
	// lagging replica, and the purge must not let Scalene re-cache that.
	BeforeFlush func()

	Delay      time.Duration // DefaultDelay when zero
	Endpoint   string        // DefaultEndpoint when empty
	HTTPClient *http.Client  // a client with requestTimeout when nil
}

// Purger queues and sends Cloudflare purge-by-URL requests.
type Purger struct {
	zoneID   string
	token    string
	site     *url.URL
	delay    time.Duration
	endpoint string
	client   *http.Client
	before   func()
	backoff  time.Duration // before the one in-flush retry

	mu      sync.Mutex
	pending map[string]struct{}
	wake    chan struct{} // buffered 1: "the queue went from empty to non-empty"
}

// New validates cfg and returns a Purger. Run must be started for anything to
// be sent.
func New(cfg Config) (*Purger, error) {
	zone := strings.TrimSpace(cfg.ZoneID)
	token := strings.TrimSpace(cfg.APIToken)
	if zone == "" || token == "" {
		return nil, errors.New("cdnpurge: zone id and api token are required")
	}
	site, err := url.Parse(strings.TrimSpace(cfg.SiteURL))
	if err != nil || (site.Scheme != "https" && site.Scheme != "http") || site.Host == "" {
		return nil, fmt.Errorf("cdnpurge: site url must be an absolute http(s) URL, got %q", cfg.SiteURL)
	}
	site.Path, site.RawQuery, site.Fragment = "", "", ""
	delay := cfg.Delay
	if delay <= 0 {
		delay = DefaultDelay
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return &Purger{
		zoneID:   zone,
		token:    token,
		site:     site,
		delay:    delay,
		endpoint: endpoint,
		client:   client,
		before:   cfg.BeforeFlush,
		backoff:  retryBackoff,
		pending:  make(map[string]struct{}),
		wake:     make(chan struct{}, 1),
	}, nil
}

// Enabled reports whether p will send anything.
func (p *Purger) Enabled() bool { return p != nil }

// Enqueue adds paths or URLs to the next flush. A path ("/article/x") is
// resolved against the site URL; an absolute URL is kept only if it is on the
// site's host, because Cloudflare rejects a whole batch that names a URL
// outside the zone. Anything else is ignored.
func (p *Purger) Enqueue(paths ...string) {
	if p == nil || len(paths) == 0 {
		return
	}
	p.mu.Lock()
	wasEmpty := len(p.pending) == 0
	dropped := 0
	for _, raw := range paths {
		u, ok := p.resolve(raw)
		if !ok {
			continue
		}
		if _, dup := p.pending[u]; dup {
			continue
		}
		if len(p.pending) >= MaxQueued {
			dropped++
			continue
		}
		p.pending[u] = struct{}{}
	}
	nonEmpty := len(p.pending) > 0
	p.mu.Unlock()

	if dropped > 0 {
		metrics.CDNPurgeURLs.WithLabelValues("dropped").Add(float64(dropped))
		slog.Warn("cdn purge queue full; urls dropped", "dropped", dropped, "max", MaxQueued)
	}
	if wasEmpty && nonEmpty {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

func (p *Purger) resolve(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	u := p.site.ResolveReference(ref)
	if !strings.EqualFold(u.Host, p.site.Host) {
		return "", false
	}
	// Cloudflare keys its cache on the lowercased host; purging a URL spelled
	// any other way evicts nothing.
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String(), true
}

// PurgeSite evicts everything Cloudflare holds for the site's host -- every
// page, image and asset on www -- right now, bypassing the queue. It is the
// emergency lever for when something stale or wrong is public and a targeted
// purge isn't enough; the origin then serves every first request again until
// the cache refills, which is why it is an admin action and not automatic.
//
// Other hosts in the zone (delta, the legacy subdomains) are left alone. The
// queued URLs are not dropped: a whole-host purge covers them, and sending
// them later as well is harmless.
func (p *Purger) PurgeSite(ctx context.Context) error {
	if p == nil {
		return errors.New("cdnpurge: purging is not configured")
	}
	if p.before != nil {
		p.before()
	}
	if err := p.post(ctx, map[string][]string{"hosts": {p.site.Host}}); err != nil {
		metrics.CDNPurgeURLs.WithLabelValues("failed").Inc()
		return err
	}
	metrics.CDNPurgeURLs.WithLabelValues("purged").Inc()
	slog.Warn("cdn purge: whole site purged", "host", p.site.Host)
	return nil
}

// SiteHost is the host PurgeSite evicts.
func (p *Purger) SiteHost() string {
	if p == nil {
		return ""
	}
	return p.site.Host
}

// Run sends queued purges until ctx ends. Whatever is still queued then is
// flushed once more on a short deadline, so a deploy's shutdown does not
// swallow the last save.
func (p *Purger) Run(ctx context.Context) {
	if p == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			p.drainOnShutdown()
			return
		case <-p.wake:
		}
		timer := time.NewTimer(p.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			p.drainOnShutdown()
			return
		case <-timer.C:
		}
		p.flush(ctx, true)
	}
}

// drainOnShutdown is the last flush. It does not requeue failures: Run is
// returning, so nothing would ever send them.
func (p *Purger) drainOnShutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	p.flush(ctx, false)
}

// flush sends everything queued. A batch that fails twice is put back for the
// next flush (when requeue is set) rather than retried in a loop here, so a
// Cloudflare outage costs one failed call per flush, not a hot loop.
func (p *Purger) flush(ctx context.Context, requeue bool) {
	p.mu.Lock()
	urls := make([]string, 0, len(p.pending))
	for u := range p.pending {
		urls = append(urls, u)
	}
	p.pending = make(map[string]struct{})
	p.mu.Unlock()
	if len(urls) == 0 {
		return
	}
	if p.before != nil {
		p.before()
	}
	sort.Strings(urls) // deterministic batches, for logs and tests

	var failed []string
	for start := 0; start < len(urls); start += BatchSize {
		batch := urls[start:min(start+BatchSize, len(urls))]
		err := p.send(ctx, batch)
		if err != nil && retryable(err) && ctx.Err() == nil {
			select {
			case <-time.After(p.backoff):
				err = p.send(ctx, batch)
			case <-ctx.Done():
			}
		}
		if err != nil {
			metrics.CDNPurgeURLs.WithLabelValues("failed").Add(float64(len(batch)))
			slog.Warn("cdn purge failed", "urls", len(batch), "error", err)
			if retryable(err) {
				failed = append(failed, batch...)
			}
			continue
		}
		metrics.CDNPurgeURLs.WithLabelValues("purged").Add(float64(len(batch)))
		slog.Info("cdn purge sent", "urls", len(batch))
	}
	if requeue && len(failed) > 0 && ctx.Err() == nil {
		p.Enqueue(failed...)
	}
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("cloudflare purge: HTTP %d: %s", e.status, e.body)
}

// retryable is true for errors a later attempt can fix: transport failures,
// rate limiting and server errors. A 4xx such as a bad token or a URL outside
// the zone would fail the same way again.
func retryable(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.status == http.StatusTooManyRequests || apiErr.status >= 500
	}
	return true
}

// send purges one batch. Each URL except the site root goes as a prefix
// ("www.thetriangle.org/article/x"), not as a file URL: on this zone a
// purge-by-URL reports success for an HTML page and evicts nothing, while a
// prefix purge evicts it (verified in production 2026-11-10 against cached
// articles, section pages and /about; robots.txt and images purge either way).
// The root cannot go as a prefix, which would be the whole site, and a URL
// purge evicts nothing for the reason above. So it goes as the cache tag the
// homepage response carries (Scalene index.astro sends "Cache-Tag: homepage"),
// which evicts that page alone. Scalene caches the homepage for a day on the
// strength of this purge.
//
// Cloudflare takes one purge type per request, so a batch holding the root is
// two calls.
func (p *Purger) send(ctx context.Context, batch []string) error {
	var prefixes []string
	root := false
	for _, raw := range batch {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if u.Path == "" || u.Path == "/" {
			root = true
			continue
		}
		prefixes = append(prefixes, u.Host+u.EscapedPath())
	}
	if len(prefixes) > 0 {
		if err := p.post(ctx, map[string][]string{"prefixes": prefixes}); err != nil {
			return err
		}
	}
	if root {
		return p.post(ctx, map[string][]string{"tags": {HomepageCacheTag}})
	}
	return nil
}

// HomepageCacheTag is the Cache-Tag Scalene's homepage response carries. Keep
// it in step with src/pages/index.astro there.
const HomepageCacheTag = "homepage"

func (p *Purger) post(ctx context.Context, purge map[string][]string) error {
	payload, err := json.Marshal(purge)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint+"/zones/"+url.PathEscape(p.zoneID)+"/purge_cache", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode/100 != 2 {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}
	// Cloudflare can answer 200 with success:false.
	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !result.Success {
		return &apiError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}
	return nil
}
