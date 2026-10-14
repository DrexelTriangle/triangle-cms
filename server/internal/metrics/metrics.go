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

var RequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "Duration of HTTP requests in seconds",
	},
	[]string{"method", "route", "status"},
)
