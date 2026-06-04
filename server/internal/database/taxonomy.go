package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func EnsureTaxonomyTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS site_taxonomy (
			id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
			kind VARCHAR(32) NOT NULL,
			slug VARCHAR(255) NOT NULL,
			canonical_title VARCHAR(255) NOT NULL,
			parent_slug VARCHAR(255) NULL,
			article_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
			UNIQUE KEY uq_site_taxonomy_kind_slug (kind, slug),
			KEY idx_site_taxonomy_kind (kind),
			KEY idx_site_taxonomy_parent_slug (parent_slug)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	if err != nil {
		return err
	}

	_, err = conn.ExecContext(ctx, `
		ALTER TABLE site_taxonomy
		ADD COLUMN IF NOT EXISTS article_count BIGINT UNSIGNED NOT NULL DEFAULT 0
	`)
	return err
}

func parseTaxonomyCountCategories(value sql.NullString) []string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(value.String), &parsed); err != nil {
		return strings.Split(value.String, ",")
	}
	return parsed
}

func RebuildTaxonomyArticleCounts(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT categories
		FROM articles
		WHERE TRIM(COALESCE(categories, '')) <> ''
		  AND archived_at IS NULL
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var rawCategories sql.NullString
		if err := rows.Scan(&rawCategories); err != nil {
			return err
		}

		seen := make(map[string]struct{})
		for _, category := range parseTaxonomyCountCategories(rawCategories) {
			slug := CanonicalizeSlug(category)
			if slug == "" {
				continue
			}
			if _, ok := seen[slug]; ok {
				continue
			}
			seen[slug] = struct{}{}
			counts[slug] += 1
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE site_taxonomy
		SET article_count = 0
		WHERE kind IN ('section', 'subsection')
	`); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE site_taxonomy
		SET article_count = ?
		WHERE kind IN ('section', 'subsection') AND slug = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for slug, count := range counts {
		if _, err := stmt.ExecContext(ctx, count, slug); err != nil {
			return err
		}
	}

	return tx.Commit()
}
