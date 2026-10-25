package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// The "View online" link in every issue, and the public site's newsletter
// preview: Scalene's /subscribe page frames the latest issue, which it used to
// do with a static tothepoint.html exported from WordPress.
//
// This copy is everyone's, so it carries nobody's unsubscribe key: its
// unsubscribe links go to the keyless unsubscribe page, which explains where
// the real link is. It is not tracked. Only sent issues are shown; a draft is
// not public until it goes out.

// frameAncestors allows the CMS itself and the public site to frame the page.
func frameAncestors(siteURL string) string {
	if u, err := url.Parse(siteURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return "'self' " + u.Scheme + "://" + u.Host
	}
	return "'self'"
}

// publicIssueHTML fills an issue's placeholders for the public copy. Links
// are relative to the page (.../issues/{id}), so they resolve whether it is
// opened on the CMS (/v1/newsletter/), through the public site's proxy
// (www.thetriangle.org/newsletter/), or framed by either.
func publicIssueHTML(n models.Newsletter) string {
	html := strings.NewReplacer(
		"{{unsubscribe_url}}", "../unsubscribe",
		"{{manage_subscription_url}}", "../unsubscribe",
		"{{view_online_url}}", strconv.FormatInt(n.ID, 10),
	).Replace(n.EmailHTML)
	// Links leave the frame: a reader clicking a story on the public site's
	// preview should get the story, not the story squeezed into the iframe.
	const base = `<base target="_blank">`
	if i := strings.Index(strings.ToLower(html), "<head>"); i >= 0 {
		return html[:i+len("<head>")] + base + html[i+len("<head>"):]
	}
	return base + html
}

// GetNewsletterIssue shows a sent issue as a web page. {id} is a newsletter
// id, or "latest" for the most recently sent one.
//
// @Summary View a sent newsletter issue
// @Tags newsletters
// @Produce html
// @Param id path string true "Newsletter ID, or latest"
// @Success 200 {string} string "The issue's HTML"
// @Success 302 {string} string "latest with nothing sent yet: redirect to the public site"
// @Failure 404 {string} string "HTML page"
// @Router /v1/newsletter/issues/{id} [get]
func GetNewsletterIssue(conn *sql.DB, siteURL string) http.Handler {
	f := unsubscribeFlow{siteURL: siteURL}
	ancestors := frameAncestors(siteURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			issue models.Newsletter
			err   error
		)
		if r.PathValue("id") == "latest" {
			issue, err = db.LatestSentNewsletter(r.Context(), conn)
			// Nothing sent yet: old WordPress issues' "view online" links land
			// here (Delta's nginx), so send the reader to the site, not a dead end.
			if errors.Is(err, sql.ErrNoRows) {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, siteURL, http.StatusFound)
				return
			}
		} else {
			id, parseErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if parseErr != nil || id <= 0 {
				f.issueNotFound(w)
				return
			}
			issue, err = db.GetNewsletter(r.Context(), conn, id)
		}
		if errors.Is(err, sql.ErrNoRows) || (err == nil && issue.Status != models.NewsletterStatusSent) {
			f.issueNotFound(w)
			return
		}
		if err != nil {
			slog.Error("load newsletter issue", "error", err)
			writeUnsubscribePage(w, http.StatusInternalServerError, f.page("Something went wrong", "Please try again in a minute."))
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// The issue is author HTML: let its inline styles and images through,
		// nothing that runs.
		h.Set("Content-Security-Policy", "default-src 'none'; img-src http: https: data:; style-src 'unsafe-inline'; base-uri 'self'; form-action 'none'; frame-ancestors "+ancestors)
		h.Set("X-Content-Type-Options", "nosniff")
		// A sent issue never changes; "latest" moves on with the next send.
		if r.PathValue("id") == "latest" {
			h.Set("Cache-Control", "public, max-age=300")
		} else {
			h.Set("Cache-Control", "public, max-age=86400")
		}
		_, _ = w.Write([]byte(publicIssueHTML(issue)))
	})
}

func (f unsubscribeFlow) issueNotFound(w http.ResponseWriter) {
	writeUnsubscribePage(w, http.StatusNotFound, f.page("This issue isn't available",
		"It may not have gone out yet, or the link may be incomplete."))
}
