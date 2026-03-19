package routes

import (
	"net/http"
	"database/sql"
	"server/internal/handlers"
)

func Register(mux *http.ServeMux, conn *sql.DB) {
	// Authors
	mux.HandleFunc("GET /users", handlers.Users)
	mux.HandleFunc("GET /v1/authors", handlers.GetAuthors(conn))
	mux.HandleFunc("POST /v1/authors", handlers.PostAuthors(conn))
	mux.HandleFunc("GET /v1/authors/{id}", handlers.GetAuthor(conn))
	mux.HandleFunc("PUT /v1/authors/{id}", handlers.PutAuthor(conn))
	mux.HandleFunc("PATCH /v1/authors/{id}", handlers.PatchAuthor(conn))
	mux.HandleFunc("DELETE /v1/authors/{id}", handlers.DeleteAuthor(conn))
	mux.HandleFunc("GET /v1/authors/{id}/articles", handlers.GetAuthorArticles(conn))

	// Articles
    mux.HandleFunc("GET /v1/articles", handlers.GetArticles(conn))
    mux.HandleFunc("GET /v1/articles/{id}", handlers.GetArticle(conn))
    mux.HandleFunc("POST /v1/articles", handlers.PostArticles(conn))
    mux.HandleFunc("PUT /v1/articles/{id}", handlers.PutArticle(conn))
    mux.HandleFunc("PATCH /v1/articles/{id}", handlers.PatchArticle(conn))
    mux.HandleFunc("DELETE /v1/articles/{id}", handlers.DeleteArticle(conn))

	// Sections
	mux.HandleFunc("GET /v1/sections/{section_slug}/articles", handlers.Users)
	mux.HandleFunc("GET /v1/subsections/{subsection_slug}/articles", handlers.Users)

	// Media
    mux.HandleFunc("GET /v1/media", handlers.Users)
    mux.HandleFunc("POST /v1/media", handlers.Users)
    mux.HandleFunc("GET /v1/media/{id}", handlers.Users)
    mux.HandleFunc("PUT /v1/media/{id}", handlers.Users)
    mux.HandleFunc("PATCH /v1/media/{id}", handlers.Users)
    mux.HandleFunc("DELETE /v1/media/{id}", handlers.Users)
    mux.HandleFunc("GET /v1/media/gallery", handlers.Users)

	// Homepage
    mux.HandleFunc("GET /v1/homepage", handlers.Users)
}
