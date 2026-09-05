package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	db "server/internal/database"
	"server/internal/models"
)

// leadWithFeaturedArticles moves the pinned articles to the front of the
// homepage news block, newest first. It is a no-op when nothing is pinned, so
// the default homepage stays newest-first.
//
// A pinned article keeps its place in its own section block: a pinned sports
// story leads the homepage and still appears in the sports rundown, which is
// what a lead story does in print. Only the news block dedupes, so a pinned
// news story is promoted rather than duplicated.
func leadWithFeaturedArticles(r *http.Request, conn *sql.DB, homepage *models.HomepageResponse, excerptWords, newsLimit int, newsMatchSlugs []string) error {
	featured, err := db.GetFeaturedArticles(r.Context(), conn, db.MaxFeaturedArticles)
	if err != nil || len(featured) == 0 {
		return err
	}

	if err := db.PopulateArticleAuthors(r.Context(), conn, featured); err != nil {
		return err
	}
	items := articleListItems(featured, excerptWords, newsMatchSlugs...)
	if len(items) == 0 {
		return nil
	}
	homepage.News = spliceFeaturedLeads(homepage.News, items, newsLimit)
	return nil
}

// spliceFeaturedLeads puts the pinned articles at the head of the news block in
// the order given, dropping the copies already in the list so a pinned news
// story is promoted rather than printed twice. The list is re-trimmed to limit
// because splicing in stories from other sections would otherwise push the
// block past the layout it was sized for.
func spliceFeaturedLeads(news []models.ArticleListItem, featured []models.ArticleListItem, limit int) []models.ArticleListItem {
	out := make([]models.ArticleListItem, 0, len(news)+len(featured))
	pinned := make(map[int64]bool, len(featured))
	for _, item := range featured {
		// A pin is only ever meant to move a story up. Two pins on one id
		// cannot happen through the API, but a duplicate here would print the
		// same card twice, which is worse than dropping one.
		if pinned[item.ID] {
			continue
		}
		pinned[item.ID] = true
		out = append(out, item)
	}
	for _, item := range news {
		if !pinned[item.ID] {
			out = append(out, item)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// @Summary Get homepage data
// @Tags homepage
// @Produce json
// @Param excerpt_words query int false "Max words in excerpt" default(50)
// @Success 200 {object} models.HomepageResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/homepage [get]
func GetHomepage(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sections := [...]struct {
			slug  string
			key   string
			limit int
		}{
			{slug: "news", key: "news", limit: 13},
			{slug: "opinion", key: "opinion", limit: 5},
			{slug: "sports", key: "sports", limit: 6},
			{slug: "entertainment", key: "entertainment", limit: 8},
			{slug: "comics-puzzles", key: "candp", limit: 6},
			{slug: "columns", key: "columns", limit: 5},
		}
		_, _, offset := listParams(r, 20)
		excerptWords := excerptWordLimit(r, 50)
		breakingNews, err := db.GetBreakingNews(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		carousel, err := db.GetHomepageCarousel(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		storyTitles, err := db.GetDevelopingStories(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		developingStories := make([]models.HomepageDevelopingStory, 0, len(storyTitles))
		for idx, title := range storyTitles {
			slug := db.CanonicalizeSlug(title)
			if slug == "" {
				slug = fmt.Sprintf("developing-story-%d", idx+1)
			}
			developingStories = append(developingStories, models.HomepageDevelopingStory{
				Slug:       slug,
				Link:       slug,
				Title:      title,
				Excerpt:    "",
				ShowInNews: false,
				Label:      []models.HomepageLabel{},
			})
		}

		sectionArticles := models.HomepageResponse{
			BreakingNews:      breakingNews,
			Carousel:          db.PublishedHomepageCarousel(carousel),
			DevelopingStories: developingStories,
		}

		// Captured from the news pass so the featured article, which may come
		// from any section, gets the same category ordering as the block it is
		// being spliced into.
		var newsMatchSlugs []string
		newsLimit := 0

		for _, section := range sections {
			if err := func(section struct {
				slug  string
				key   string
				limit int
			}) error {
				// Resolved rather than literal so a homepage block for a
				// container section (one with no category of its own) still
				// shows its subsections' articles.
				matchSlugs, err := sectionMatchSlugs(r.Context(), conn, section.slug)
				if err != nil {
					return err
				}
				params := ArticleParams{Section: section.slug, SectionMatchSlugs: matchSlugs}
				limit := section.limit
				rows, err := queryArticles(r, conn, params, limit+1, offset)
				if err != nil {
					return err
				}
				defer rows.Close()

				articles, err := db.CollectArticles(rows)
				if err != nil {
					return err
				}
				if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
					return err
				}
				hasMore := len(articles) > limit
				if hasMore {
					articles = articles[:limit]
				}
				switch section.key {
				case "news":
					newsMatchSlugs = matchSlugs
					newsLimit = limit
					sectionArticles.News = articleListItems(articles, excerptWords, matchSlugs...)
				case "opinion":
					sectionArticles.Opinion = articleListItems(articles, excerptWords, matchSlugs...)
				case "sports":
					sectionArticles.Sports = articleListItems(articles, excerptWords, matchSlugs...)
				case "entertainment":
					sectionArticles.Entertainment = articleListItems(articles, excerptWords, matchSlugs...)
				case "candp":
					sectionArticles.CAndP = articleListItems(articles, excerptWords, matchSlugs...)
				case "columns":
					sectionArticles.Columns = articleListItems(articles, excerptWords, matchSlugs...)
				}
				return nil
			}(section); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// The homepage lead is the first entry of the news block (Scalene's
		// "3-6-3" layout renders news[0] as the big centre card), so pinning an
		// article means moving it to the front of that list. Pins are spliced in
		// rather than sorted into the news query because a pinned article may be
		// filed under any section: a pinned sports story still takes the lead
		// card, which a news-scoped ORDER BY could never do.
		if offset == 0 {
			if err := leadWithFeaturedArticles(r, conn, &sectionArticles, excerptWords, newsLimit, newsMatchSlugs); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// The homepage carries the editor's live decisions (the featured lead,
		// the breaking-news banner, the carousel) so it needs the shortest
		// freshness bound of anything here, which is why publicReadCacheControl
		// is set to one that suits it. The rest of the public reads inherited
		// the same bound rather than each picking one.
		setAlwaysPublicCache(w)
		writeJSON(w, http.StatusOK, sectionArticles)
	}
}
