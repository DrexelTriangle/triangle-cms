package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// article_categories indexes articles.categories as (article, category title) rows.
// The source column stays authoritative. Rebuild on startup to cover ETL reseeds;
// article writes maintain the index between rebuilds.

// maxCategoryLength matches the VARCHAR in schema/article_categories.sql. A
// longer title is skipped rather than truncated, because a truncated key would
// match a section it does not belong to.
const maxCategoryLength = 191

// Keep the listing's artifact filter on source columns. Derived indexes need not
// agree with them, and a generated column would rebuild FULLTEXT indexes
// while holding a metadata lock.

func EnsureArticleCategoriesTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, TableSchema("article_categories"))
	return err
}

// normalizeCategoryValues turns the stored `categories` JSON into the keys this
// table is indexed on.
//
// Only a well-formed JSON array counts. parseStringListField falls back to
// splitting on commas for rows that are not JSON, but the LIKE predicate this
// replaces anchored on JSON quotes and so never matched those rows either;
// indexing them here would quietly add articles to sections that have never
// shown them.
func normalizeCategoryValues(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil
	}
	return normalizeCategoryList(parsed)
}

// normalizeCategoryList lowercases, trims and dedupes category titles. The
// lowercasing mirrors the LOWER() the old predicate applied to the column, and
// is what lets CategoryMatchValues compare against these rows directly.
func normalizeCategoryList(categories []string) []string {
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		value := strings.ToLower(strings.TrimSpace(category))
		if value == "" || len(value) > maxCategoryLength {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, value)
		}
	}
	return out
}

// ReplaceArticleCategories rewrites one article's rows. Delete-then-insert
// rather than a diff: an article carries a handful of categories, and the
// simpler operation cannot leave a stale row behind.
func ReplaceArticleCategories(ctx context.Context, conn execer, articleID int64, categories []string) error {
	if _, err := conn.ExecContext(ctx, "DELETE FROM `article_categories` WHERE `article_id` = ?", articleID); err != nil {
		return err
	}
	values := normalizeCategoryList(categories)
	if len(values) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values)*2)
	for _, value := range values {
		placeholders = append(placeholders, "(?, ?)")
		args = append(args, articleID, value)
	}
	_, err := conn.ExecContext(ctx,
		"INSERT INTO `article_categories` (`article_id`, `category`) VALUES "+strings.Join(placeholders, ", "),
		args...,
	)
	return err
}

// ReplaceArticleCategoriesBySlug is ReplaceArticleCategories for the update
// paths, which address an article by slug. A slug that matches nothing is not an
// error here: the caller's own UPDATE already reported that as a 404, and
// failing again would turn one missing article into a 500.
func ReplaceArticleCategoriesBySlug(ctx context.Context, conn *sql.DB, slug string, categories []string) error {
	var articleID int64
	switch err := conn.QueryRowContext(ctx,
		"SELECT `id` FROM `articles` WHERE `slug` = ? LIMIT 1", strings.TrimSpace(slug),
	).Scan(&articleID); err {
	case nil:
	case sql.ErrNoRows:
		return nil
	default:
		return err
	}
	return ReplaceArticleCategories(ctx, conn, articleID, categories)
}

// RebuildArticleCategories rebuilds the whole table from `articles`.
//
// Run at every startup. It is cheap (the corpus is under ten thousand rows and
// two or three categories each) and running it unconditionally is what makes
// the table safe to depend on: after a WordPress ETL reseed, which replaces
// `articles` wholesale and renumbers its ids, the previous contents are not
// merely stale but point at the wrong articles.
func RebuildArticleCategories(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, "SELECT `id`, `categories` FROM `articles`")
	if err != nil {
		return err
	}
	defer rows.Close()

	type articleCategories struct {
		id         int64
		categories []string
	}
	var pending []articleCategories
	for rows.Next() {
		var id int64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		values := normalizeCategoryValues(raw.String)
		if len(values) == 0 {
			continue
		}
		pending = append(pending, articleCategories{id: id, categories: values})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// The scan is finished before the writes start: holding a result set open
	// across them would need a second connection from the pool, and the pool is
	// the resource this whole change is trying to spend less of.

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// TRUNCATE rather than DELETE: this is a full replacement, and it leaves no
	// undo log to write for rows that are about to be re-inserted anyway.
	if _, err := tx.ExecContext(ctx, "TRUNCATE TABLE `article_categories`"); err != nil {
		return err
	}

	// Batched, because one INSERT per article is ten thousand round trips on
	// every boot.
	const batchSize = 500
	placeholders := make([]string, 0, batchSize)
	args := make([]any, 0, batchSize*2)
	flush := func() error {
		if len(placeholders) == 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			"INSERT INTO `article_categories` (`article_id`, `category`) VALUES "+strings.Join(placeholders, ", "),
			args...,
		)
		placeholders = placeholders[:0]
		args = args[:0]
		return err
	}

	for _, article := range pending {
		for _, category := range article.categories {
			placeholders = append(placeholders, "(?, ?)")
			args = append(args, article.id, category)
			if len(placeholders) >= batchSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return tx.Commit()
}
