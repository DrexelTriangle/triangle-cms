package handlers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	db "server/internal/database"
	"server/internal/models"
	"strings"
)

// @Summary Search articles
// @Tags articles
// @Produce json
// @Param q query string true "Search term"
// @Param limit query int false "Max results" default(20)
// @Param offset query int false "Offset"
// @Success 200 {array} models.ArticleListItem
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/search [get]
// QueryEmbedder turns a search query into a vector. It is an interface, and a
// nil one is valid: a deployment without the embedding sidecar serves lexical
// search through the same handler.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
}

func GetSearch(conn *sql.DB, embedder QueryEmbedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusOK, []any{})
			return
		}

		limit := intParam(r, "limit", 20)
		offset := intParam(r, "offset", 0)

		// The sidecar is on the request path here, so its failures must not be.
		// A timeout, a cold model, or no sidecar at all costs the semantic half of
		// the ranking; it never costs the reader their results.
		var queryVector []float32
		if embedder != nil {
			vector, err := embedder.EmbedQuery(r.Context(), q)
			if err != nil {
				slog.WarnContext(r.Context(), "query embedding unavailable; serving lexical search", "error", err)
			} else {
				queryVector = vector
			}
		}

		articles, err := db.SearchArticlesHybrid(r.Context(), conn, q, queryVector, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := db.PopulateArticleAuthors(r.Context(), conn, articles); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		resp := make([]models.ArticleListItem, 0, len(articles))
		for _, article := range articles {
			categories := make([]models.CategorySummary, 0, len(article.Categories))
			for _, category := range article.Categories {
				name := strings.TrimSpace(category)
				if name == "" {
					continue
				}
				categories = append(categories, models.CategorySummary{Name: name, Slug: db.CategoryLinkSlug(name)})
			}

			authors := make([]models.AuthorSummary, 0, len(article.Authors))
			for _, author := range article.Authors {
				authors = append(authors, models.AuthorSummary{ID: author.ID, Name: author.DisplayName, Slug: author.Slug})
			}

			item := models.ArticleListItem{
				Title:         article.Title,
				ID:            article.ID,
				Authors:       authors,
				Categories:    categories,
				Excerpt:       article.Excerpt,
				Slug:          article.Slug,
				Status:        article.Status,
				CommentStatus: article.CommentStatus,
				FeaturedImage: article.PhotoURL,
				IsFeatured:    article.IsFeatured,
				BreakingNews:  article.BreakingNews,
			}
			item.PublishedDate = article.PublishedAt
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
