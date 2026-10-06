package database

import (
	"context"
	"database/sql"

	"server/internal/newsletter"
)

// NewsletterArticleLookup resolves the articles a newsletter links to, read
// fresh on every save and render so a locked link always reflects the
// article's current slug, title and publication state.
type NewsletterArticleLookup struct {
	Conn *sql.DB
}

var _ newsletter.ArticleLookup = (*NewsletterArticleLookup)(nil)

// ArticleIDsBySlug maps slugs to ids. Archived articles are skipped. A
// scheduled or draft article still resolves, so an editor can link a story
// before it goes live; the render warns until it does. Should a slug still be
// shared by two live rows, the newer one wins.
func (l *NewsletterArticleLookup) ArticleIDsBySlug(ctx context.Context, slugs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(slugs) == 0 {
		return out, nil
	}
	args := make([]any, len(slugs))
	for i, s := range slugs {
		args[i] = s
	}
	rows, err := l.Conn.QueryContext(ctx,
		"SELECT slug, id FROM articles WHERE archived_at IS NULL AND slug IN ("+placeholders(len(slugs))+") ORDER BY id",
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			slug string
			id   int64
		)
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

// ArticlesByID returns what the renderer needs for each id that exists.
// Published means live on the public site: a past pub_date and not archived.
func (l *NewsletterArticleLookup) ArticlesByID(ctx context.Context, ids []int64) (map[int64]newsletter.ArticleRef, error) {
	out := map[int64]newsletter.ArticleRef{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := l.Conn.QueryContext(ctx, `
		SELECT id, slug, COALESCE(title, ''),
		       COALESCE(NULLIF(excerpt, ''), description, ''),
		       COALESCE(photo_url, ''), COALESCE(photo_alt, ''),
		       (pub_date IS NOT NULL AND pub_date <= UTC_TIMESTAMP() AND archived_at IS NULL)
		  FROM articles WHERE id IN (`+placeholders(len(ids))+`)`, int64Args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref newsletter.ArticleRef
		if err := rows.Scan(&ref.ID, &ref.Slug, &ref.Title, &ref.Excerpt, &ref.ImageURL, &ref.ImageAlt, &ref.Published); err != nil {
			return nil, err
		}
		out[ref.ID] = ref
	}
	return out, rows.Err()
}
