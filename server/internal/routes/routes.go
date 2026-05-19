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
	registerPublicRoutes(mux, conn)

	if verifier == nil {
		return
	}

	registerProtectedRoutes(mux, conn, middleware.RequireAuth(verifier, conn), middleware.RequireAdmin)
}

func registerPublicRoutes(mux *http.ServeMux, conn *sql.DB) {
	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("GET /v1/health", handlers.HealthCheck)
	mux.HandleFunc("GET /v1/health/db", handlers.HealthReady(conn))

	mux.Handle("GET /v1/authors", handlers.GetAuthors(conn))
	mux.Handle("GET /v1/authors/{slug}", handlers.GetAuthor(conn))
	mux.Handle("GET /v1/authors/{slug}/articles", handlers.GetAuthorArticles(conn))

	mux.Handle("GET /v1/articles", handlers.GetArticles(conn))
	mux.Handle("GET /v1/articles/{slug}", handlers.GetArticle(conn))
	mux.Handle("GET /v1/search", handlers.GetSearch(conn))

	mux.Handle("GET /v1/sections/{section_slug}/articles", handlers.GetSectionArticles(conn))
	mux.Handle("GET /v1/subsections/{subsection_slug}/articles", handlers.GetSubsectionArticles(conn))

	mux.Handle("GET /v1/media", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/media/{id}", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/media/gallery", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/homepage", handlers.GetHomepage(conn))
}

func registerProtectedRoutes(
	mux *http.ServeMux,
	conn *sql.DB,
	auth func(http.Handler) http.Handler,
	adminOnly func(http.Handler) http.Handler,
) {
	mux.Handle("POST /v1/authors", auth(adminOnly(handlers.PostAuthors(conn))))
	mux.Handle("PUT /v1/authors/{slug}", auth(adminOnly(handlers.PutAuthor(conn))))
	mux.Handle("PATCH /v1/authors/{slug}", auth(adminOnly(handlers.PatchAuthor(conn))))
	mux.Handle("DELETE /v1/authors/{slug}", auth(adminOnly(handlers.DeleteAuthor(conn))))

	mux.Handle("POST /v1/articles", auth(handlers.PostArticles(conn)))
	mux.Handle("PUT /v1/articles/{slug}", auth(handlers.PutArticle(conn)))
	mux.Handle("PATCH /v1/articles/{slug}", auth(handlers.PatchArticle(conn)))
	mux.Handle("DELETE /v1/articles/{slug}", auth(adminOnly(handlers.DeleteArticle(conn))))

	mux.Handle("POST /v1/media", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("PUT /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("PATCH /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))
	mux.Handle("DELETE /v1/media/{id}", auth(http.HandlerFunc(handlers.Users)))

	mux.Handle("GET /v1/users/me", auth(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", auth(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", auth(adminOnly(handlers.PatchUser(conn))))
}
