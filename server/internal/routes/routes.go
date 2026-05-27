package routes

import (
	"database/sql"
	"net/http"
	"server/internal/auth"
	"server/internal/handlers"
	"server/internal/middleware"

	"github.com/coreos/go-oidc/v3/oidc"
	httpSwagger "github.com/swaggo/http-swagger"
)

func Register(mux *http.ServeMux, conn *sql.DB, verifier *oidc.IDTokenVerifier, oidcCfg auth.OIDCConfig) {
	mux.Handle("/swagger/", httpSwagger.WrapHandler)

	mux.HandleFunc("GET /v1/health", handlers.HealthCheck)
	mux.HandleFunc("GET /v1/health/db", handlers.HealthReady(conn))

	// Auth (BFF)
	mux.HandleFunc("GET /v1/auth/login", handlers.AuthLogin(oidcCfg))
	mux.HandleFunc("GET /v1/auth/callback", handlers.AuthCallback(oidcCfg, verifier, conn))
	mux.HandleFunc("POST /v1/auth/logout", handlers.AuthLogout(conn))

	authMW := func(h http.Handler) http.Handler { return h }
	if verifier != nil {
		authMW = middleware.RequireAuth(verifier, conn, oidcCfg)
	}
	adminOnly := middleware.RequireAdmin

	mux.Handle("GET /v1/articles", handlers.GetArticles(conn))
	mux.Handle("GET /v1/articles/{slug}", handlers.GetArticle(conn))
	mux.Handle("GET /v1/search", handlers.GetSearch(conn))

	mux.Handle("GET /v1/authors", authMW(handlers.GetAuthors(conn)))
	mux.Handle("GET /v1/authors/{slug}", authMW(handlers.GetAuthor(conn)))
	mux.Handle("GET /v1/authors/{slug}/articles", authMW(handlers.GetAuthorArticles(conn)))
	mux.Handle("POST /v1/authors", authMW(adminOnly(handlers.PostAuthors(conn))))
	mux.Handle("PUT /v1/authors/{slug}", authMW(adminOnly(handlers.PutAuthor(conn))))
	mux.Handle("PATCH /v1/authors/{slug}", authMW(adminOnly(handlers.PatchAuthor(conn))))
	mux.Handle("DELETE /v1/authors/{slug}", authMW(adminOnly(handlers.DeleteAuthor(conn))))

	mux.Handle("GET /v1/articles", authMW(handlers.GetArticles(conn)))
	mux.Handle("GET /v1/articles/{slug}", authMW(handlers.GetArticle(conn)))
	mux.Handle("GET /v1/search", authMW(handlers.GetSearch(conn)))
	mux.Handle("POST /v1/articles", authMW(handlers.PostArticles(conn)))
	mux.Handle("PUT /v1/articles/{slug}", authMW(handlers.PutArticle(conn)))
	mux.Handle("PATCH /v1/articles/{slug}", authMW(handlers.PatchArticle(conn)))
	mux.Handle("DELETE /v1/articles/{slug}", authMW(adminOnly(handlers.DeleteArticle(conn))))

	// Taxonomy (categories, sections, tags)
	mux.HandleFunc("GET /v1/taxonomy", handlers.GetTaxonomy(conn))
	mux.HandleFunc("POST /v1/taxonomy", handlers.PostTaxonomy(conn))
	mux.HandleFunc("GET /v1/taxonomy/{type}/{slug}", handlers.GetTaxonomyItem(conn))
	mux.HandleFunc("PUT /v1/taxonomy/{type}/{slug}", handlers.PutTaxonomyItem(conn))
	mux.HandleFunc("DELETE /v1/taxonomy/{type}/{slug}", handlers.DeleteTaxonomyItem(conn))

	mux.Handle("GET /v1/sections/{section_slug}/articles", authMW(handlers.GetSectionArticles(conn)))
	mux.Handle("GET /v1/subsections/{subsection_slug}/articles", authMW(handlers.GetSubsectionArticles(conn)))

	mux.Handle("GET /v1/media", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("POST /v1/media", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("GET /v1/media/{id}", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("PUT /v1/media/{id}", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("PATCH /v1/media/{id}", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("DELETE /v1/media/{id}", authMW(http.HandlerFunc(handlers.Users)))
	mux.Handle("GET /v1/media/gallery", authMW(http.HandlerFunc(handlers.Users)))

	mux.Handle("GET /v1/homepage", authMW(handlers.GetHomepage(conn)))

	mux.Handle("GET /v1/users/me", authMW(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", authMW(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", authMW(adminOnly(handlers.PatchUser(conn))))
}
