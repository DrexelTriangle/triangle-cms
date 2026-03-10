package database

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// validSortDir is the whitelist used by buildOrderByClause.
var validSortDir = map[string]bool{"asc": true, "desc": true}

const (
	TableAuthors  = "authors"
	TableArticles = "articles"

	JoinAuthorsByArticle = "JOIN articles_authors aa ON a.id = aa.author_id"
	JoinArticlesByAuthor = "JOIN articles_authors aa ON art.id = aa.articles_id"
)

var (
	ErrInvalidTable            = errors.New("invalid table")
	ErrInvalidAlias            = errors.New("invalid alias")
	ErrInvalidJoin             = errors.New("invalid join")
	ErrMissingRequiredField    = errors.New("missing required field")
	ErrNoPatchFields           = errors.New("at least one patch field is required")
)

var allowedTables = map[string]bool{
	TableAuthors:  true,
	TableArticles: true,
}

var allowedJoinsByTable = map[string]map[string]bool{
	TableAuthors: {
		JoinAuthorsByArticle: true,
	},
	TableArticles: {
		JoinArticlesByAuthor: true,
	},
}

var identifierRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ─── Core helpers ─────────────────────────────────────────────────────────────

// HasNonEmptyParam reports whether params contains key with a non-nil, non-blank value.
func HasNonEmptyParam(params map[string]any, key string) bool {
	value, ok := params[key]
	if !ok || value == nil {
		return false
	}
	if str, isString := value.(string); isString {
		return strings.TrimSpace(str) != ""
	}
	return true
}

func normalizeSQL(query string) string {
	lines := strings.Split(query, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return strings.Join(normalized, "\n")
}

func buildWhereClause(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, " AND ")
}

func buildOrderByClause(params map[string]any, allowed map[string]bool) string {
	col, _ := params["sort_by"].(string)
	dir, _ := params["sort_direction"].(string)
	if !allowed[strings.ToLower(col)] {
		return ""
	}
	if !validSortDir[strings.ToLower(dir)] {
		dir = "ASC"
	}
	return fmt.Sprintf("ORDER BY %s %s", col, strings.ToUpper(dir))
}

func getLimitOffset(params map[string]any) (int, int) {
	limit, _ := params["limit"].(int)
	offset, _ := params["offset"].(int)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return limit, offset
}

func validateTable(table string) error {
	if !allowedTables[table] {
		return fmt.Errorf("%w: %s", ErrInvalidTable, table)
	}
	return nil
}

func validateAlias(alias string) error {
	if !identifierRegex.MatchString(alias) {
		return fmt.Errorf("%w: %s", ErrInvalidAlias, alias)
	}
	return nil
}

func validateJoinForTable(table, join string) error {
	if strings.TrimSpace(join) == "" {
		return nil
	}
	allowedByTable, ok := allowedJoinsByTable[table]
	if !ok || !allowedByTable[join] {
		return fmt.Errorf("%w for table %s", ErrInvalidJoin, table)
	}
	return nil
}

func requireParam(params map[string]any, key string) error {
	if !HasNonEmptyParam(params, key) {
		return fmt.Errorf("%w: %s", ErrMissingRequiredField, key)
	}
	return nil
}

func requireAllFields(params map[string]any, fields []string) error {
	for _, field := range fields {
		if err := requireParam(params, field); err != nil {
			return err
		}
	}
	return nil
}

// quoteColumn wraps reserved SQL words in backticks; others pass through.
var reservedColumns = map[string]bool{"text": true}

func quoteColumn(col string) string {
	if reservedColumns[col] {
		return "`" + col + "`"
	}
	return col
}

// ─── Generic builders ─────────────────────────────────────────────────────────

// BuildSelectByIDSQL builds a SELECT * for a single row by id.
func BuildSelectByIDSQL(table string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, "id"); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT * FROM %s WHERE id = ?", table), []any{params["id"]}, nil
}

// BuildDeleteByIDSQL builds a DELETE for a single row by id.
func BuildDeleteByIDSQL(table string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, "id"); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("DELETE FROM %s WHERE id = ?", table), []any{params["id"]}, nil
}

// BuildInsertSQL builds a full INSERT given an ordered field list.
func BuildInsertSQL(table string, fields []string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireAllFields(params, fields); err != nil {
		return "", nil, err
	}

	cols := make([]string, len(fields))
	placeholders := make([]string, len(fields))
	args := make([]any, len(fields))

	for i, f := range fields {
		cols[i] = quoteColumn(f)
		placeholders[i] = "?"
		args[i] = params[f]
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s)\nVALUES (%s)",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	return query, args, nil
}

// BuildUpdateFullSQL builds a full UPDATE (PUT semantics) given an ordered field list.
// All fields are required.
func BuildUpdateFullSQL(table string, fields []string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, "id"); err != nil {
		return "", nil, err
	}
	if err := requireAllFields(params, fields); err != nil {
		return "", nil, err
	}

	parts := make([]string, len(fields))
	args := make([]any, len(fields))

	for i, f := range fields {
		parts[i] = fmt.Sprintf("%s = ?", quoteColumn(f))
		args[i] = params[f]
	}
	args = append(args, params["id"])

	query := fmt.Sprintf(
		"UPDATE %s\nSET %s\nWHERE id = ?",
		table,
		strings.Join(parts, ", "),
	)
	return query, args, nil
}

// BuildUpdatePartialSQL builds a partial UPDATE (PATCH semantics) from whichever fields are present.
func BuildUpdatePartialSQL(table string, fields []string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, "id"); err != nil {
		return "", nil, err
	}

	var parts []string
	var args []any

	for _, f := range fields {
		if HasNonEmptyParam(params, f) {
			parts = append(parts, fmt.Sprintf("%s = ?", quoteColumn(f)))
			args = append(args, params[f])
		}
	}
	if len(parts) == 0 {
		return "", nil, ErrNoPatchFields
	}
	args = append(args, params["id"])

	return fmt.Sprintf("UPDATE %s\nSET %s\nWHERE id = ?", table, strings.Join(parts, ", ")), args, nil
}

// ListSQLOptions captures the variable parts of a paginated list query.
type ListSQLOptions struct {
	Alias         string          // table alias used in SELECT and clauses
	Join          string          // optional JOIN clause (empty = omitted)
	Conditions    []string        // WHERE conditions (combined with AND)
	Args          []any           // args corresponding to conditions
	AllowedSortBy map[string]bool // whitelist for sort_by values
	Params        map[string]any  // original params map (for ORDER BY + LIMIT/OFFSET)
}

// BuildListSQL builds a paginated SELECT with optional JOIN, WHERE, and ORDER BY.
func BuildListSQL(table string, opts ListSQLOptions) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := validateAlias(opts.Alias); err != nil {
		return "", nil, err
	}
	if err := validateJoinForTable(table, opts.Join); err != nil {
		return "", nil, err
	}

	args := append([]any{}, opts.Args...)
	limit, offset := getLimitOffset(opts.Params)
	args = append(args, limit, offset)

	query := fmt.Sprintf(`
SELECT %s.*
FROM %s %s
%s
%s
%s
LIMIT ? OFFSET ?`,
		opts.Alias,
		table, opts.Alias,
		opts.Join,
		buildWhereClause(opts.Conditions),
		buildOrderByClause(opts.Params, opts.AllowedSortBy),
	)

	return normalizeSQL(query), args, nil
}

