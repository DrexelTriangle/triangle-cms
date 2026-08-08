package database

import (
	"context"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"server/internal/models"
)

func TestAuthorSortByColumn_UsesExistingColumns(t *testing.T) {
	for key, column := range AuthorSortByColumn {
		if column == "created_at" || column == "updated_at" {
			t.Fatalf("author sort key %q maps to non-existent column %q", key, column)
		}
	}
}

func TestBuildOrderLimit_UnsupportedAuthorSortByIgnored(t *testing.T) {
	query := BuildOrderLimit(
		"SELECT `id` FROM `authors` ORDER BY `id` DESC",
		"created_at",
		"desc",
		AuthorSortByColumn,
		20,
		0,
	)

	if strings.Count(query, "ORDER BY") != 1 {
		t.Fatalf("expected only default ORDER BY clause, got query: %s", query)
	}
	if strings.Contains(query, "created_at") {
		t.Fatalf("query should not include unsupported sort column: %s", query)
	}
}

func TestGetRelatedArticlesBySlug_Validation(t *testing.T) {
	ctx := context.Background()

	if _, err := GetRelatedArticlesBySlug(ctx, nil, "", 5); err == nil {
		t.Fatal("expected error for empty slug")
	}

	if _, err := GetRelatedArticlesBySlug(ctx, nil, "some-slug", 0); err == nil {
		t.Fatal("expected error for non-positive k")
	}
}

func TestIsVectorSearchUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "missing vector table",
			err:  &mysqlDriver.MySQLError{Number: 1146},
			want: true,
		},
		{
			name: "missing function",
			err:  &mysqlDriver.MySQLError{Number: 1305},
			want: true,
		},
		{
			name: "other mysql error",
			err:  &mysqlDriver.MySQLError{Number: 1062},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsVectorSearchUnavailableError(tc.err); got != tc.want {
				t.Fatalf("IsVectorSearchUnavailableError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestArticleInputToDBFields_PreservesBreakingNews(t *testing.T) {
	fields := ArticleInputToDBFields(models.ArticleInput{
		Title:        "Campus alert",
		Content:      "Body",
		Status:       models.ArticleStatusDraft,
		BreakingNews: true,
	})

	breakingNewsFieldIndex := 9
	got, ok := fields[breakingNewsFieldIndex].(bool)
	if !ok {
		t.Fatalf("breaking_news field has type %T, want bool", fields[breakingNewsFieldIndex])
	}
	if !got {
		t.Fatal("breaking_news field was not preserved")
	}
}

func TestArticleInputToDBFields_StoresFuturePublishDateAsSchedule(t *testing.T) {
	fields := ArticleInputToDBFields(models.ArticleInput{
		Title:         "Tomorrow's story",
		Content:       "Body",
		Status:        models.ArticleStatusPublished,
		PublishedDate: "2030-05-06T14:30:00Z",
	})

	publishedDateFieldIndex := 6
	if fields[publishedDateFieldIndex] != nil {
		t.Fatalf("pub_date = %v, want nil until schedule is due", fields[publishedDateFieldIndex])
	}
	scheduledDateFieldIndex := 18
	got, ok := fields[scheduledDateFieldIndex].(string)
	if !ok {
		t.Fatalf("scheduled_pub_date field has type %T, want string", fields[scheduledDateFieldIndex])
	}
	if got != "2030-05-06 14:30:00" {
		t.Fatalf("scheduled_pub_date = %q, want scheduled timestamp", got)
	}
}

func TestArticleInputToDBFields_StoresTags(t *testing.T) {
	fields := ArticleInputToDBFields(models.ArticleInput{
		Title:   "Tagged story",
		Content: "Body",
		Status:  models.ArticleStatusDraft,
		Tags:    []string{"campus", "housing"},
	})

	const tagsFieldIndex = 12
	got, ok := fields[tagsFieldIndex].(string)
	if !ok {
		t.Fatalf("tags field has type %T, want string", fields[tagsFieldIndex])
	}
	if got != `["campus","housing"]` {
		t.Fatalf("tags = %q, want JSON tag list", got)
	}
}

func TestArticleInputToDBFields_DefaultsEmptyTagsToJSONList(t *testing.T) {
	fields := ArticleInputToDBFields(models.ArticleInput{
		Title:   "Untagged story",
		Content: "Body",
		Status:  models.ArticleStatusDraft,
	})

	const tagsFieldIndex = 12
	if got := fields[tagsFieldIndex]; got != "[]" {
		t.Fatalf("empty tags = %v, want []", got)
	}
}

// A full replacement is still an edit, and it used to write mod_date = NULL.
// Two things depend on that column: the public sitemap's lastmod is
// COALESCE(mod_date, pub_date), and the embedding reconciler decides a vector is
// stale by comparing mod_date to when the article was last embedded. A NULL made
// a PUT the one edit the CMS could not see it had made.
func TestArticleToDBFields_StampsModifiedDate(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)

	fields := ArticleToDBFields(models.Article{
		Title:   "Rewritten story",
		Slug:    "rewritten-story",
		Content: "New body",
		Status:  models.ArticleStatusPublished,
	})

	// Column order is title, slug, excerpt, text, categories, pub_date, mod_date.
	const modDateFieldIndex = 6
	raw, ok := fields[modDateFieldIndex].(string)
	if !ok {
		t.Fatalf("mod_date field has type %T, want a formatted string", fields[modDateFieldIndex])
	}
	stamped, err := time.Parse("2006-01-02 15:04:05", raw)
	if err != nil {
		t.Fatalf("mod_date %q is not a valid datetime: %v", raw, err)
	}
	if stamped.Before(before) || stamped.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("mod_date = %s, want roughly now", raw)
	}
}

// An article normally opens with its featured image, so an excerpt derived by
// stripping tags used to start with the photo credit in the caption instead of
// the story.
func TestDeriveExcerpt_SkipsImageCaptions(t *testing.T) {
	cases := map[string]string{
		"editor and block-editor figures": `<figure class="wp-caption alignnone">` +
			`<img src="/x.jpg" alt=""><figcaption class="wp-caption-text">Photo by Jane Doe.</figcaption>` +
			`</figure><p>The story begins here.</p>`,
		"classic-editor caption div": `<div class="wp-caption alignnone"><img src="/x.jpg" alt="">` +
			`<p class="wp-caption-text">Photo by Jane Doe.</p></div><p>The story begins here.</p>`,
		"bare figcaption": `<figcaption>Photo by Jane Doe.</figcaption><p>The story begins here.</p>`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if got := deriveExcerpt(content); got != "The story begins here." {
				t.Errorf("deriveExcerpt = %q, want the article text without the caption", got)
			}
		})
	}
}

// A caption mid-article should not cut the excerpt short either.
func TestDeriveExcerpt_KeepsTextAroundAnInlineFigure(t *testing.T) {
	content := `<p>Before.</p><figure class="wp-caption"><img src="/x.jpg" alt="">` +
		`<figcaption class="wp-caption-text">Credit.</figcaption></figure><p>After.</p>`

	if got := deriveExcerpt(content); got != "Before. After." {
		t.Errorf("deriveExcerpt = %q, want both paragraphs and no caption", got)
	}
}

// A crossword post is a single [puzzleme ...] shortcode, which stripping tags
// leaves untouched: the derived excerpt was the shortcode source, embed ids and
// all, printed under the headline on the public site.
func TestDeriveExcerpt_DropsShortcodes(t *testing.T) {
	puzzleme := `<p>[puzzleme basepath='https://puzzleme.amuselabs.com/pmm/' ` +
		`set='4d204c915da55d641d981a020851d8b3990a78c1' id='dc2b9486' ` +
		`attribution='Made by Coco Li using the online <a href="https://amuselabs.com/games/crossword/" ` +
		`target="_blank">cross word generator</a> from Amuse Labs' type='crossword']</p>`

	cases := map[string]struct{ content, want string }{
		"puzzleme only":       {puzzleme, ""},
		"puzzleme with prose": {puzzleme + `<p>Solve it below.</p>`, "Solve it below."},
		"paired shortcode": {
			`<p>[gallery ids="1,2"]</p><p>Look at these.</p>`,
			"Look at these.",
		},
		"unknown shortcode with attributes": {
			`[somewidget size='3']<p>The story begins here.</p>`,
			"The story begins here.",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := deriveExcerpt(tc.content); got != tc.want {
				t.Errorf("deriveExcerpt = %q, want %q", got, tc.want)
			}
		})
	}
}

// The shortcode rule keys on attributes, not on brackets, so editorial
// interjections survive -- they are part of the sentence.
func TestDeriveExcerpt_KeepsEditorialBrackets(t *testing.T) {
	content := `<p>He said the vote was "unanimious" [sic] and [Editor's note: it was not].</p>`
	want := `He said the vote was "unanimious" [sic] and [Editor's note: it was not].`

	if got := deriveExcerpt(content); got != want {
		t.Errorf("deriveExcerpt = %q, want %q", got, want)
	}
}

// The listing column set must be the full set minus exactly the body. If a new
// column is added to articleColumnSet and quietly lands in summaryOmittedColumns
// too, listings stop returning it and the public site renders a blank field.
func TestArticleSummaryColumns_OmitOnlyTheBody(t *testing.T) {
	full := map[string]bool{}
	for _, name := range ArticleColumns {
		full[name] = true
	}
	summary := map[string]bool{}
	for _, name := range ArticleSummaryColumns {
		summary[name] = true
	}

	for name := range full {
		if name == "text" {
			continue
		}
		if !summary[name] {
			t.Errorf("column %q is missing from ArticleSummaryColumns", name)
		}
	}
	if summary["text"] {
		t.Error("ArticleSummaryColumns still selects `text`; listings do not read the body")
	}
	if len(ArticleSummaryColumns) != len(ArticleColumns)-1 {
		t.Errorf("ArticleSummaryColumns has %d columns, want %d", len(ArticleSummaryColumns), len(ArticleColumns)-1)
	}
}

// scanTargets is what makes the column list and the Scan positional agreement
// one fact rather than two. If they can disagree, every value after the
// mismatch lands in the wrong field.
func TestArticleScanTargets_MatchColumnCounts(t *testing.T) {
	var row articleRow
	if got, want := len(row.scanTargets(true)), len(ArticleColumns); got != want {
		t.Errorf("full scan targets = %d, want %d", got, want)
	}
	if got, want := len(row.scanTargets(false)), len(ArticleSummaryColumns); got != want {
		t.Errorf("summary scan targets = %d, want %d", got, want)
	}
}

func TestArticleSelectList_QualifiesForJoins(t *testing.T) {
	if got := articleSelectList(false, "a"); !strings.HasPrefix(got, "a.`id`, a.`title`") {
		t.Errorf("qualified select list = %q, want it to start with a.`id`, a.`title`", got)
	}
	if got := articleSelectList(false, ""); !strings.HasPrefix(got, "`id`, `title`") {
		t.Errorf("unqualified select list = %q, want it to start with `id`, `title`", got)
	}
	if strings.Contains(articleSelectList(false, "a"), "`text`") {
		t.Error("summary select list names `text`")
	}
	if !strings.Contains(articleSelectList(true, "a"), "`text`") {
		t.Error("full select list is missing `text`")
	}
}
