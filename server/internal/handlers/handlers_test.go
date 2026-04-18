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

func TestAppendCategorySlugCondition(t *testing.T) {
	var conditions []string
	var args []any

	appendCategorySlugCondition(&conditions, &args, "comics-puzzles")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}

	wantArgs := []string{
		"%comics-puzzles%",
		"%comics puzzles%",
		"%comics & puzzles%",
	}
	for i, want := range wantArgs {
		got, ok := args[i].(string)
		if !ok {
			t.Fatalf("arg %d has non-string type %T", i, args[i])
		}
		if got != want {
			t.Fatalf("arg %d = %q, want %q", i, got, want)
		}
	}
}

func TestAppendArticleTypeCondition(t *testing.T) {
	var conditions []string
	var args []any

	appendArticleTypeCondition(&conditions, &args, "developing-stories")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if len(args) != 6 {
		t.Fatalf("expected 6 args, got %d", len(args))
	}

	wantArgs := []string{
		"%developing-stories%",
		"%developing-stories%",
		"%developing-stories%",
		"%developing stories%",
		"%developing stories%",
		"%developing stories%",
	}
	for i, want := range wantArgs {
		got, ok := args[i].(string)
		if !ok {
			t.Fatalf("arg %d has non-string type %T", i, args[i])
		}
		if got != want {
			t.Fatalf("arg %d = %q, want %q", i, got, want)
		}
	}
}

func TestAppendArticleTypeCondition_Negated(t *testing.T) {
	var conditions []string
	var args []any

	appendArticleTypeCondition(&conditions, &args, "developing-stories", true)

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if !strings.HasPrefix(conditions[0], "NOT (") {
		t.Fatalf("expected negated clause, got %q", conditions[0])
	}
	if len(args) != 6 {
		t.Fatalf("expected 6 args, got %d", len(args))
	}
}
