package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	db "server/internal/database"
)

// Open and click tracking for sent issues. Both endpoints are public and keyed
// by a delivery's random token, one per copy sent.
//
// The numbers are approximate in the usual ways: Apple Mail Privacy Protection
// loads every pixel on delivery (opens read high), clients that block images
// never load it (opens read low, partly made up by counting a click as an
// open), and corporate link scanners follow links nobody clicked. HEAD requests,
// which scanners send and people don't, are never counted.

// The smallest transparent GIF.
var trackingPixel = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b,
}

func validDeliveryToken(token string) bool {
	_, ok := db.ParseSubscriberKey(token, "")
	return ok
}

// GetNewsletterOpen serves the open pixel and counts the open. It answers
// with the pixel whatever happens, so a mail client never shows a broken
// image.
//
// @Summary Newsletter open pixel
// @Tags newsletters
// @Produce image/gif
// @Param token path string true "Delivery token"
// @Success 200 {file} binary
// @Router /v1/newsletter/o/{token} [get]
func GetNewsletterOpen(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if r.Method == http.MethodGet && validDeliveryToken(token) {
			if err := db.RecordNewsletterOpen(r.Context(), conn, token); err != nil && !db.IsNoSubscriber(err) {
				slog.Error("record newsletter open", "error", err)
			}
		}
		h := w.Header()
		h.Set("Content-Type", "image/gif")
		// Every load is a separate open; Gmail's image proxy caches anyway,
		// which is why opens per reader are not meaningful, only first opens.
		h.Set("Cache-Control", "no-store, private")
		h.Set("Content-Length", strconv.Itoa(len(trackingPixel)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(trackingPixel)
	})
}

// GetNewsletterClick counts a click and redirects to the link. It redirects
// only to a link registered for the delivery's own issue.
//
// @Summary Newsletter click redirect
// @Tags newsletters
// @Param token path string true "Delivery token"
// @Param link path int true "Link ID"
// @Success 302
// @Failure 404 {string} string "HTML page"
// @Router /v1/newsletter/c/{token}/{link} [get]
func GetNewsletterClick(conn *sql.DB, siteURL string) http.Handler {
	f := unsubscribeFlow{siteURL: siteURL}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		linkID, err := strconv.ParseInt(r.PathValue("link"), 10, 64)
		if err != nil || linkID <= 0 || !validDeliveryToken(token) {
			f.notFound(w)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		target, err := db.RecordNewsletterClick(r.Context(), conn, token, linkID)
		if db.IsNoSubscriber(err) {
			f.notFound(w)
			return
		}
		if err != nil {
			f.failed(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
	})
}
