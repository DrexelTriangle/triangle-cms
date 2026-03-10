package database

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var validSortDirections = map[string]bool{"asc": true, "desc": true}

const (
	paramID            = "id"
	paramSortBy        = "sort_by"
	paramSortDirection = "sort_direction"
	paramLimit         = "limit"
	paramOffset        = "offset"

	defaultLimit = 20
	maxLimit     = 100
)

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

func getStringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func getIntParam(params map[string]any, key string) int {
	value, _ := params[key].(int)
	return value
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
	column := getStringParam(params, paramSortBy)
	direction := strings.ToLower(getStringParam(params, paramSortDirection))

	if !allowed[strings.ToLower(column)] {
		return ""
	}
	if !validSortDirections[direction] {
		direction = "asc"
	}

	return fmt.Sprintf("ORDER BY %s %s", column, strings.ToUpper(direction))
}

func getPagination(params map[string]any) (int, int) {
	limit := getIntParam(params, paramLimit)
	offset := getIntParam(params, paramOffset)

	if limit <= 0 || limit > maxLimit {
		limit = defaultLimit
	}

	return limit, offset
}

func getLimitOffset(params map[string]any) (int, int) {
	return getPagination(params)
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

var reservedColumns = map[string]bool{"text": true}

func quoteColumn(col string) string {
	if reservedColumns[col] {
		return "`" + col + "`"
	}
	return col
}

func BuildSelectByIDSQL(table string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, paramID); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT * FROM %s WHERE id = ?", table), []any{params[paramID]}, nil
}

func BuildDeleteByIDSQL(table string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, paramID); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("DELETE FROM %s WHERE id = ?", table), []any{params[paramID]}, nil
}

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

func BuildUpdateFullSQL(table string, fields []string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, paramID); err != nil {
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
	args = append(args, params[paramID])

	query := fmt.Sprintf(
		"UPDATE %s\nSET %s\nWHERE id = ?",
		table,
		strings.Join(parts, ", "),
	)
	return query, args, nil
}

func BuildUpdatePartialSQL(table string, fields []string, params map[string]any) (string, []any, error) {
	if err := validateTable(table); err != nil {
		return "", nil, err
	}
	if err := requireParam(params, paramID); err != nil {
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
	args = append(args, params[paramID])

	return fmt.Sprintf("UPDATE %s\nSET %s\nWHERE id = ?", table, strings.Join(parts, ", ")), args, nil
}

type ListSQLOptions struct {
	Alias         string
	Join          string
	Conditions    []string
	Args          []any
	AllowedSortBy map[string]bool
	Params        map[string]any
}

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
	limit, offset := getPagination(opts.Params)
	args = append(args, limit, offset)

	clauses := []string{
		fmt.Sprintf("SELECT %s.*", opts.Alias),
		fmt.Sprintf("FROM %s %s", table, opts.Alias),
	}

	if strings.TrimSpace(opts.Join) != "" {
		clauses = append(clauses, opts.Join)
	}

	if whereClause := buildWhereClause(opts.Conditions); whereClause != "" {
		clauses = append(clauses, whereClause)
	}

	if orderByClause := buildOrderByClause(opts.Params, opts.AllowedSortBy); orderByClause != "" {
		clauses = append(clauses, orderByClause)
	}

	clauses = append(clauses, "LIMIT ? OFFSET ?")

	return strings.Join(clauses, "\n"), args, nil
}

