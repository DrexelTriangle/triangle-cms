package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"server/internal/activity"
	"server/internal/cdnpurge"
	db "server/internal/database"
	"server/internal/models"
)

// After a write that changes what the public site shows, two caches stand
// between the editor and readers: this process's homepage response cache, and
// Cloudflare in front of Scalene. The first is dropped inline, because it is
// cheap and in-process. The second is queued on cdnPurger, which sends it a few
// seconds later and never makes the write wait or fail.
//
// cdnPurger is nil unless the Cloudflare purge settings are configured, and
// every purge helper returns before touching the database in that case, so an
// unconfigured install pays nothing for this.

var (
	cdnPurgerMu sync.RWMutex
	cdnPurger   *cdnpurge.Purger

	homepageCachesMu sync.Mutex
	homepageCaches   []*responseCache
)

// SetCDNPurger installs the purger that article and settings writes enqueue on.
// nil disables CDN purging.
func SetCDNPurger(p *cdnpurge.Purger) {
	cdnPurgerMu.Lock()
	cdnPurger = p
	cdnPurgerMu.Unlock()
}

func currentCDNPurger() *cdnpurge.Purger {
	cdnPurgerMu.RLock()
	defer cdnPurgerMu.RUnlock()
	return cdnPurger
}

func registerHomepageCache(c *responseCache) {
	homepageCachesMu.Lock()
	homepageCaches = append(homepageCaches, c)
	homepageCachesMu.Unlock()
}

// InvalidateHomepageCaches drops every homepage response cache in this
// process. Exported for the purger's BeforeFlush hook.
func InvalidateHomepageCaches() { invalidateHomepageCaches() }

func invalidateHomepageCaches() {
	homepageCachesMu.Lock()
	caches := append([]*responseCache(nil), homepageCaches...)
	homepageCachesMu.Unlock()
	for _, c := range caches {
		c.invalidate()
	}
}

// purgeHomepage is for writes that change only the homepage: breaking news,
// the carousel, developing stories, exhibit spotlights.
func purgeHomepage() {
	invalidateHomepageCaches()
	currentCDNPurger().Enqueue("/")
}

// purgeSiteChrome is for writes that change what every page wraps its content
// in: the footer menu and the site title. Scalene caches article pages at the
// edge for up to 30 days, so without this they would keep the old footer for a
// month; "/article/" goes as a prefix and evicts every article at once. Section,
// author and other pages expire within minutes and are left to do so.
//
// It costs a re-render of each article as it is next read, which is why it is
// kept to these two rare admin edits.
func purgeSiteChrome() {
	invalidateHomepageCaches()
	currentCDNPurger().Enqueue("/", "/article/")
}

// emergencyPurgeCooldown spaces site-wide purges. Each one sends every reader's
// next request for every page and image to the origin; a second click while the
// cache is still refilling adds load and fixes nothing the first didn't.
const emergencyPurgeCooldown = time.Minute

var (
	emergencyPurgeMu   sync.Mutex
	lastEmergencyPurge time.Time
)

// @Summary Purge the whole public site from Cloudflare
// @Description Emergency lever: evicts every cached page, image and asset on the public site's host, for when something stale or wrong is public and an article save does not clear it. The origin then serves each first request again while the cache refills, so it is limited to once a minute.
// @Tags settings
// @Produce json
// @Success 200 {object} models.CachePurgeResponse
// @Failure 429 {object} models.ErrorResponse
// @Failure 502 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/settings/cache/purge [post]
func PostEmergencyCachePurge() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		purger := currentCDNPurger()
		if !purger.Enabled() {
			writeError(w, http.StatusServiceUnavailable, "Cloudflare purging is not configured on this server")
			return
		}

		emergencyPurgeMu.Lock()
		defer emergencyPurgeMu.Unlock()
		if wait := emergencyPurgeCooldown - time.Since(lastEmergencyPurge); wait > 0 {
			writeError(w, http.StatusTooManyRequests,
				fmt.Sprintf("the site was purged moments ago; try again in %ds", int(wait.Seconds())+1))
			return
		}

		// Own deadline: the purge should finish even if the admin's tab closes.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := purger.PurgeSite(ctx); err != nil {
			slog.Error("emergency cache purge failed", "error", err)
			writeError(w, http.StatusBadGateway, "Cloudflare did not accept the purge; nothing was evicted")
			return
		}
		now := time.Now().UTC()
		lastEmergencyPurge = now
		// After the purge, so the homepage reload it triggers reads current data.
		invalidateHomepageCaches()

		activity.LogRequest(r, "cache_purged", "Whole site purged from Cloudflare", "host", purger.SiteHost())
		writeJSON(w, http.StatusOK, models.CachePurgeResponse{Host: purger.SiteHost(), PurgedAt: now.Format(time.RFC3339)})
	})
}

// articleSurface is every public URL whose content depends on one article.
type articleSurface struct {
	live  bool
	paths []string
}

// loadArticleSurface reads the public URLs an article currently appears on. An
// article that is a draft, scheduled or archived has live=false; its paths are
// still filled in, because an unpublish has to purge the pages it was on.
//
// A missing row is not an error: it is a surface with nothing on it.
func loadArticleSurface(ctx context.Context, conn *sql.DB, articleID int64) (articleSurface, error) {
	var surface articleSurface
	if conn == nil || articleID <= 0 {
		return surface, nil
	}
	var slug string
	var photoURL sql.NullString
	var live bool
	err := conn.QueryRowContext(ctx,
		"SELECT `slug`, `photo_url`, (`pub_date` IS NOT NULL AND `archived_at` IS NULL) FROM `articles` WHERE `id` = ?",
		articleID,
	).Scan(&slug, &photoURL, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return surface, nil
	}
	if err != nil {
		return surface, err
	}
	surface.live = live

	if s := strings.TrimSpace(slug); s != "" {
		surface.paths = append(surface.paths, "/article/"+s)
	}
	// Only a URL on the site's own host survives Enqueue, which is the case
	// this is for: a library image that 404ed before it was synced.
	if photoURL.Valid {
		surface.paths = append(surface.paths, photoURL.String)
	}

	var categories []string
	rows, err := conn.QueryContext(ctx, "SELECT `category` FROM `article_categories` WHERE `article_id` = ?", articleID)
	if err != nil {
		return surface, err
	}
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			rows.Close()
			return surface, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return surface, err
	}
	rows.Close()

	sections, err := db.SectionPagesForCategories(ctx, conn, categories)
	if err != nil {
		return surface, err
	}
	for _, s := range sections {
		surface.paths = append(surface.paths, "/"+s)
	}

	authors, err := db.LoadAuthorsByArticleIDs(ctx, conn, []int64{articleID})
	if err != nil {
		return surface, err
	}
	for _, a := range authors[articleID] {
		if s := strings.TrimSpace(a.Slug); s != "" {
			surface.paths = append(surface.paths, "/author/"+s)
		}
	}
	return surface, nil
}

// articlePurge captures an article's public surface before a write so that
// done can purge both what it was on and what it is on now: a renamed slug, a
// moved section or a dropped byline leaves a stale page behind otherwise.
type articlePurge struct {
	conn   *sql.DB
	id     int64
	before articleSurface
	active bool
}

// beginArticlePurge snapshots the article's surface. It is a no-op returning
// an inert value when purging is disabled. A failed snapshot is logged and the
// purge goes ahead on the after-state alone; it is best effort by design.
func beginArticlePurge(ctx context.Context, conn *sql.DB, articleID int64) *articlePurge {
	p := &articlePurge{conn: conn, id: articleID, active: currentCDNPurger().Enabled()}
	if !p.active || articleID <= 0 {
		return p
	}
	before, err := loadArticleSurface(ctx, conn, articleID)
	if err != nil {
		slog.Warn("cdn purge: reading article before write failed", "article_id", articleID, "error", err)
	}
	p.before = before
	return p
}

// done runs after a successful write. The homepage cache is dropped whatever
// the article's state, since a pin, a breaking flag or a publish all show
// there. The CDN purge runs only if the article was or is public: editing a
// draft changes nothing a reader can see.
//
// It reads with a fresh context: the write has committed, and an editor
// closing the tab must not cancel the purge of a retraction.
func (p *articlePurge) done() {
	invalidateHomepageCaches()
	if p == nil || !p.active || p.id <= 0 {
		return
	}
	purgeArticlesAfterWrite(p.conn, []int64{p.id}, p.before)
}

func purgeArticlesAfterWrite(conn *sql.DB, ids []int64, before articleSurface) {
	purger := currentCDNPurger()
	if !purger.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), homepageLoadTimeout)
	defer cancel()

	paths := append([]string(nil), before.paths...)
	anyLive := before.live
	for _, id := range ids {
		after, err := loadArticleSurface(ctx, conn, id)
		if err != nil {
			slog.Warn("cdn purge: reading article after write failed", "article_id", id, "error", err)
		}
		anyLive = anyLive || after.live
		paths = append(paths, after.paths...)
	}
	if !anyLive {
		return
	}
	purger.Enqueue(append(paths, "/")...)
}

// PurgePublishedArticles is the scheduler's hook: articles it took live have
// no before-state worth purging, only the pages they now appear on.
func PurgePublishedArticles(conn *sql.DB, ids []int64) {
	if len(ids) == 0 {
		return
	}
	invalidateHomepageCaches()
	purgeArticlesAfterWrite(conn, ids, articleSurface{})
}
