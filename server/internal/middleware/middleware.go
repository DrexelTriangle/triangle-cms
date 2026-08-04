package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"server/internal/metrics"
	"strconv"
	"time"
)

type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

type responseRecord struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecord) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecord) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecord{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if user, ok := UserFromContext(r.Context()); ok && user != nil {
			args = append(args,
				"actor_id", user.ID,
				"actor_name", user.Name,
				"actor_role", string(user.Role),
			)
		}

		slog.Info("http request", args...)
	})
}

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered",
					"panic", err,
					"method", r.Method,
					"path", r.URL.Path,
					"stack_trace", string(debug.Stack()),
				)
				http.Error(w, "500 - Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecord{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(rec.status)
		metrics.RequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		metrics.RequestDuration.WithLabelValues(r.Method, route, status).Observe(time.Since(start).Seconds())
	})
}
