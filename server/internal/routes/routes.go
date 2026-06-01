package routes

import (
	"database/sql"
	"net/http"
	"server/internal/auth"
	"server/internal/handlers"
	"server/internal/middleware"
	"time"

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

	mux.Handle("GET /v1/authors", authMW(handlers.GetAuthors(conn)))
	mux.Handle("GET /v1/authors/{slug}", authMW(handlers.GetAuthor(conn)))
	mux.Handle("GET /v1/authors/{slug}/articles", authMW(handlers.GetAuthorArticles(conn)))

	mux.Handle("GET /v1/articles", authMW(handlers.GetArticles(conn)))
	mux.Handle("GET /v1/articles/{slug}", authMW(handlers.GetArticle(conn)))
	mux.Handle("GET /v1/search", authMW(handlers.GetSearch(conn)))
	mux.Handle("GET /v1/sections/{section_slug}/articles", handlers.GetSectionArticles(conn))
	mux.Handle("GET /v1/subsections/{subsection_slug}/articles", handlers.GetSubsectionArticles(conn))

	mux.Handle("GET /v1/media", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/media/{id}", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/media/gallery", http.HandlerFunc(handlers.Users))
	mux.Handle("GET /v1/homepage", handlers.GetHomepage(conn))
	mux.Handle("GET /v1/settings/site", handlers.GetSiteSettings(conn))
	mux.Handle("GET /v1/users/me", authMW(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/poll", handlers.GetPoll(conn))
	mux.Handle("GET /v1/poll/title", handlers.GetPollTitle(conn))
	mux.Handle("POST /v1/poll", middleware.RateLimitByIP(5, time.Minute)(handlers.PostPoll(conn)))
	mux.Handle("GET /v1/poll/options", handlers.GetPollOptions(conn))
	mux.Handle("GET /v1/taxonomy", authMW(handlers.GetTaxonomy(conn)))
	mux.Handle("PATCH /v1/poll/title", authMW(adminOnly(handlers.PatchPollTitle(conn))))
	mux.Handle("POST /v1/poll/options", authMW(adminOnly(handlers.PostPollOption(conn))))
	mux.Handle("PATCH /v1/poll/options", authMW(adminOnly(handlers.PatchPollOption(conn))))
	mux.Handle("DELETE /v1/poll/options", authMW(adminOnly(handlers.DeletePollOption(conn))))
	mux.Handle("PATCH /v1/settings/site", authMW(adminOnly(handlers.PatchSiteSettings(conn))))

	if verifier != nil {
		mux.Handle("POST /v1/authors", authMW(adminOnly(handlers.PostAuthors(conn))))
		mux.Handle("PUT /v1/authors/{slug}", authMW(adminOnly(handlers.PutAuthor(conn))))
		mux.Handle("PATCH /v1/authors/{slug}", authMW(adminOnly(handlers.PatchAuthor(conn))))
		mux.Handle("DELETE /v1/authors/{slug}", authMW(adminOnly(handlers.DeleteAuthor(conn))))

			mux.Handle("POST /v1/articles", authMW(handlers.PostArticles(conn)))
			mux.Handle("PUT /v1/articles/{slug}", authMW(handlers.PutArticle(conn)))
				mux.Handle("PATCH /v1/articles/{slug}", authMW(handlers.PatchArticle(conn)))
				mux.Handle("PATCH /v1/articles/{slug}/restore", authMW(handlers.RestoreArticle(conn)))
				mux.Handle("DELETE /v1/articles/{slug}", authMW(handlers.DeleteArticle(conn)))
		}
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

	// Taxonomy (categories, sections, tags)
	mux.HandleFunc("GET /v1/taxonomy", handlers.GetTaxonomy(conn))
	mux.HandleFunc("POST /v1/taxonomy", handlers.PostTaxonomy(conn))
	mux.HandleFunc("GET /v1/taxonomy/{type}/{slug}", handlers.GetTaxonomyItem(conn))
	mux.HandleFunc("PUT /v1/taxonomy/{type}/{slug}", handlers.PutTaxonomyItem(conn))
	mux.HandleFunc("DELETE /v1/taxonomy/{type}/{slug}", handlers.DeleteTaxonomyItem(conn))

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
	mux.Handle("GET /v1/settings/site", handlers.GetSiteSettings(conn))

	mux.Handle("GET /v1/users/me", auth(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", auth(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", auth(adminOnly(handlers.PatchUser(conn))))
	mux.Handle("PATCH /v1/settings/site", auth(adminOnly(handlers.PatchSiteSettings(conn))))

	mux.Handle("POST /v1/poll/options", auth(adminOnly(handlers.PostPollOption(conn))))
	mux.Handle("PATCH /v1/poll/title", auth(adminOnly(handlers.PatchPollTitle(conn))))
	mux.Handle("PATCH /v1/poll/options", auth(adminOnly(handlers.PatchPollOption(conn))))
	mux.Handle("DELETE /v1/poll/options", auth(adminOnly(handlers.DeletePollOption(conn))))
}
