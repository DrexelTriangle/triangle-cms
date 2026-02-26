package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogging(t *testing.T) {
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)

	var logOutput bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test"))
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Chain(mux, Logging, Recovery).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	logLine := logOutput.String()
	if !strings.Contains(logLine, `msg="http request"`) {
		t.Fatalf("expected request log message, got %q", logLine)
	}
	if !strings.Contains(logLine, "method=GET") {
		t.Fatalf("expected method in log, got %q", logLine)
	}
	if !strings.Contains(logLine, "path=/test") {
		t.Fatalf("expected path in log, got %q", logLine)
	}
	if !strings.Contains(logLine, "status=200") {
		t.Fatalf("expected status in log, got %q", logLine)
	}
}

func TestLogging_DefaultStatusWhenHandlerDoesNotWrite(t *testing.T) {
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)

	var logOutput bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /noop", func(w http.ResponseWriter, r *http.Request) {
		// Intentionally no write and no explicit header.
	})

	req := httptest.NewRequest(http.MethodGet, "/noop", nil)
	rec := httptest.NewRecorder()

	Chain(mux, Logging).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	logLine := logOutput.String()
	if !strings.Contains(logLine, `msg="http request"`) {
		t.Fatalf("expected request log message, got %q", logLine)
	}
	if !strings.Contains(logLine, "method=GET") {
		t.Fatalf("expected method in log, got %q", logLine)
	}
	if !strings.Contains(logLine, "path=/noop") {
		t.Fatalf("expected path in log, got %q", logLine)
	}
	if !strings.Contains(logLine, "status=200") {
		t.Fatalf("expected default status in log, got %q", logLine)
	}
}

func TestRecovery(t *testing.T) {
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)

	var logOutput bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Chain(mux, Logging, Recovery).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	logLine := logOutput.String()
	if !strings.Contains(logLine, `msg="panic recovered"`) {
		t.Fatalf("expected panic recovery log, got %q", logLine)
	}
	if !strings.Contains(logLine, "panic=boom") {
		t.Fatalf("expected panic value in log, got %q", logLine)
	}
	if !strings.Contains(logLine, "stack_trace=") {
		t.Fatalf("expected stack trace in log, got %q", logLine)
	}
}
