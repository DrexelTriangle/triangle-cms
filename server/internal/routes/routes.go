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
	mux.Handle("GET /v1/activity", authMW(adminOnly(handlers.GetActivity())))
	mux.Handle("GET /v1/users/me", authMW(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", authMW(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", authMW(adminOnly(handlers.PatchUser(conn))))
	mux.Handle("GET /v1/poll", handlers.GetPoll(conn))
	mux.Handle("GET /v1/poll/title", handlers.GetPollTitle(conn))
	mux.Handle("POST /v1/poll", middleware.RateLimitByIP(5, time.Minute)(handlers.PostPoll(conn)))
	mux.Handle("GET /v1/poll/options", handlers.GetPollOptions(conn))
	mux.Handle("GET /v1/developing-stories", handlers.GetDevelopingStories(conn))
	mux.Handle("GET /v1/taxonomy", authMW(handlers.GetTaxonomy(conn)))
	mux.Handle("GET /v1/taxonomy/{type}/{slug}", authMW(handlers.GetTaxonomyItem(conn)))
	mux.Handle("PATCH /v1/poll/title", authMW(adminOnly(handlers.PatchPollTitle(conn))))
	mux.Handle("POST /v1/poll/options", authMW(adminOnly(handlers.PostPollOption(conn))))
	mux.Handle("PATCH /v1/poll/options", authMW(adminOnly(handlers.PatchPollOption(conn))))
	mux.Handle("DELETE /v1/poll/options", authMW(adminOnly(handlers.DeletePollOption(conn))))
	mux.Handle("POST /v1/developing-stories", authMW(adminOnly(handlers.PostDevelopingStory(conn))))
	mux.Handle("DELETE /v1/developing-stories", authMW(adminOnly(handlers.DeleteDevelopingStory(conn))))
	mux.Handle("PATCH /v1/settings/site", authMW(adminOnly(handlers.PatchSiteSettings(conn))))
	mux.Handle("POST /v1/settings/taxonomy/rebuild", authMW(adminOnly(handlers.PostRebuildTaxonomyCounts(conn))))
	mux.Handle("POST /v1/taxonomy", authMW(adminOnly(handlers.PostTaxonomy(conn))))
	mux.Handle("PUT /v1/taxonomy/{type}/{slug}", authMW(adminOnly(handlers.PutTaxonomyItem(conn))))
	mux.Handle("DELETE /v1/taxonomy/{type}/{slug}", authMW(adminOnly(handlers.DeleteTaxonomyItem(conn))))

	if verifier != nil {
		mux.Handle("POST /v1/authors", authMW(adminOnly(handlers.PostAuthors(conn))))
		mux.Handle("PUT /v1/authors/{slug}", authMW(adminOnly(handlers.PutAuthor(conn))))
		mux.Handle("PATCH /v1/authors/{slug}", authMW(adminOnly(handlers.PatchAuthor(conn))))
		mux.Handle("PATCH /v1/authors/{slug}/restore", authMW(adminOnly(handlers.RestoreAuthor(conn))))
		mux.Handle("DELETE /v1/authors/{slug}", authMW(adminOnly(handlers.DeleteAuthor(conn))))

		mux.Handle("POST /v1/articles", authMW(handlers.PostArticles(conn)))
		mux.Handle("PUT /v1/articles/{slug}", authMW(handlers.PutArticle(conn)))
		mux.Handle("PATCH /v1/articles/{slug}", authMW(handlers.PatchArticle(conn)))
		mux.Handle("PATCH /v1/articles/{slug}/restore", authMW(handlers.RestoreArticle(conn)))
		mux.Handle("DELETE /v1/articles/{slug}", authMW(handlers.DeleteArticle(conn)))
	}
}
