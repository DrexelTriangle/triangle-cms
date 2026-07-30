package database

import (
	"embed"
	"fmt"
	"strings"
)

// schemaFS holds the canonical CREATE TABLE definition for every CMS-owned
// table. These same files are read by scripts/generate_wordpress_sql.py when it
// writes the local seed SQL, so the schema a dev database is seeded with and the
// schema the CMS converges to at startup cannot drift apart. See
// schema/README.md.
//
//go:embed schema/*.sql
var schemaFS embed.FS

// TableSchema returns the canonical CREATE TABLE statement for table. It panics
// if the definition is missing, because that means the binary was built without
// a schema file it needs and it could only fail later, at first request, against
// a table that was never created.
func TableSchema(table string) string {
	body, err := schemaFS.ReadFile("schema/" + table + ".sql")
	if err != nil {
		panic(fmt.Sprintf("database: no canonical schema for table %q: %v", table, err))
	}
	return strings.TrimSpace(string(body))
}
