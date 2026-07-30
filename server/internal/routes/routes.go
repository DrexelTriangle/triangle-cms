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

	// Public, but identity-aware: these endpoints serve the public site and must
	// answer anonymous callers, while an authenticated editor gets the wider
	// view (drafts, archived rows) that the CMS UI needs. OptionalAuth resolves
	// a session when one is present and lets the request through when it is not;
	// the handlers branch on middleware.UserFromContext.
	optionalAuth := func(h http.Handler) http.Handler { return h }
	if verifier != nil {
		optionalAuth = middleware.OptionalAuth(verifier, conn, oidcCfg)
	}

	mux.Handle("GET /v1/authors", optionalAuth(handlers.GetAuthors(conn)))
	mux.Handle("GET /v1/authors/{slug}", handlers.GetAuthor(conn))
	mux.Handle("GET /v1/authors/{slug}/articles", optionalAuth(handlers.GetAuthorArticles(conn)))

	mux.Handle("GET /v1/articles", optionalAuth(handlers.GetArticles(conn)))
	mux.Handle("GET /v1/articles/{slug}", handlers.GetArticle(conn))
	mux.Handle("GET /v1/articles/{slug}/comments", handlers.GetArticleComments(conn))
	mux.Handle("POST /v1/articles/{slug}/comments", middleware.RateLimitByIP(5, time.Minute)(handlers.PostArticleComment(conn)))
	mux.Handle("GET /v1/search", handlers.GetSearch(conn))
	mux.Handle("GET /v1/sections/{section_slug}/articles", optionalAuth(handlers.GetSectionArticles(conn)))
	mux.Handle("GET /v1/subsections/{subsection_slug}/articles", optionalAuth(handlers.GetSubsectionArticles(conn)))
	mux.Handle("GET /v1/comments", authMW(handlers.GetComments(conn)))
	mux.Handle("PATCH /v1/comments/{id}", authMW(handlers.PatchComment(conn)))
	mux.Handle("DELETE /v1/comments/{id}", authMW(handlers.DeleteComment(conn)))

	// The media library is editor-facing: the assets themselves are served
	// publicly by Nginx off the CephFS mount, but the catalogue is not.
	// "gallery" and "index" are literal segments, so Go's mux prefers them over
	// /v1/media/{id}.
	mux.Handle("GET /v1/media", authMW(handlers.GetMedia(conn)))
	mux.Handle("GET /v1/media/gallery", authMW(handlers.GetMediaGallery(conn)))
	mux.Handle("GET /v1/media/{id}", authMW(handlers.GetMediaItem(conn)))
	mux.Handle("POST /v1/media", authMW(adminOnly(handlers.PostMedia(conn))))
	// Indexing runs in the background (the walk outlives any proxy timeout), so
	// starting it and polling its progress are separate endpoints.
	mux.Handle("POST /v1/media/index", authMW(adminOnly(handlers.PostMediaIndex(conn))))
	mux.Handle("GET /v1/media/index", authMW(adminOnly(handlers.GetMediaIndexStatus())))
	mux.Handle("PATCH /v1/media/{id}", authMW(handlers.PatchMediaItem(conn)))
	mux.Handle("DELETE /v1/media/{id}", authMW(adminOnly(handlers.DeleteMediaItem(conn))))
	mux.Handle("GET /v1/homepage", handlers.GetHomepage(conn))
	mux.Handle("GET /v1/settings/site", handlers.GetSiteSettings(conn))
	mux.Handle("GET /v1/settings/seo", handlers.GetSEOSettings(conn))
	mux.Handle("GET /v1/settings/breaking-news", handlers.GetBreakingNews(conn))
	mux.Handle("GET /v1/seo/audit", authMW(handlers.GetSEOAudit(conn)))
	mux.Handle("GET /v1/activity", authMW(adminOnly(handlers.GetActivity())))
	mux.Handle("GET /v1/users/me", authMW(http.HandlerFunc(handlers.GetMe)))
	mux.Handle("GET /v1/users", authMW(adminOnly(handlers.GetUsers(conn))))
	mux.Handle("PATCH /v1/users/{id}", authMW(adminOnly(handlers.PatchUser(conn))))
	mux.Handle("GET /v1/poll", handlers.GetPoll(conn))
	mux.Handle("GET /v1/poll/title", handlers.GetPollTitle(conn))
	mux.Handle("POST /v1/poll", middleware.RateLimitByIP(5, time.Minute)(handlers.PostPoll(conn)))
	mux.Handle("GET /v1/poll/options", handlers.GetPollOptions(conn))

	// Poll archive. GET /v1/polls is public and hides drafts; the editor-facing
	// listing that includes them is a separate admin-gated path.
	mux.Handle("GET /v1/polls", handlers.GetPolls(conn))
	mux.Handle("GET /v1/polls/{id}", handlers.GetPollByID(conn))
	mux.Handle("GET /v1/developing-stories", handlers.GetDevelopingStories(conn))
	mux.Handle("GET /v1/taxonomy", handlers.GetTaxonomy(conn))
	mux.Handle("GET /v1/taxonomy/{type}/{slug}", handlers.GetTaxonomyItem(conn))
	mux.Handle("PATCH /v1/poll/title", authMW(adminOnly(handlers.PatchPollTitle(conn))))
	mux.Handle("POST /v1/poll/options", authMW(adminOnly(handlers.PostPollOption(conn))))
	mux.Handle("PATCH /v1/poll/options", authMW(adminOnly(handlers.PatchPollOption(conn))))
	mux.Handle("DELETE /v1/poll/options", authMW(adminOnly(handlers.DeletePollOption(conn))))
	// "manage" is a literal segment, so Go's mux prefers it over /v1/polls/{id}.
	mux.Handle("GET /v1/polls/manage", authMW(adminOnly(handlers.GetPollsManage(conn))))
	mux.Handle("POST /v1/polls", authMW(adminOnly(handlers.PostPollRecord(conn))))
	mux.Handle("PATCH /v1/polls/{id}", authMW(adminOnly(handlers.PatchPollRecord(conn))))
	mux.Handle("DELETE /v1/polls/{id}", authMW(adminOnly(handlers.DeletePollRecord(conn))))
	mux.Handle("POST /v1/polls/{id}/options", authMW(adminOnly(handlers.PostPollRecordOption(conn))))
	mux.Handle("PATCH /v1/polls/{id}/options/{option_id}", authMW(adminOnly(handlers.PatchPollRecordOption(conn))))
	mux.Handle("DELETE /v1/polls/{id}/options/{option_id}", authMW(adminOnly(handlers.DeletePollRecordOption(conn))))
	mux.Handle("POST /v1/developing-stories", authMW(adminOnly(handlers.PostDevelopingStory(conn))))
	mux.Handle("DELETE /v1/developing-stories", authMW(adminOnly(handlers.DeleteDevelopingStory(conn))))
	mux.Handle("PATCH /v1/settings/site", authMW(adminOnly(handlers.PatchSiteSettings(conn))))
	mux.Handle("PATCH /v1/settings/seo", authMW(adminOnly(handlers.PatchSEOSettings(conn))))
	mux.Handle("PATCH /v1/settings/breaking-news", authMW(adminOnly(handlers.PatchBreakingNews(conn))))
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
		mux.Handle("PUT /v1/articles/{slug}/edit-lock", authMW(handlers.AcquireArticleEditLock(conn)))
		mux.Handle("DELETE /v1/articles/{slug}/edit-lock", authMW(handlers.ReleaseArticleEditLock(conn)))
	}
}
