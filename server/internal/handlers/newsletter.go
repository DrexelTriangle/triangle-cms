package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"server/internal/akismet"
	db "server/internal/database"
	"server/internal/models"
)

// The public subscribe endpoint answers every accepted request the same way,
// so it cannot be used to learn whether an address is on the list, or whether
// a submission was taken for spam.
var newsletterSubscribeOK = models.NewsletterSubscribeResponse{OK: true}

const (
	newsletterInvalidSubscription = "invalid subscription"
	newsletterSubscriptionFailed  = "subscription failed"
)

// PostNewsletterSubscribe is the one newsletter route open to the internet,
// for the public site's subscribe form. Its input is treated as hostile: the
// body is capped, every field is validated strictly, a honeypot field and
// Akismet catch bots, and the route is rate limited per IP and globally.
//
// spamChecker may be nil (Akismet not configured). If Akismet errors the
// subscription is kept: it is single opt-in and nothing is mailed yet, so a
// missed bot costs a row, while a dropped reader costs a subscriber.
//
// @Summary Subscribe to the newsletter
// @Description Public. Always answers 202 {"ok":true} for an accepted submission, including one already on file or discarded as spam.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param body body models.NewsletterSubscribeRequest true "Subscription"
// @Success 202 {object} models.NewsletterSubscribeResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 429 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/newsletter/subscribe [post]
func PostNewsletterSubscribe(conn *sql.DB, spamChecker akismet.Checker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNewsletterSubscribeBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}
		var req models.NewsletterSubscribeRequest
		dec := json.NewDecoder(bytes.NewReader(body))
		if err := dec.Decode(&req); err != nil || dec.More() {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}

		// People never see the honeypot field; anything in it is a bot.
		if req.Website != "" {
			slog.Info("newsletter subscribe", "outcome", "honeypot")
			writeJSON(w, http.StatusAccepted, newsletterSubscribeOK)
			return
		}

		email, ok := normalizeNewsletterEmail(req.Email)
		if !ok {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}
		name, ok := normalizeNewsletterName(req.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}
		lists, ok := normalizeListIDs(req.Lists, 1, maxNewsletterLists)
		if !ok {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}
		public, err := db.CountExistingNewsletterLists(r.Context(), conn, lists, true)
		if err != nil {
			slog.Error("newsletter subscribe: list check failed", "error", err)
			writeError(w, http.StatusInternalServerError, newsletterSubscriptionFailed)
			return
		}
		if public != len(lists) {
			writeError(w, http.StatusBadRequest, newsletterInvalidSubscription)
			return
		}

		ip := clientIP(r)
		if spamChecker != nil {
			isSpam, err := spamChecker.CheckComment(r.Context(), akismet.Comment{
				UserIP:      ip,
				UserAgent:   r.UserAgent(),
				Referrer:    r.Referer(),
				Type:        "signup",
				Author:      name,
				AuthorEmail: email,
				CreatedAt:   time.Now().UTC(),
			})
			switch {
			case err != nil && akismet.IsConfigError(err):
				slog.Error("akismet is misconfigured; newsletter signups are not spam-checked", "error", err)
			case err != nil:
				slog.Warn("akismet signup check failed; keeping the subscription", "error", err)
			case isSpam:
				slog.Info("newsletter subscribe", "outcome", "spam")
				writeJSON(w, http.StatusAccepted, newsletterSubscribeOK)
				return
			}
		}

		outcome, err := db.SubscribePublic(r.Context(), conn, db.PublicSubscription{
			Email: email, Name: name, ListIDs: lists, IP: ip,
		})
		if err != nil {
			slog.Error("newsletter subscribe failed", "error", err)
			writeError(w, http.StatusInternalServerError, newsletterSubscriptionFailed)
			return
		}
		// No address in the log: it is personal data and the outcome is enough.
		slog.Info("newsletter subscribe", "outcome", string(outcome), "lists", len(lists))
		writeJSON(w, http.StatusAccepted, newsletterSubscribeOK)
	})
}
