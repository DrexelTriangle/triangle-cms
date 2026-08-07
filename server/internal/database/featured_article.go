package database

import (
	"context"
	"database/sql"

	"server/internal/models"
)

// The featured article is the one an editor has pinned to the big lead card in
// the middle of the homepage. It is stored in the legacy `priority` column and
// is exclusive: exactly zero or one article carries it at a time, enforced on
// write by ClearFeaturedExcept.

const featuredArticleConditions = "WHERE `priority` = 1 AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL"

// GetFeaturedArticle returns the featured article, or nil when nothing is
// featured. Unpublished, scheduled and archived rows are skipped so an article
// cannot reach the homepage through the flag alone -- an editor who features a
// draft and forgets to publish it gets the normal newest-first lead, not a
// headline the public should not see yet.
//
// The ORDER BY is a defensive tiebreak: exclusivity is enforced on write, but a
// direct DB edit or an ETL reseed could leave two rows flagged, and the homepage
// must still resolve to one article rather than picking arbitrarily.
func GetFeaturedArticle(ctx context.Context, conn *sql.DB) (*models.Article, error) {
	query := searchSelectColumns + featuredArticleConditions + " ORDER BY `pub_date` DESC, `id` DESC LIMIT 1"
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles, err := CollectArticles(rows)
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, nil
	}
	return &articles[0], nil
}

// ClearFeaturedExcept unfeatures every article other than the given slug. Call
// it after the write that features an article, never before: if the ordering
// were reversed and the main update then failed, the site would be left with no
// featured article at all instead of the one it had.
func ClearFeaturedExcept(ctx context.Context, conn *sql.DB, slug string) error {
	_, err := conn.ExecContext(ctx,
		"UPDATE `articles` SET `priority` = 0 WHERE `priority` = 1 AND `slug` <> ?", slug)
	return err
}

// ClearFeaturedExceptID is ClearFeaturedExcept for the create path, where the
// slug may have been generated from the title rather than supplied by the
// caller and the insert's id is the only handle on the new row.
func ClearFeaturedExceptID(ctx context.Context, conn *sql.DB, id int64) error {
	_, err := conn.ExecContext(ctx,
		"UPDATE `articles` SET `priority` = 0 WHERE `priority` = 1 AND `id` <> ?", id)
	return err
}
