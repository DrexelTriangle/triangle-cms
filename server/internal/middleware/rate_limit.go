package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ipWindowCounter struct {
	windowStart time.Time
	count       int
}

// rateLimitNow is the limiters' clock; tests pin it.
var rateLimitNow = time.Now

func writeRateLimited(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "rate limit exceeded",
	})
}

// RateLimitGlobal caps requests to a route across all callers in a fixed
// window. It backs up the per-IP limits on public write endpoints: an attacker
// rotating source addresses gets past RateLimitByIP, not past this.
func RateLimitGlobal(limit int, window time.Duration) Middleware {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	var (
		mu          sync.Mutex
		windowStart time.Time
		count       int
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := rateLimitNow()
			mu.Lock()
			if windowStart.IsZero() || now.Sub(windowStart) >= window {
				windowStart, count = now, 0
			}
			count++
			allowed := count <= limit
			mu.Unlock()
			if !allowed {
				writeRateLimited(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByIP limits requests by client IP within a fixed time window.
func RateLimitByIP(limit int, window time.Duration) Middleware {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}

	var mu sync.Mutex
	counters := map[string]ipWindowCounter{}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			now := rateLimitNow()

			mu.Lock()
			entry := counters[ip]
			if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= window {
				entry = ipWindowCounter{
					windowStart: now,
					count:       0,
				}
			}
			entry.count++
			counters[ip] = entry
			allowed := entry.count <= limit

			// Opportunistic bounded cleanup.
			if len(counters) > 5000 {
				cutoff := now.Add(-window * 2)
				for k, v := range counters {
					if v.windowStart.Before(cutoff) {
						delete(counters, k)
					}
				}
			}
			mu.Unlock()

			if !allowed {
				writeRateLimited(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				return candidate
			}
		}
	}

	ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && ip != "" {
		return ip
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
