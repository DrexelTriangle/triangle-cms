package database

import (
	"context"
	"database/sql"

	"server/internal/models"
)

// Featured articles are the ones an editor has pinned to the top of the
// homepage, stored in the legacy `priority` column.
//
// The flag used to be exclusive, cleared on every write so that one article
// carried it. It is not any more: a second breaking story should be able to go
// up without taking the first one down, which is what pinning by hand and then
// pinning again meant before.
//
// Nothing clears the flag on an editor's behalf, so a pin comes down when
// somebody unticks it. Order is by recency, so the newest pinned story leads
// and an editor who wants the other one first unpins the newer.

const featuredArticleConditions = "WHERE `priority` = 1 AND `pub_date` IS NOT NULL AND `pub_date` <= UTC_TIMESTAMP() AND `archived_at` IS NULL"

// MaxFeaturedArticles caps how many pinned stories lead the homepage. Past
// this the news block is all pins and the homepage stops being a rundown.
const MaxFeaturedArticles = 3

// GetFeaturedArticles returns the pinned articles, newest first, at most limit
// of them. Unpublished, scheduled and archived rows are skipped so an article
// cannot reach the homepage through the flag alone: an editor who pins a draft
// and forgets to publish it gets the normal newest-first lead, not a headline
// the public should not see yet.
func GetFeaturedArticles(ctx context.Context, conn *sql.DB, limit int) ([]models.Article, error) {
	if limit <= 0 {
		return nil, nil
	}
	query := searchSelectColumns + featuredArticleConditions + " ORDER BY `pub_date` DESC, `id` DESC LIMIT ?"
	rows, err := conn.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return CollectArticles(rows)
}
