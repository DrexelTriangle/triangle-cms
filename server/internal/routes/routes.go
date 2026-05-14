package routes

import (
	"database/sql"
	"net/http"
	"server/internal/handlers"
	"server/internal/middleware"

	"github.com/coreos/go-oidc/v3/oidc"
	httpSwagger "github.com/swaggo/http-swagger"
)

func Register(mux *http.ServeMux, conn *sql.DB, verifier *oidc.IDTokenVerifier) {
	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("GET /v1/health", handlers.HealthCheck)
	mux.HandleFunc("GET /v1/health/db", handlers.HealthReady(conn))

	auth := func(h http.Handler) http.Handler { return h }
	if verifier != nil {
		auth = middleware.RequireAuth(verifier, conn)
	}
	adminOnly := middleware.RequireAdmin

	mux.HandleFunc("GET /users", handlers.Users)

	mux.Handle("GET /v1/authors", auth(handlers.GetAuthors(conn)))
	mux.Handle("GET /v1/authors/{slug}", auth(handlers.GetAuthor(conn)))
	mux.Handle("GET /v1/authors/{slug}/articles", auth(handlers.GetAuthorArticles(conn)))
	mux.Handle("POST /v1/authors", auth(adminOnly(handlers.PostAuthors(conn))))
	mux.Handle("PUT /v1/authors/{slug}", auth(adminOnly(handlers.PutAuthor(conn))))
	mux.Handle("PATCH /v1/authors/{slug}", auth(adminOnly(handlers.PatchAuthor(conn))))
	mux.Handle("DELETE /v1/authors/{slug}", auth(adminOnly(handlers.DeleteAuthor(conn))))

	mux.Handle("GET /v1/articles", auth(handlers.GetArticles(conn)))
	mux.Handle("GET /v1/articles/{slug}", auth(handlers.GetArticle(conn)))
	mux.Handle("GET /v1/search", auth(handlers.GetSearch(conn)))
	mux.Handle("POST /v1/articles", auth(handlers.PostArticles(conn)))
	mux.Handle("PUT /v1/articles/{slug}", auth(handlers.PutArticle(conn)))
	mux.Handle("PATCH /v1/articles/{slug}", auth(handlers.PatchArticle(conn)))
	mux.Handle("DELETE /v1/articles/{slug}", auth(adminOnly(handlers.DeleteArticle(conn))))

	mux.Handle("GET /v1/sections/{section_slug}/articles", auth(handlers.GetSectionArticles(conn)))
	mux.Handle("GET /v1/subsections/{subsection_slug}/articles", auth(handlers.GetSubsectionArticles(conn)))

	mux.Handle("GET /v1/media", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("POST /v1/media", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("GET /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("PUT /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("PATCH /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("DELETE /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("GET /v1/media/gallery", auth(http.HandlerFunc(handlers.Users)))

	mux.Handle("GET /v1/homepage", auth(handlers.GetHomepage(conn)))

	mux.Handle("GET /v1/users/me", auth(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", auth(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", auth(adminOnly(handlers.PatchUser(conn))))
}
