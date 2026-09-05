package database

import (
	"embed"
	"fmt"
	"strings"
)

// schemaFS shares table definitions with scripts/generate_wordpress_sql.py.
//
//go:embed schema/*.sql
var schemaFS embed.FS

// TableSchema returns a CREATE TABLE statement, panicking on a missing embedded file.
func TableSchema(table string) string {
	body, err := schemaFS.ReadFile("schema/" + table + ".sql")
	if err != nil {
		panic(fmt.Sprintf("database: no canonical schema for table %q: %v", table, err))
	}
	return strings.TrimSpace(string(body))
}
