package database

import (
	"context"
	"database/sql"
	"fmt"
)

// newsletterTable pairs a CMS-owned newsletter table with the expand-only
// column block that brings an existing copy of it up to the canonical schema.
// The CREATE in schema/<name>.sql is a no-op against a table that already
// exists, so a column added there reaches an existing database only through
// this block. Keep the two in step.
type newsletterTable struct {
	name    string
	columns string
}

// Parents before children: the join tables' foreign keys need their targets.
var newsletterTables = []newsletterTable{
	{"newsletter_lists", `
		ADD COLUMN IF NOT EXISTS name VARCHAR(128) NOT NULL,
		ADD COLUMN IF NOT EXISTS description VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS is_public TINYINT(1) NOT NULL DEFAULT 1,
		ADD COLUMN IF NOT EXISTS created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`},
	{"newsletter_subscribers", `
		ADD COLUMN IF NOT EXISTS email VARCHAR(254) NOT NULL,
		ADD COLUMN IF NOT EXISTS name VARCHAR(100) NULL,
		ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'subscribed',
		ADD COLUMN IF NOT EXISTS token CHAR(43) NOT NULL,
		ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL,
		ADD COLUMN IF NOT EXISTS signup_ip VARCHAR(45) NULL,
		ADD COLUMN IF NOT EXISTS subscribed_at DATETIME NOT NULL,
		ADD COLUMN IF NOT EXISTS unsubscribed_at DATETIME NULL,
		ADD COLUMN IF NOT EXISTS created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`},
	{"newsletter_subscriber_lists", ""},
	{"newsletter_campaigns", `
		ADD COLUMN IF NOT EXISTS subject VARCHAR(255) NOT NULL,
		ADD COLUMN IF NOT EXISTS preview_text VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS body_blocks LONGTEXT NOT NULL,
		ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'draft',
		ADD COLUMN IF NOT EXISTS scheduled_at DATETIME NULL,
		ADD COLUMN IF NOT EXISTS sent_at DATETIME NULL,
		ADD COLUMN IF NOT EXISTS recipient_count INT UNSIGNED NULL,
		ADD COLUMN IF NOT EXISTS created_by VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS updated_by VARCHAR(255) NULL,
		ADD COLUMN IF NOT EXISTS created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP`},
	{"newsletter_campaign_lists", ""},
}

// EnsureNewsletterTables creates the newsletter tables (lists, subscribers,
// campaigns and the two join tables) and expands existing copies. Additive
// only: it never drops or retypes a column a rolled-back binary still reads.
func EnsureNewsletterTables(ctx context.Context, conn *sql.DB) error {
	for _, table := range newsletterTables {
		if _, err := conn.ExecContext(ctx, TableSchema(table.name)); err != nil {
			return fmt.Errorf("create %s: %w", table.name, err)
		}
		if table.columns == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, "ALTER TABLE "+table.name+table.columns); err != nil {
			return fmt.Errorf("expand %s: %w", table.name, err)
		}
	}
	return nil
}
