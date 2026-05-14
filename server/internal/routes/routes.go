package routes

import (
	"database/sql"
	"net/http"
	"server/internal/handlers"
)

func Register(mux *http.ServeMux, conn *sql.DB) {
	// Authors
	mux.HandleFunc("GET /users", handlers.Users)
	mux.HandleFunc("GET /v1/authors", handlers.GetAuthors(conn))
	mux.HandleFunc("POST /v1/authors", handlers.PostAuthors(conn))
	mux.HandleFunc("GET /v1/authors/{slug}", handlers.GetAuthor(conn))
	mux.HandleFunc("PUT /v1/authors/{slug}", handlers.PutAuthor(conn))
	mux.HandleFunc("PATCH /v1/authors/{slug}", handlers.PatchAuthor(conn))
	mux.HandleFunc("DELETE /v1/authors/{slug}", handlers.DeleteAuthor(conn))
	mux.HandleFunc("GET /v1/authors/{slug}/articles", handlers.GetAuthorArticles(conn))

	// Articles
	mux.HandleFunc("GET /v1/articles", handlers.GetArticles(conn))
	mux.HandleFunc("GET /v1/articles/{slug}", handlers.GetArticle(conn))
	mux.HandleFunc("GET /v1/search", handlers.GetSearch(conn))
	mux.HandleFunc("POST /v1/articles", handlers.PostArticles(conn))
	mux.HandleFunc("PUT /v1/articles/{slug}", handlers.PutArticle(conn))
	mux.HandleFunc("PATCH /v1/articles/{slug}", handlers.PatchArticle(conn))
	mux.HandleFunc("DELETE /v1/articles/{slug}", handlers.DeleteArticle(conn))

	// Sections
	mux.HandleFunc("GET /v1/sections/{section_slug}/articles", handlers.GetSectionArticles(conn))
	mux.HandleFunc("GET /v1/subsections/{subsection_slug}/articles", handlers.GetSubsectionArticles(conn))

	// Taxonomy (categories, sections, tags)
	mux.HandleFunc("GET /v1/taxonomy", handlers.GetTaxonomy(conn))
	mux.HandleFunc("POST /v1/taxonomy", handlers.PostTaxonomy(conn))
	mux.HandleFunc("GET /v1/taxonomy/{kind}/{slug}", handlers.GetTaxonomyItem(conn))
	mux.HandleFunc("PUT /v1/taxonomy/{kind}/{slug}", handlers.PutTaxonomyItem(conn))
	mux.HandleFunc("DELETE /v1/taxonomy/{kind}/{slug}", handlers.DeleteTaxonomyItem(conn))

	// Media
	mux.HandleFunc("GET /v1/media", handlers.Users)
	mux.HandleFunc("POST /v1/media", handlers.Users)
	mux.HandleFunc("GET /v1/media/{id}", handlers.Users)
	mux.HandleFunc("PUT /v1/media/{id}", handlers.Users)
	mux.HandleFunc("PATCH /v1/media/{id}", handlers.Users)
	mux.HandleFunc("DELETE /v1/media/{id}", handlers.Users)
	mux.HandleFunc("GET /v1/media/gallery", handlers.Users)

	// Homepage
	mux.HandleFunc("GET /v1/homepage", handlers.GetHomepage(conn))
}
