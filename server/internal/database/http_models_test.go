package database

import (
	"context"
	"strings"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
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

func TestNormalizePhotoURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "   ",
			want:  "",
		},
		{
			name:  "absolute https unchanged",
			input: "https://cdn.example.com/a.jpg",
			want:  "https://cdn.example.com/a.jpg",
		},
		{
			name:  "wp content without leading slash",
			input: "wp-content/uploads/2016/03/image.jpg",
			want:  "https://www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
		},
		{
			name:  "wp content with leading slash",
			input: "/wp-content/uploads/2016/03/image.jpg",
			want:  "https://www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
		},
		{
			name:  "host without scheme",
			input: "www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
			want:  "https://www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
		},
		{
			name:  "protocol relative",
			input: "//www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
			want:  "https://www.thetriangle.org/wp-content/uploads/2016/03/image.jpg",
		},
		{
			name:  "other relative unchanged",
			input: "images/image.jpg",
			want:  "images/image.jpg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePhotoURL(tc.input); got != tc.want {
				t.Fatalf("normalizePhotoURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
