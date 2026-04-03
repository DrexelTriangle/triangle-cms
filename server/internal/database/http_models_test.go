package database

import (
	"strings"
	"testing"
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
