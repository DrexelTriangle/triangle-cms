package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// The public subscribe form is a few fields; anything bigger isn't one.
const maxNewsletterSubscribeBytes = 4 << 10

// PostNewsletterSubscribe adds an address from the public site's form. It is
// single opt-in, as the WordPress plugin was: the address is confirmed at
// once. Every valid submission gets the same answer, whether the address was
// new, already subscribed, or had unsubscribed (which it stays: the form can't
// undo an unsubscribe), so the form can't be used to find out who is on the
// list. A filled-in honeypot gets the same answer and stores nothing.
//
// @Summary Subscribe to the newsletter
// @Description Public, rate limited per IP. Lists are ids 1-7; none means no particular list.
// @Tags newsletters
// @Accept json
// @Produce json
// @Param body body models.NewsletterSubscribeRequest true "Address and lists"
// @Success 200 {object} models.NewsletterSubscribeResponse
// @Failure 400 {object} models.NewsletterSubscribeResponse
// @Failure 429 {object} models.ErrorResponse
// @Failure 500 {object} models.NewsletterSubscribeResponse
// @Router /v1/newsletter-subscribers/subscribe [post]
func PostNewsletterSubscribe(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.NewsletterSubscribeRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxNewsletterSubscribeBytes)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			subscribeError(w, http.StatusBadRequest, "Send an email address and the lists to join.")
			return
		}
		// Before any validation, so a bot learns nothing from the answer,
		// whatever else it sent.
		if strings.TrimSpace(body.Website) != "" {
			slog.Info("newsletter subscribe honeypot filled; ignored")
			writeJSON(w, http.StatusOK, models.NewsletterSubscribeResponse{Success: true})
			return
		}
		email, ok := parseSubscribeAddress(body.Email)
		if !ok {
			subscribeError(w, http.StatusBadRequest, "That doesn't look like an email address.")
			return
		}
		lists, ok := parseSubscribeLists(body.Lists)
		if !ok {
			subscribeError(w, http.StatusBadRequest, "Pick lists from the form.")
			return
		}

		outcome, err := db.SubscribeNewsletter(r.Context(), conn, email, lists)
		if err != nil {
			slog.Error("newsletter subscribe failed", "error", err)
			subscribeError(w, http.StatusInternalServerError, "Something went wrong. Please try again in a minute.")
			return
		}
		// No address in the log: it is a reader's, and the outcome is all
		// anyone needs to see the form working.
		switch outcome {
		case db.Subscribed:
			slog.Info("newsletter subscriber added", "lists", len(lists))
		case db.NotResubscribed:
			slog.Info("newsletter subscribe for an unsubscribed or bounced address; left as it was")
		}
		writeJSON(w, http.StatusOK, models.NewsletterSubscribeResponse{Success: true})
	})
}

func subscribeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, models.NewsletterSubscribeResponse{Success: false, Error: msg})
}

// parseSubscribeAddress accepts one bare address with a dotted domain, the
// shape every real mailbox has, and returns it trimmed.
func parseSubscribeAddress(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxNewsletterFieldLength {
		return "", false
	}
	addr, err := netmail.ParseAddress(raw)
	if err != nil || addr.Address != raw {
		return "", false
	}
	at := strings.LastIndexByte(raw, '@')
	if at <= 0 {
		return "", false
	}
	domain := raw[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", false
	}
	return raw, true
}

// parseSubscribeLists dedupes the list ids and refuses any that isn't one of
// the seven.
func parseSubscribeLists(ids []int) ([]int, bool) {
	seen := map[int]bool{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !db.IsNewsletterList(id) {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, true
}
