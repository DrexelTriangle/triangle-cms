package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var RequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests",
	},
	[]string{"method", "route", "status"},
)

// HomepageCacheRequests counts how each GET /v1/homepage was answered by the
// in-process response cache. The outcome label is a fixed set (hit, miss,
// shared, stale, bypass, error, canceled); nothing request-derived goes in a label.
var HomepageCacheRequests = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "homepage_cache_requests_total",
		Help: "GET /v1/homepage requests by response-cache outcome",
	},
	[]string{"outcome"},
)

// CDNPurgeURLs counts public-site URLs handed to Cloudflare's purge API by
// result: purged, failed (the call errored; retryable failures are requeued and
// counted again) or dropped (the queue was full).
var CDNPurgeURLs = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "cdn_purge_urls_total",
		Help: "Public-site URLs sent to the Cloudflare purge API, by result",
	},
	[]string{"result"},
)

// CDNWarmRequests counts the cache warmer's re-requests of purged pages by
// result: warmed (2xx through Cloudflare) or failed.
var CDNWarmRequests = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "cdn_warm_requests_total",
		Help: "Purged public pages re-requested through Cloudflare to refill its cache, by result",
	},
	[]string{"result"},
)

var RequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "Duration of HTTP requests in seconds",
	},
	[]string{"method", "route", "status"},
)
