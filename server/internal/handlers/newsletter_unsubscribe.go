package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/mailer"
	"server/internal/models"
)

// The unsubscribe link in every issue lands here, on the CMS rather than the
// public site, so it works whatever the site is doing. A GET only ever shows
// a page: mail scanners and link previewers open every link in an email, and
// an unsubscribe on GET would quietly drop people who never clicked. The
// change is a POST, either from the page's button or from an inbox's RFC 8058
// one-click unsubscribe, which POSTs "List-Unsubscribe=One-Click" to the URL
// in the List-Unsubscribe header.
//
// No rate limit: one-click POSTs arrive in bursts from a handful of Gmail and
// Yahoo addresses after every send, and a 128-bit token can't be guessed.

type unsubscribePage struct {
	Title   string
	Message string
	Email   string
	// Action and ActionLabel, when set, render a single-button form.
	Action      string
	ActionLabel string
	SiteURL     string
}

var unsubscribeTemplate = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} · To The Point</title>
<style>
body{margin:0;background:#f3f4f6;font-family:Helvetica,Arial,sans-serif;color:#1f2328}
main{max-width:520px;margin:32px auto;background:#fff;border-radius:6px;overflow:hidden}
header{background:#055b9d;color:#fff;text-align:center;padding:20px 16px;font-family:Georgia,'Times New Roman',serif}
header small{display:block;font-size:13px;letter-spacing:2px}
header span{font-size:26px}
section{padding:24px}
h1{font-size:22px;font-weight:normal;margin:0 0 12px}
p{font-size:15px;line-height:1.55;margin:0 0 16px}
button{background:#055b9d;color:#fff;border:0;border-radius:4px;padding:10px 18px;font-size:15px;font-weight:bold;cursor:pointer}
button:focus-visible{outline:3px solid #2f8fd3;outline-offset:2px}
a{color:#055b9d}
@media (max-width:560px){main{margin:0;border-radius:0}}
</style>
</head>
<body>
<main>
<header><small>THE TRIANGLE</small><span>To The Point</span></header>
<section>
<h1>{{.Title}}</h1>
<p>{{.Message}}{{if .Email}} <strong>{{.Email}}</strong>{{end}}</p>
{{if .Action}}<form method="post" action="{{.Action}}"><p><button type="submit">{{.ActionLabel}}</button></p></form>{{end}}
<p><a href="{{.SiteURL}}">Go to The Triangle</a></p>
</section>
</main>
</body>
</html>
`))

func writeUnsubscribePage(w http.ResponseWriter, status int, page unsubscribePage) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The URL holds the subscriber's key: keep it out of caches and out of
	// the Referer the "Go to The Triangle" link would send.
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if err := unsubscribeTemplate.Execute(w, page); err != nil {
		slog.Error("render newsletter unsubscribe page", "error", err)
	}
}

// maskEmail shows enough of the address to recognise it without handing the
// whole thing to anyone the email was forwarded to.
func maskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "•••"
	}
	return local[:1] + "•••@" + domain
}

// subscriberLinkQuery rebuilds the key part of the link, for the form to post
// back to.
func subscriberLinkQuery(r *http.Request) string {
	q := url.Values{}
	if t := r.URL.Query().Get("t"); t != "" {
		q.Set("t", t)
	} else {
		q.Set("nk", r.URL.Query().Get("nk"))
	}
	return q.Encode()
}

type unsubscribeFlow struct {
	siteURL string
}

func (f unsubscribeFlow) page(title, message string) unsubscribePage {
	return unsubscribePage{Title: title, Message: message, SiteURL: f.siteURL}
}

// key reads the subscriber key from the URL, answering the request itself
// (and returning ok=false) for a test send's key or a malformed one.
func (f unsubscribeFlow) key(w http.ResponseWriter, r *http.Request) (db.SubscriberKey, bool) {
	q := r.URL.Query()
	if q.Get("t") == mailer.TestToken {
		writeUnsubscribePage(w, http.StatusOK, f.page("This was a test email",
			"The link worked, but test emails aren't tied to a subscription, so nothing changed."))
		return db.SubscriberKey{}, false
	}
	key, ok := db.ParseSubscriberKey(q.Get("t"), q.Get("nk"))
	if !ok {
		f.notFound(w)
		return db.SubscriberKey{}, false
	}
	return key, true
}

func (f unsubscribeFlow) notFound(w http.ResponseWriter) {
	writeUnsubscribePage(w, http.StatusNotFound, f.page("This link doesn't work",
		"It's broken or out of date. If you copied it from an email, check that the whole link came across."))
}

func (f unsubscribeFlow) failed(w http.ResponseWriter, err error) {
	slog.Error("newsletter unsubscribe failed", "error", err)
	writeUnsubscribePage(w, http.StatusInternalServerError, f.page("Something went wrong",
		"Nothing changed. Please try the link again in a minute."))
}

// show renders the page for a subscriber's current state.
func (f unsubscribeFlow) show(w http.ResponseWriter, r *http.Request, s *db.SubscriberState, justChanged bool) {
	query := subscriberLinkQuery(r)
	page := f.page("", "")
	page.Email = maskEmail(s.Email)
	switch s.Status {
	case models.SubscriberStatusConfirmed:
		if justChanged {
			page.Title = "You're subscribed again"
			page.Message = "Welcome back. To The Point will go to"
		} else {
			page.Title = "Unsubscribe from To The Point?"
			page.Message = "You'll stop getting the newsletter at"
		}
		page.Action = "/v1/newsletter/unsubscribe?" + query
		page.ActionLabel = "Unsubscribe"
		if justChanged {
			page.Action = ""
		}
	case models.SubscriberStatusUnsubscribed:
		page.Title = "You're unsubscribed"
		page.Message = "To The Point won't be sent to"
		page.Action = "/v1/newsletter/resubscribe?" + query
		page.ActionLabel = "Subscribe again"
	default:
		page.Title = "This address isn't receiving To The Point"
		page.Message = "Email to it bounced, so we stopped sending to"
	}
	writeUnsubscribePage(w, http.StatusOK, page)
}

// GetNewsletterUnsubscribe shows the unsubscribe page for the link's subscriber.
// It never changes anything.
//
// @Summary Newsletter unsubscribe page
// @Tags newsletters
// @Produce html
// @Param t query string false "Subscriber token"
// @Param nk query string false "WordPress key, <id>-<token>"
// @Success 200 {string} string "HTML page"
// @Failure 404 {string} string "HTML page"
// @Router /v1/newsletter/unsubscribe [get]
func GetNewsletterUnsubscribe(conn *sql.DB, siteURL string) http.Handler {
	f := unsubscribeFlow{siteURL: siteURL}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := f.key(w, r)
		if !ok {
			return
		}
		s, err := db.FindSubscriberByKey(r.Context(), conn, key)
		switch {
		case db.IsNoSubscriber(err):
			f.notFound(w)
		case err != nil:
			f.failed(w, err)
		default:
			f.show(w, r, s, false)
		}
	})
}

// PostNewsletterUnsubscribe unsubscribes the link's subscriber, from the
// page's button or an inbox's one-click unsubscribe. Repeating it is harmless.
//
// @Summary Unsubscribe from the newsletter
// @Tags newsletters
// @Accept x-www-form-urlencoded
// @Produce html
// @Param t query string false "Subscriber token"
// @Param nk query string false "WordPress key, <id>-<token>"
// @Success 200 {string} string "HTML page"
// @Failure 404 {string} string "HTML page"
// @Router /v1/newsletter/unsubscribe [post]
func PostNewsletterUnsubscribe(conn *sql.DB, siteURL string) http.Handler {
	return changeSubscriptionHandler(conn, siteURL, db.UnsubscribeByKey, "newsletter_unsubscribed")
}

// PostNewsletterResubscribe undoes an unsubscribe from the same link.
//
// @Summary Resubscribe to the newsletter
// @Tags newsletters
// @Accept x-www-form-urlencoded
// @Produce html
// @Param t query string false "Subscriber token"
// @Param nk query string false "WordPress key, <id>-<token>"
// @Success 200 {string} string "HTML page"
// @Failure 404 {string} string "HTML page"
// @Router /v1/newsletter/resubscribe [post]
func PostNewsletterResubscribe(conn *sql.DB, siteURL string) http.Handler {
	return changeSubscriptionHandler(conn, siteURL, db.ResubscribeByKey, "newsletter_resubscribed")
}

func changeSubscriptionHandler(
	conn *sql.DB,
	siteURL string,
	change func(ctx context.Context, conn *sql.DB, key db.SubscriberKey, now time.Time) (*db.SubscriberState, bool, error),
	action string,
) http.Handler {
	f := unsubscribeFlow{siteURL: siteURL}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := f.key(w, r)
		if !ok {
			return
		}
		// The body is the one-click marker at most; nothing in it is needed.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		oneClick := r.PostFormValue("List-Unsubscribe") == "One-Click"

		after, changed, err := change(r.Context(), conn, key, time.Now())
		if db.IsNoSubscriber(err) {
			f.notFound(w)
			return
		}
		if err != nil {
			f.failed(w, err)
			return
		}
		if changed {
			activity.LogRequest(r, action, "Subscriber "+strconv.FormatInt(after.ID, 10), "one_click", oneClick)
		}
		f.show(w, r, after, changed)
	})
}
