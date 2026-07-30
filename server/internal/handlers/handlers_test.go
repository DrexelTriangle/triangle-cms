package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"server/internal/middleware"
	"server/internal/models"
)

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

func TestGetComments_InvalidStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/comments?status=deleted", nil)
	rec := httptest.NewRecorder()

	GetComments(nil)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestPatchComment_InvalidID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/v1/comments/not-a-number", strings.NewReader(`{"status":"approved"}`))
	req.SetPathValue("id", "not-a-number")
	rec := httptest.NewRecorder()

	PatchComment(nil)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestPatchComment_InvalidStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/v1/comments/12", strings.NewReader(`{"status":"deleted"}`))
	req.SetPathValue("id", "12")
	rec := httptest.NewRecorder()

	PatchComment(nil)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
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

func TestTaxonomyCountSlugs_CanonicalizesAndDedupes(t *testing.T) {
	got := taxonomyCountSlugs([]string{
		"News",
		" news ",
		"Academic Transformation",
		"academic-transformation",
		"",
		"   ",
		"Special Editions",
	})

	want := []string{"news", "academic-transformation", "special-editions"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index, slug := range want {
		if got[index] != slug {
			t.Fatalf("got %v, want %v", got, want)
		}
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

func TestAuthorArchiveCondition_DefaultsToActiveAuthors(t *testing.T) {
	got := authorArchiveCondition(url.Values{}, true)

	if got != "a.`archived_at` IS NULL" {
		t.Fatalf("got %q, want active author condition", got)
	}
}

func TestAuthorArchiveCondition_ArchivedTrueSelectsTrash(t *testing.T) {
	values := url.Values{}
	values.Set("archived", "1")

	got := authorArchiveCondition(values, true)

	if got != "a.`archived_at` IS NOT NULL" {
		t.Fatalf("got %q, want archived author condition", got)
	}
}

func TestAuthorArchiveCondition_ArchivedFalseSelectsActive(t *testing.T) {
	values := url.Values{}
	values.Set("archived", "false")

	got := authorArchiveCondition(values, true)

	if got != "a.`archived_at` IS NULL" {
		t.Fatalf("got %q, want active author condition", got)
	}
}

func TestAuthorArchiveCondition_AnonymousIgnoresArchivedParam(t *testing.T) {
	values := url.Values{}
	values.Set("archived", "true")

	got := authorArchiveCondition(values, false)

	if got != "a.`archived_at` IS NULL" {
		t.Fatalf("got %q, want anonymous callers pinned to active authors", got)
	}
}

// The article listing is public, so an anonymous caller must never be able to
// widen it to drafts or soft-deleted rows via query params.
func TestArticleQueryFilters_AnonymousIsPinnedToPublished(t *testing.T) {
	for _, query := range []string{"", "?status=draft", "?archived=true", "?status=draft&archived=true"} {
		t.Run("anonymous "+query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/articles"+query, nil)
			conditions, _ := articleQueryFilters(req, ArticleParams{})
			joined := strings.Join(conditions, " AND ")

			if !strings.Contains(joined, "`pub_date` IS NOT NULL") {
				t.Fatalf("anonymous listing must be published-only, got %q", joined)
			}
			if !strings.Contains(joined, "`archived_at` IS NULL") {
				t.Fatalf("anonymous listing must exclude archived, got %q", joined)
			}
			if strings.Contains(joined, "`pub_date` IS NULL") {
				t.Fatalf("anonymous listing must not select drafts, got %q", joined)
			}
			if strings.Contains(joined, "`archived_at` IS NOT NULL") {
				t.Fatalf("anonymous listing must not select archived, got %q", joined)
			}
		})
	}
}

func TestArticleQueryFilters_EditorKeepsDraftAndArchivedFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles?status=draft&archived=true", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if !strings.Contains(joined, "`pub_date` IS NULL") {
		t.Fatalf("editor must keep the draft filter, got %q", joined)
	}
	if !strings.Contains(joined, "`archived_at` IS NOT NULL") {
		t.Fatalf("editor must keep the archived filter, got %q", joined)
	}
}
