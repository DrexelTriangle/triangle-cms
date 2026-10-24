package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

const (
	// A To The Point issue renders to ~92 KB of email HTML; this leaves room
	// for a long one without letting a request park megabytes in a row.
	maxNewsletterRequestBytes = 4 << 20
	maxNewsletterFieldLength  = 255
	maxNewsletterURLLength    = 2048
)

// @Summary List newsletter issues
// @Tags newsletters
// @Produce json
// @Success 200 {object} models.NewslettersResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters [get]
func GetNewsletters(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := db.ListNewsletters(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, models.NewslettersResponse{Newsletters: items})
	})
}

// @Summary Newsletter summary stats
// @Tags newsletters
// @Produce json
// @Success 200 {object} models.NewsletterStats
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/stats [get]
func GetNewsletterStats(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats, err := db.GetNewsletterStats(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, stats)
	})
}

// @Summary Get a newsletter issue
// @Tags newsletters
// @Produce json
// @Param id path int true "Newsletter ID"
// @Success 200 {object} models.Newsletter
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id} [get]
func GetNewsletter(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		item, err := db.GetNewsletter(r.Context(), conn, id)
		if err != nil {
			writeNewsletterError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
}

// PostNewsletter creates a draft issue.
//
// @Summary Create a newsletter issue
// @Tags newsletters
// @Accept json
// @Produce json
// @Param body body models.NewsletterInput true "Issue content"
// @Success 201 {object} models.Newsletter
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters [post]
func PostNewsletter(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeNewsletterInput(w, r)
		if !ok {
			return
		}
		created, err := db.InsertNewsletter(r.Context(), conn, in, requestUserEmail(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		activity.LogRequest(r, "newsletter_created", "Newsletter "+strconv.FormatInt(created.ID, 10), "subject", created.Subject)
		writeJSON(w, http.StatusCreated, created)
	})
}

// PutNewsletter saves an issue's content. Refused with 409 once the issue is
// sending or sent.
//
// @Summary Save a newsletter issue
// @Tags newsletters
// @Accept json
// @Produce json
// @Param id path int true "Newsletter ID"
// @Param body body models.NewsletterInput true "Issue content"
// @Success 200 {object} models.Newsletter
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id} [put]
func PutNewsletter(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		in, ok := decodeNewsletterInput(w, r)
		if !ok {
			return
		}
		updated, err := db.UpdateNewsletter(r.Context(), conn, id, in, requestUserEmail(r))
		if err != nil {
			writeNewsletterError(w, err)
			return
		}
		activity.LogRequest(r, "newsletter_updated", "Newsletter "+strconv.FormatInt(id, 10), "subject", updated.Subject)
		writeJSON(w, http.StatusOK, updated)
	})
}

// @Summary Delete a newsletter issue
// @Tags newsletters
// @Produce json
// @Param id path int true "Newsletter ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id} [delete]
func DeleteNewsletter(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := db.DeleteNewsletter(r.Context(), conn, id); err != nil {
			writeNewsletterError(w, err)
			return
		}
		activity.LogRequest(r, "newsletter_deleted", "Newsletter "+strconv.FormatInt(id, 10)+" deleted")
		w.WriteHeader(http.StatusNoContent)
	})
}

// PostNewsletterSchedule queues an issue for send_at, or moves its send time.
// It only records the schedule: nothing sends a scheduled issue until the
// sender lands.
//
// @Summary Schedule a newsletter issue
// @Tags newsletters
// @Accept json
// @Produce json
// @Param id path int true "Newsletter ID"
// @Param body body models.NewsletterScheduleRequest true "Send time (RFC 3339)"
// @Success 200 {object} models.Newsletter
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id}/schedule [post]
func PostNewsletterSchedule(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body models.NewsletterScheduleRequest
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "send_at must be an RFC 3339 time")
			return
		}
		if !body.SendAt.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "send_at must be in the future")
			return
		}
		updated, err := db.ScheduleNewsletter(r.Context(), conn, id, body.SendAt, requestUserEmail(r))
		if err != nil {
			writeNewsletterError(w, err)
			return
		}
		activity.LogRequest(r, "newsletter_scheduled", "Newsletter "+strconv.FormatInt(id, 10), "send_at", body.SendAt.UTC().Format(time.RFC3339))
		writeJSON(w, http.StatusOK, updated)
	})
}

// @Summary Unschedule a newsletter issue
// @Tags newsletters
// @Produce json
// @Param id path int true "Newsletter ID"
// @Success 200 {object} models.Newsletter
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id}/schedule [delete]
func DeleteNewsletterSchedule(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		updated, err := db.UnscheduleNewsletter(r.Context(), conn, id, requestUserEmail(r))
		if err != nil {
			writeNewsletterError(w, err)
			return
		}
		activity.LogRequest(r, "newsletter_unscheduled", "Newsletter "+strconv.FormatInt(id, 10))
		writeJSON(w, http.StatusOK, updated)
	})
}

// PostNewsletterTest is a placeholder until the sender exists. It answers 501
// rather than 204 so the dashboard never reports a test email that was not sent.
//
// @Summary Send a test of a newsletter issue
// @Tags newsletters
// @Accept json
// @Produce json
// @Param id path int true "Newsletter ID"
// @Param body body models.NewsletterTestRequest true "Recipients"
// @Failure 501 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletters/{id}/test [post]
func PostNewsletterTest() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "Sending isn't set up yet, so no test email was sent.")
	})
}

// GetNewsletterSubscribers lists subscribers, confirmed only unless ?status=
// names another status or "all".
//
// @Summary List newsletter subscribers
// @Tags newsletters
// @Produce json
// @Param status query string false "Filter by status (default confirmed)" Enums(confirmed, unsubscribed, bounced, all)
// @Success 200 {object} models.NewsletterSubscribersResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter-subscribers [get]
func GetNewsletterSubscribers(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		switch {
		case status == "":
			status = models.SubscriberStatusConfirmed
		case status == "all":
			status = ""
		case !db.ValidSubscriberStatuses[status]:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		items, err := db.ListNewsletterSubscribers(r.Context(), conn, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, models.NewsletterSubscribersResponse{Subscribers: items})
	})
}

// DeleteNewsletterSubscriber erases a subscriber. This is removal, not
// unsubscribing: no record of the address is kept.
//
// @Summary Delete a newsletter subscriber
// @Tags newsletters
// @Produce json
// @Param id path int true "Subscriber ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter-subscribers/{id} [delete]
func DeleteNewsletterSubscriber(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := classifiedIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := db.DeleteNewsletterSubscriber(r.Context(), conn, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "subscriber not found")
			return
		}
		activity.LogRequest(r, "newsletter_subscriber_deleted", "Subscriber "+strconv.FormatInt(id, 10)+" deleted")
		w.WriteHeader(http.StatusNoContent)
	})
}

func decodeNewsletterInput(w http.ResponseWriter, r *http.Request) (models.NewsletterInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxNewsletterRequestBytes)
	var in models.NewsletterInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "newsletter is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON")
		}
		return in, false
	}

	in.Subject = strings.TrimSpace(in.Subject)
	in.Preheader = strings.TrimSpace(in.Preheader)
	if in.Sponsor != nil {
		in.Sponsor.ImageURL = strings.TrimSpace(in.Sponsor.ImageURL)
		in.Sponsor.LinkURL = strings.TrimSpace(in.Sponsor.LinkURL)
		in.Sponsor.Alt = strings.TrimSpace(in.Sponsor.Alt)
		if in.Sponsor.ImageURL == "" {
			in.Sponsor = nil
		}
	}

	if msg := validateNewsletterInput(in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return in, false
	}
	return in, true
}

func validateNewsletterInput(in models.NewsletterInput) string {
	switch {
	case in.Subject == "":
		return "subject is required"
	case len(in.Subject) > maxNewsletterFieldLength:
		return "subject is too long"
	case len(in.Preheader) > maxNewsletterFieldLength:
		return "preheader is too long"
	case strings.TrimSpace(in.EmailHTML) == "":
		return "email_html is required"
	}
	if s := in.Sponsor; s != nil {
		if !isHTTPURL(s.ImageURL) || len(s.ImageURL) > maxNewsletterURLLength {
			return "sponsor image_url must be an http(s) URL"
		}
		if s.LinkURL != "" && (!isHTTPURL(s.LinkURL) || len(s.LinkURL) > maxNewsletterURLLength) {
			return "sponsor link_url must be an http(s) URL"
		}
		if len(s.Alt) > maxNewsletterFieldLength {
			return "sponsor alt is too long"
		}
	}
	return ""
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func writeNewsletterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "newsletter not found")
	case errors.Is(err, db.ErrNewsletterLocked):
		writeError(w, http.StatusConflict, "This newsletter has already gone out, so it can't be changed.")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func requestUserEmail(r *http.Request) string {
	if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
		return user.Email
	}
	return ""
}
