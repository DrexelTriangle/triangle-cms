package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pinRateLimitClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	now := start
	prev := rateLimitNow
	rateLimitNow = func() time.Time { return now }
	t.Cleanup(func() { rateLimitNow = prev })
	return &now
}

func TestRateLimitGlobal_CapsAcrossIPs(t *testing.T) {
	pinRateLimitClock(t, time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	calls := 0
	handler := RateLimitGlobal(3, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	}))

	for i, ip := range []string{"203.0.113.1:1", "203.0.113.2:1", "203.0.113.3:1"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/newsletter/subscribe", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d from %s = %d, want 202", i+1, ip, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/newsletter/subscribe", nil)
	req.RemoteAddr = "198.51.100.77:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request from a fresh IP = %d, want 429: the cap is global", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"rate limit exceeded"}` {
		t.Errorf("body = %s", body)
	}
	if calls != 3 {
		t.Errorf("wrapped handler ran %d times, want 3", calls)
	}
}

func TestRateLimitGlobal_ResetsAfterWindow(t *testing.T) {
	now := pinRateLimitClock(t, time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	handler := RateLimitGlobal(1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	do := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		return rec.Code
	}
	if do() != http.StatusOK || do() != http.StatusTooManyRequests {
		t.Fatal("expected 200 then 429 inside one window")
	}
	*now = now.Add(59 * time.Second)
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("1s before the window ends = %d, want 429", code)
	}
	*now = now.Add(time.Second)
	if code := do(); code != http.StatusOK {
		t.Fatalf("after the window = %d, want 200", code)
	}
}

func TestRateLimitByIP_UsesPinnedClock(t *testing.T) {
	now := pinRateLimitClock(t, time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	handler := RateLimitByIP(1, time.Hour)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "203.0.113.50:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if do() != http.StatusOK || do() != http.StatusTooManyRequests {
		t.Fatal("expected 200 then 429")
	}
	*now = now.Add(time.Hour)
	if code := do(); code != http.StatusOK {
		t.Fatalf("per-IP window did not reset on the pinned clock: %d", code)
	}
}
