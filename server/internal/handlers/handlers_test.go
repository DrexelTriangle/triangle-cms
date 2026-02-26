package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsersHandler(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		path            string
		wantStatus      int
		wantContentType string
		wantBody        string
	}{
		{
			name:            "get users",
			method:          http.MethodGet,
			path:            "/users",
			wantStatus:      http.StatusOK,
			wantContentType: "application/json",
			wantBody:        "{\"status\":\"OK\",\"message\":\"Users endpoint hit\",\"code\":200}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()

			handler := http.HandlerFunc(Users)
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("handler returned wrong status code: got %v want %v", rr.Code, tt.wantStatus)
			}

			contentType := rr.Header().Get("Content-Type")
			if contentType != tt.wantContentType {
				t.Fatalf("expected Content-Type: %s, got: %q", tt.wantContentType, contentType)
			}

			body, _ := io.ReadAll(rr.Body)
			if strings.TrimSpace(string(body)) != tt.wantBody {
				t.Fatalf("handler returned unexpected body: got %v want %v", string(body), tt.wantBody)
			}
		})
	}
}

type failingResponseWriter struct {
	header http.Header
	status int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("forced write error")
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func TestUsersHandler_EncodeFailure(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	w := &failingResponseWriter{}

	Users(w, req)

	if w.status != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, w.status)
	}
}
