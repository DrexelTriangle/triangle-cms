package database

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHasNonEmptyParam(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		key    string
		want   bool
	}{
		{name: "missing key", params: map[string]any{}, key: "id", want: false},
		{name: "nil value", params: map[string]any{"id": nil}, key: "id", want: false},
		{name: "empty string", params: map[string]any{"id": ""}, key: "id", want: false},
		{name: "spaces only", params: map[string]any{"id": "   "}, key: "id", want: false},
		{name: "valid string", params: map[string]any{"id": "10"}, key: "id", want: true},
		{name: "zero int valid", params: map[string]any{"offset": 0}, key: "offset", want: true},
	}

	for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
			got := HasNonEmptyParam(tt.params, tt.key)
			if got != tt.want {
				t.Fatalf("HasNonEmptyParam() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildOrderByClause_DefaultDirection(t *testing.T) {
	clause := buildOrderByClause(map[string]any{
		"sort_by":        "title",
		"sort_direction": "invalid",
	}, map[string]bool{"title": true})

	if clause != "ORDER BY title ASC" {
		t.Fatalf("unexpected clause: %q", clause)
	}
}

func TestBuildOrderByClause_InvalidColumn(t *testing.T) {
	clause := buildOrderByClause(map[string]any{
		"sort_by":        "bad_col",
		"sort_direction": "asc",
	}, map[string]bool{"title": true})

	if clause != "" {
		t.Fatalf("expected empty clause for invalid column, got: %q", clause)
	}
}

func TestGetLimitOffset_DefaultLimit(t *testing.T) {
	limit, offset := getLimitOffset(map[string]any{"limit": 0, "offset": 3})
	if limit != 20 || offset != 3 {
		t.Fatalf("got (%d, %d), want (20, 3)", limit, offset)
	}

	limit, offset = getLimitOffset(map[string]any{"limit": 101, "offset": 1})
	if limit != 20 || offset != 1 {
		t.Fatalf("got (%d, %d), want (20, 1)", limit, offset)
	}
}

func TestBuildUpdatePartialSQL(t *testing.T) {
	query, args, err := BuildUpdatePartialSQL("authors", []string{"display_name", "first_name", "last_name", "email", "login"}, map[string]any{
		"first_name": "A",
		"email":      "a@b.com",
		"id":         1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "first_name = ?") || !strings.Contains(query, "email = ?") {
		t.Fatalf("unexpected query: %s", query)
	}
	want := []any{"A", "a@b.com", 1}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i, a := range args {
		if a != want[i] {
			t.Fatalf("args[%d] = %v, want %v", i, a, want[i])
		}
	}
}

func TestBuildUpdatePartialSQL_NoFields(t *testing.T) {
	_, _, err := BuildUpdatePartialSQL("authors", []string{"first_name", "email"}, map[string]any{"id": 1})
	if err == nil || err.Error() != "at least one patch field is required" {
		t.Fatalf("expected patch-field error, got: %v", err)
	}
}

func TestNormalizeSQL(t *testing.T) {
	input := "\n  SELECT *  \n\n  FROM authors\n"
	got := normalizeSQL(input)
	want := "SELECT *\nFROM authors"
	if got != want {
		t.Fatalf("normalizeSQL() = %q, want %q", got, want)
	}
}

func TestBuildWhereClause(t *testing.T) {
	if buildWhereClause(nil) != "" {
		t.Fatal("expected empty string for no conditions")
	}
	got := buildWhereClause([]string{"a = ?", "b = ?"})
	if got != "WHERE a = ? AND b = ?" {
		t.Fatalf("unexpected clause: %q", got)
	}
}

func TestBuildSelectByIDSQL(t *testing.T) {
	query, args, err := BuildSelectByIDSQL("authors", map[string]any{"id": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "SELECT * FROM authors WHERE id = ?" {
		t.Fatalf("unexpected query: %s", query)
	}
	if !reflect.DeepEqual(args, []any{5}) {
		t.Fatalf("args = %#v, want %#v", args, []any{5})
	}
}

func TestBuildSelectByIDSQL_MissingID(t *testing.T) {
	_, _, err := BuildSelectByIDSQL("authors", map[string]any{})
	if err == nil || err.Error() != "missing required field: id" {
		t.Fatalf("expected missing id error, got: %v", err)
	}
}

func TestBuildSelectByIDSQL_InvalidTable(t *testing.T) {
	_, _, err := BuildSelectByIDSQL("users", map[string]any{"id": 1})
	if err == nil || err.Error() != "invalid table: users" {
		t.Fatalf("expected invalid table error, got: %v", err)
	}
}

func TestBuildDeleteByIDSQL(t *testing.T) {
	query, args, err := BuildDeleteByIDSQL("articles", map[string]any{"id": 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query != "DELETE FROM articles WHERE id = ?" {
		t.Fatalf("unexpected query: %s", query)
	}
	if !reflect.DeepEqual(args, []any{9}) {
		t.Fatalf("args = %#v, want %#v", args, []any{9})
	}
}

func TestBuildInsertSQL(t *testing.T) {
	fields := []string{"name", "email"}
	params := map[string]any{"name": "Alice", "email": "a@example.com"}
	query, args, err := BuildInsertSQL("authors", fields, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "INSERT INTO authors") || !strings.Contains(query, "VALUES (?, ?)") {
		t.Fatalf("unexpected query: %s", query)
	}
	if !reflect.DeepEqual(args, []any{"Alice", "a@example.com"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildInsertSQL_ReservedColumn(t *testing.T) {
	fields := []string{"text"}
	params := map[string]any{"text": "body"}
	query, _, err := BuildInsertSQL("articles", fields, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "`text`") {
		t.Fatalf("expected backtick-quoted text column, got: %s", query)
	}
}

func TestBuildInsertSQL_MissingField(t *testing.T) {
	_, _, err := BuildInsertSQL("authors", []string{"name", "email"}, map[string]any{"name": "Alice"})
	if err == nil || err.Error() != "missing required field: email" {
		t.Fatalf("expected missing field error, got: %v", err)
	}
}

func TestBuildUpdateFullSQL(t *testing.T) {
	fields := []string{"name", "email"}
	params := map[string]any{"name": "Bob", "email": "b@example.com", "id": 3}
	query, args, err := BuildUpdateFullSQL("authors", fields, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(query, "UPDATE authors") || !strings.Contains(query, "WHERE id = ?") {
		t.Fatalf("unexpected query: %s", query)
	}
	if !reflect.DeepEqual(args, []any{"Bob", "b@example.com", 3}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestBuildUpdateFullSQL_MissingID(t *testing.T) {
	_, _, err := BuildUpdateFullSQL("authors", []string{"name", "email"}, map[string]any{"name": "Bob", "email": "b@example.com"})
	if err == nil || err.Error() != "missing required field: id" {
		t.Fatalf("expected missing id error, got: %v", err)
	}
}

func TestBuildUpdatePartialSQL_MissingID(t *testing.T) {
	_, _, err := BuildUpdatePartialSQL("authors", []string{"email"}, map[string]any{"email": "a@b.com"})
	if err == nil || err.Error() != "missing required field: id" {
		t.Fatalf("expected missing id error, got: %v", err)
	}
}

func TestBuildListSQL(t *testing.T) {
	tests := []struct {
		name         string
		opts         ListSQLOptions
		wantContains []string
		wantNotHave  []string
		wantArgs     []any
	}{
		{
			name: "no join no filter",
			opts: ListSQLOptions{
				Alias:         "a",
				AllowedSortBy: map[string]bool{},
				Params:        map[string]any{"limit": 10, "offset": 0},
			},
			wantContains: []string{"SELECT a.*", "FROM authors a", "LIMIT ? OFFSET ?"},
			wantNotHave:  []string{"JOIN", "WHERE", "ORDER BY"},
			wantArgs:     []any{10, 0},
		},
		{
			name: "with join and condition",
			opts: ListSQLOptions{
				Alias:         "a",
				Join:          "JOIN articles_authors aa ON a.id = aa.author_id",
				Conditions:    []string{"aa.articles_id = ?"},
				Args:          []any{7},
				AllowedSortBy: map[string]bool{"name": true},
				Params: map[string]any{
					"sort_by":        "name",
					"sort_direction": "desc",
					"limit":          5,
					"offset":         10,
				},
			},
			wantContains: []string{
				"JOIN articles_authors",
				"WHERE aa.articles_id = ?",
				"ORDER BY name DESC",
				"LIMIT ? OFFSET ?",
			},
			wantNotHave: []string{},
			wantArgs:    []any{7, 5, 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, args, err := BuildListSQL("authors", tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, part := range tt.wantContains {
				if !strings.Contains(query, part) {
					t.Fatalf("query missing %q\nquery:\n%s", part, query)
				}
			}
			for _, part := range tt.wantNotHave {
				if strings.Contains(query, part) {
					t.Fatalf("query should not have %q\nquery:\n%s", part, query)
				}
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tt.wantArgs)
			}
		})
	}
}

func TestBuildListSQL_InvalidTable(t *testing.T) {
	_, _, err := BuildListSQL("users", ListSQLOptions{Alias: "u", Params: map[string]any{"limit": 10, "offset": 0}})
	if err == nil || err.Error() != "invalid table: users" {
		t.Fatalf("expected invalid table error, got: %v", err)
	}
}

func TestBuildListSQL_InvalidAlias(t *testing.T) {
	_, _, err := BuildListSQL("authors", ListSQLOptions{Alias: "a;drop", Params: map[string]any{"limit": 10, "offset": 0}})
	if err == nil || err.Error() != "invalid alias: a;drop" {
		t.Fatalf("expected invalid alias error, got: %v", err)
	}
}

func TestBuildListSQL_InvalidJoin(t *testing.T) {
	_, _, err := BuildListSQL("authors", ListSQLOptions{
		Alias:  "a",
		Join:   "JOIN anything bad",
		Params: map[string]any{"limit": 10, "offset": 0},
	})
	if err == nil || err.Error() != "invalid join for table authors" {
		t.Fatalf("expected invalid join error, got: %v", err)
	}
}

func TestBuilderErrors_AreWrappable(t *testing.T) {
	_, _, err := BuildSelectByIDSQL("users", map[string]any{"id": 1})
	if !errors.Is(err, ErrInvalidTable) {
		t.Fatalf("expected ErrInvalidTable, got: %v", err)
	}

	_, _, err = BuildInsertSQL("authors", []string{"name", "email"}, map[string]any{"name": "Alice"})
	if !errors.Is(err, ErrMissingRequiredField) {
		t.Fatalf("expected ErrMissingRequiredField, got: %v", err)
	}

	_, _, err = BuildUpdatePartialSQL("authors", []string{"email"}, map[string]any{"id": 1})
	if !errors.Is(err, ErrNoPatchFields) {
		t.Fatalf("expected ErrNoPatchFields, got: %v", err)
	}
}
