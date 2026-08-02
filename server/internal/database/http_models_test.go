package database

import (
	"context"
	"strings"
	"testing"

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
