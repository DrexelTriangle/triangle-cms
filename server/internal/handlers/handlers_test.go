package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestArticleCommentsClosed(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "closed", status: "closed", want: true},
		{name: "case and space tolerant", status: " Closed ", want: true},
		{name: "open", status: "open", want: false},
		{name: "empty means open", status: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := articleCommentsClosed(tt.status); got != tt.want {
				t.Fatalf("articleCommentsClosed(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestArticleListItems_IncludesScaleneBreakingNewsFlag(t *testing.T) {
	items := articleListItems([]models.Article{{
		ID:           12,
		Title:        "Campus alert",
		Slug:         "campus-alert",
		BreakingNews: true,
	}}, 50)

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !items[0].BreakingNews {
		t.Fatal("breaking_news flag was not copied to article list item")
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

	// Patterns are anchored on the JSON quotes so a slug matches a whole
	// category, never a fragment of a longer one -- see
	// db.CategoryMatchPatterns.
	wantArgs := []string{
		`%"comics-puzzles"%`,
		`%"comics puzzles"%`,
		`%"comics & puzzles"%`,
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

func TestArticlePatchDateColumns_PublishedAutosavePreservesExistingPublishDate(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	currentPublishedAt := sql.NullTime{Time: time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC), Valid: true}

	cols, args := articlePatchDateColumns(true, models.ArticleStatusPublished, false, nil, nil, currentPublishedAt, sql.NullTime{}, now)

	if strings.Join(cols, ",") != "scheduled_pub_date" {
		t.Fatalf("cols = %v, want only scheduled_pub_date", cols)
	}
	if len(args) != 1 || args[0] != nil {
		t.Fatalf("args = %v, want scheduled_pub_date cleared without pub_date arg", args)
	}
}

func TestArticlePatchDateColumns_PublishFromUnpublishedStampsPublishDate(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	cols, args := articlePatchDateColumns(true, models.ArticleStatusPublished, false, nil, nil, sql.NullTime{}, sql.NullTime{}, now)

	if strings.Join(cols, ",") != "pub_date,scheduled_pub_date" {
		t.Fatalf("cols = %v, want pub_date and scheduled_pub_date", cols)
	}
	if len(args) != 2 || args[0] != "2026-08-02 12:00:00" || args[1] != nil {
		t.Fatalf("args = %v, want pub_date stamped and scheduled_pub_date cleared", args)
	}
}

func TestArticlePatchDateColumns_DraftParksExistingPublishDate(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	currentPublishedAt := sql.NullTime{Time: time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC), Valid: true}

	cols, args := articlePatchDateColumns(true, models.ArticleStatusDraft, false, nil, nil, currentPublishedAt, sql.NullTime{}, now)

	if strings.Join(cols, ",") != "pub_date,scheduled_pub_date,last_pub_date" {
		t.Fatalf("cols = %v, want the live dates cleared and the old one parked", cols)
	}
	if len(args) != 3 || args[0] != nil || args[1] != nil || args[2] != "2026-07-01 09:30:00" {
		t.Fatalf("args = %v, want last_pub_date set to the old publish date", args)
	}
}

func TestArticlePatchDateColumns_RepublishRestoresParkedPublishDate(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	lastPublishedAt := sql.NullTime{Time: time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC), Valid: true}

	cols, args := articlePatchDateColumns(true, models.ArticleStatusPublished, false, nil, nil, sql.NullTime{}, lastPublishedAt, now)

	if strings.Join(cols, ",") != "pub_date,scheduled_pub_date" {
		t.Fatalf("cols = %v, want pub_date and scheduled_pub_date", cols)
	}
	if len(args) != 2 || args[0] != "2026-07-01 09:30:00" || args[1] != nil {
		t.Fatalf("args = %v, want the original publish date restored", args)
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
			if !strings.Contains(joined, "`pub_date` <= UTC_TIMESTAMP()") {
				t.Fatalf("anonymous listing must exclude scheduled articles, got %q", joined)
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
	if !strings.Contains(joined, "`scheduled_pub_date` IS NULL") {
		t.Fatalf("editor draft filter must exclude scheduled articles, got %q", joined)
	}
	if !strings.Contains(joined, "`archived_at` IS NOT NULL") {
		t.Fatalf("editor must keep the archived filter, got %q", joined)
	}
}

func TestArticleQueryFilters_EditorCanFilterScheduled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles?status=scheduled", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if !strings.Contains(joined, "`pub_date` IS NULL") {
		t.Fatalf("scheduled filter must exclude already-published articles, got %q", joined)
	}
	if !strings.Contains(joined, "`scheduled_pub_date` IS NOT NULL") {
		t.Fatalf("scheduled filter must require a schedule date, got %q", joined)
	}
}

func TestArticleQueryFilters_AuthorSearchMatchesNameOrLogin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles?author=Jane", nil)

	conditions, args := articleQueryFilters(req, ArticleParams{AuthorSearch: "Jane"})
	joined := strings.Join(conditions, " AND ")

	if !strings.Contains(joined, "LOWER(au.`display_name`) LIKE ?") {
		t.Fatalf("author search must match display_name, got %q", joined)
	}
	if !strings.Contains(joined, "LOWER(au.`login`) LIKE ?") {
		t.Fatalf("author search must match login, got %q", joined)
	}
	if len(args) < 2 || args[len(args)-2] != "%jane%" || args[len(args)-1] != "%jane%" {
		t.Fatalf("author search args = %v, want trailing %%jane%% patterns", args)
	}
}

// The single-article endpoint is public AND returns the full article body, so
// the anonymous restriction matters more here than on the listing.
func TestArticleDetailCondition_AnonymousSeesOnlyLiveArticles(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles/some-slug", nil)

	got := articleDetailCondition(req)

	if !strings.Contains(got, "`pub_date` IS NOT NULL") {
		t.Fatalf("anonymous lookup must exclude drafts, got %q", got)
	}
	if !strings.Contains(got, "`pub_date` <= UTC_TIMESTAMP()") {
		t.Fatalf("anonymous lookup must exclude scheduled articles, got %q", got)
	}
	if !strings.Contains(got, "`archived_at` IS NULL") {
		t.Fatalf("anonymous lookup must exclude archived articles, got %q", got)
	}
}

func TestArticleDetailCondition_EditorSeesEverything(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles/some-slug", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))

	got := articleDetailCondition(req)

	if got != "`slug` = ?" {
		t.Fatalf("editor lookup must not be narrowed, got %q", got)
	}
}

// A draft created in the CMS has neither authors nor categories until an editor
// fills them in, which is exactly the shape of the import-artifact filter. The
// editor listing must not drop it.
func TestArticleQueryFilters_EditorSeesUnfiledDrafts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if !strings.Contains(joined, "OR `pub_date` IS NULL)") {
		t.Fatalf("editor listing must exempt drafts from the artifact filter, got %q", joined)
	}
}

func TestArticleQueryFilters_AnonymousKeepsArtifactFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles", nil)

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if strings.Contains(joined, "OR `pub_date` IS NULL") {
		t.Fatalf("anonymous listing must not widen the artifact filter, got %q", joined)
	}
	if !strings.Contains(joined, "TRIM(COALESCE(`authors`, ''))") {
		t.Fatalf("anonymous listing must keep the artifact filter, got %q", joined)
	}
}

// Oldest-first used to append `pub_date` IS NOT NULL for everyone, which hid
// every draft — and made the Draft status filter return nothing at all.
func TestArticleQueryFilters_EditorAscendingDateSortKeepsDrafts(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles?sort_by=published_date&sort_direction=asc&status=draft", nil)
	req = req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if strings.Contains(joined, "`pub_date` IS NOT NULL") {
		t.Fatalf("editor oldest-first sort must not exclude drafts, got %q", joined)
	}
}

func TestArticleQueryFilters_AnonymousAscendingDateSortSkipsNullPubDates(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/articles?sort_by=published_date&sort_direction=asc", nil)

	conditions, _ := articleQueryFilters(req, ArticleParams{})
	joined := strings.Join(conditions, " AND ")

	if !strings.Contains(joined, "`pub_date` IS NOT NULL") {
		t.Fatalf("anonymous oldest-first sort must stay published-only, got %q", joined)
	}
}

// Sorting straight on `pub_date` parks every draft after the last published
// article, so a new draft lands on the final page of the listing.
func TestArticleOrderByClause(t *testing.T) {
	editorReq := func(query string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/v1/articles"+query, nil)
		return req.WithContext(middleware.ContextWithUser(req.Context(), &models.User{ID: 1, Role: models.RoleAdmin}))
	}

	got := articleOrderByClause(editorReq("?sort_by=published_date&sort_direction=desc"), "published_date", "desc")
	if got != " ORDER BY COALESCE(`pub_date`, `creation_date`) DESC, `id` DESC" {
		t.Fatalf("editor newest-first clause = %q", got)
	}

	if got := articleOrderByClause(editorReq("?sort_by=title&sort_direction=asc"), "title", "asc"); got != "" {
		t.Fatalf("non-date sort must fall through to BuildOrderLimit, got %q", got)
	}

	anonReq := httptest.NewRequest(http.MethodGet, "/v1/articles?sort_by=published_date", nil)
	if got := articleOrderByClause(anonReq, "published_date", "desc"); got != "" {
		t.Fatalf("anonymous sort must be unchanged, got %q", got)
	}
}
