package routes

import (
	"database/sql"
	"net/http"
	"server/internal/akismet"
	"server/internal/auth"
	"server/internal/handlers"
	"server/internal/middleware"
	"server/internal/slack"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger"
)

func Register(mux *http.ServeMux, conn *sql.DB, verifier *oidc.IDTokenVerifier, oidcCfg auth.OIDCConfig, spamChecker akismet.Checker, slackNotifier slack.Notifier, queryEmbedder handlers.QueryEmbedder) {
	mux.Handle("/swagger/", httpSwagger.WrapHandler)
	// Prometheus scrape target. Unauthenticated by design, and safe only
	// because Nginx proxies just /v1 and /swagger: nothing routes /metrics in
	// from outside, and Prometheus reaches it over the host loopback port the
	// slot publishes. If a public vhost ever forwards /metrics, put it behind
	// auth first -- it exposes route names, traffic volumes, and error rates.
	mux.Handle("GET /metrics", promhttp.Handler())

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
	// Sections are editorial configuration, not account administration, so
	// editors manage them. Deletion is still limited to items nothing uses --
	// that guard lives in the handler and applies to admins too.
	editorOrAdmin := middleware.RequireEditor

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
	// "random" is a literal segment, so Go's mux prefers it over
	// /v1/articles/{slug}.
	mux.Handle("GET /v1/articles/random", handlers.GetRandomArticle(conn))
	mux.Handle("GET /v1/articles/{slug}", optionalAuth(handlers.GetArticle(conn)))
	mux.Handle("GET /v1/sitemap/slugs", handlers.GetSitemapSlugs(conn))

	// Classifieds. Submission is public and rate limited like comments;
	// everything a reader submits lands as pending. Moderation happens either
	// in the CMS queue (authenticated) or from the buttons on the Slack
	// notification, which authenticates by Slack request signature instead of a
	// session — hence no authMW on that route. "manage" is a literal segment,
	// so Go's mux prefers it over /v1/classifieds/{id}.
	mux.Handle("GET /v1/classifieds", handlers.GetClassifieds(conn))
	mux.Handle("POST /v1/classifieds", middleware.RateLimitByIP(5, time.Minute)(handlers.PostClassified(conn, slackNotifier)))
	mux.Handle("GET /v1/classifieds/manage", authMW(handlers.GetClassifiedsManage(conn, slackNotifier)))
	mux.Handle("PATCH /v1/classifieds/{id}", authMW(handlers.PatchClassified(conn)))
	mux.Handle("DELETE /v1/classifieds/{id}", authMW(adminOnly(handlers.DeleteClassified(conn))))
	mux.Handle("POST /v1/integrations/slack/classifieds", middleware.RateLimitByIP(30, time.Minute)(handlers.PostSlackClassifiedAction(conn)))
	// The public photo gallery. The editor-facing catalogue at
	// /v1/media/gallery stays authenticated; this one is images-only.
	mux.Handle("GET /v1/gallery", handlers.GetPublicGallery(conn))
	mux.Handle("GET /v1/articles/{slug}/comments", handlers.GetArticleComments(conn))
	mux.Handle("POST /v1/articles/{slug}/comments", middleware.RateLimitByIP(5, time.Minute)(handlers.PostArticleComment(conn, spamChecker)))
	mux.Handle("GET /v1/search", handlers.GetSearch(conn, queryEmbedder))
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
	// Adding an image is part of writing an article, so uploading (and the paste
	// sideload that wraps it) is open to editors. Destructive and bulk
	// operations -- delete, reindex -- stay admin-only.
	mux.Handle("POST /v1/media", authMW(handlers.PostMedia(conn)))
	mux.Handle("POST /v1/media/fetch", authMW(handlers.PostMediaFetch(conn)))
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
	mux.Handle("GET /v1/settings/homepage-carousel", handlers.GetHomepageCarousel(conn))
	mux.Handle("GET /v1/settings/footer", handlers.GetFooterSettings(conn))
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
	// listing that includes them is a separate authenticated path.
	mux.Handle("GET /v1/polls", handlers.GetPolls(conn))
	mux.Handle("GET /v1/polls/{id}", handlers.GetPollByID(conn))
	mux.Handle("GET /v1/developing-stories", handlers.GetDevelopingStories(conn))
	mux.Handle("GET /v1/taxonomy", handlers.GetTaxonomy(conn))
	mux.Handle("GET /v1/taxonomy/{type}/{slug}", handlers.GetTaxonomyItem(conn))
	// Running the poll is part of editorial work, not site administration, so
	// authoring and moderating questions is open to any signed-in editor.
	mux.Handle("PATCH /v1/poll/title", authMW(handlers.PatchPollTitle(conn)))
	mux.Handle("POST /v1/poll/options", authMW(handlers.PostPollOption(conn)))
	mux.Handle("PATCH /v1/poll/options", authMW(handlers.PatchPollOption(conn)))
	mux.Handle("DELETE /v1/poll/options", authMW(handlers.DeletePollOption(conn)))
	// "manage" is a literal segment, so Go's mux prefers it over /v1/polls/{id}.
	mux.Handle("GET /v1/polls/manage", authMW(handlers.GetPollsManage(conn)))
	mux.Handle("POST /v1/polls", authMW(handlers.PostPollRecord(conn)))
	mux.Handle("PATCH /v1/polls/{id}", authMW(handlers.PatchPollRecord(conn)))
	mux.Handle("DELETE /v1/polls/{id}", authMW(handlers.DeletePollRecord(conn)))
	mux.Handle("POST /v1/polls/{id}/options", authMW(handlers.PostPollRecordOption(conn)))
	mux.Handle("PATCH /v1/polls/{id}/options/{option_id}", authMW(handlers.PatchPollRecordOption(conn)))
	mux.Handle("DELETE /v1/polls/{id}/options/{option_id}", authMW(handlers.DeletePollRecordOption(conn)))
	mux.Handle("POST /v1/developing-stories", authMW(adminOnly(handlers.PostDevelopingStory(conn))))
	mux.Handle("DELETE /v1/developing-stories", authMW(adminOnly(handlers.DeleteDevelopingStory(conn))))
	mux.Handle("PATCH /v1/settings/site", authMW(adminOnly(handlers.PatchSiteSettings(conn))))
	mux.Handle("PATCH /v1/settings/seo", authMW(adminOnly(handlers.PatchSEOSettings(conn))))
	mux.Handle("PATCH /v1/settings/breaking-news", authMW(adminOnly(handlers.PatchBreakingNews(conn))))
	mux.Handle("PATCH /v1/settings/homepage-carousel", authMW(adminOnly(handlers.PatchHomepageCarousel(conn))))
	mux.Handle("PATCH /v1/settings/footer", authMW(adminOnly(handlers.PatchFooterSettings(conn))))
	mux.Handle("POST /v1/settings/taxonomy/rebuild", authMW(adminOnly(handlers.PostRebuildTaxonomyCounts(conn))))
	mux.Handle("POST /v1/taxonomy", authMW(editorOrAdmin(handlers.PostTaxonomy(conn))))
	mux.Handle("PUT /v1/taxonomy/{type}/{slug}", authMW(editorOrAdmin(handlers.PutTaxonomyItem(conn))))
	mux.Handle("DELETE /v1/taxonomy/{type}/{slug}", authMW(editorOrAdmin(handlers.DeleteTaxonomyItem(conn))))

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
