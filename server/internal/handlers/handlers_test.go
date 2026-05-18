package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestUsersHandler_NotImplemented(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/media", nil)
	rec := httptest.NewRecorder()

	Users(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected %d, got %d", http.StatusNotImplemented, rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body["error"] != "not implemented" {
		t.Fatalf("expected error %q, got %q", "not implemented", body["error"])
	}
}

func TestGetMe_UnauthorizedWithoutUserInContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	rec := httptest.NewRecorder()

	GetMe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Fatalf("expected error %q, got %q", "unauthorized", body["error"])
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

func TestArticleQueryFilters_FormatsDateFiltersWithGoReferenceLayout(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles", nil)
	q := url.Values{}
	q.Set("published_date", "2026-05-18T14:30:45Z")
	q.Set("creation_date", "2026-05-17")
	req.URL.RawQuery = q.Encode()

	_, args := articleQueryFilters(req, ArticleParams{})

	var stringArgs []string
	for _, arg := range args {
		s, ok := arg.(string)
		if ok {
			stringArgs = append(stringArgs, s)
		}
	}

	contains := func(target string) bool {
		for _, candidate := range stringArgs {
			if candidate == target {
				return true
			}
		}
		return false
	}

	if !contains("2026-05-18 14:30:45") {
		t.Fatalf("expected formatted published_date argument %q in args %v", "2026-05-18 14:30:45", stringArgs)
	}
	if !contains("2026-05-17 00:00:00") {
		t.Fatalf("expected formatted creation_date argument %q in args %v", "2026-05-17 00:00:00", stringArgs)
	}
}
