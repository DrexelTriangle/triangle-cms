package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"server/internal/newsletter"
)

const (
	maxNewsletterListName        = 128
	maxNewsletterListDescription = 255
	maxNewsletterAdminBody       = 16 << 10
	// A campaign request is the block document plus a little envelope.
	maxNewsletterCampaignBody = newsletter.MaxDocumentBytes + 8<<10
	errSendingUnavailable     = "sending isn't available yet"
)

func newsletterIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	return id, err == nil && id > 0
}

func newsletterActor(r *http.Request) string {
	if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
		return user.Email
	}
	return "unknown"
}

// decodeNewsletterJSON reads one JSON document of at most limit bytes.
func decodeNewsletterJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(dst); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}

func newsletterServerError(w http.ResponseWriter, what string, err error) {
	slog.Error("newsletter: "+what, "error", err)
	writeError(w, http.StatusInternalServerError, what)
}

// checkListIDs validates an optional list selection for the CMS (private
// lists allowed). It writes the 400/500 itself and reports whether to go on.
func checkListIDs(w http.ResponseWriter, r *http.Request, conn *sql.DB, ids []int64) ([]int64, bool) {
	lists, ok := normalizeListIDs(ids, 0, maxNewsletterLists)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("list_ids must be up to %d positive ids", maxNewsletterLists))
		return nil, false
	}
	n, err := db.CountExistingNewsletterLists(r.Context(), conn, lists, false)
	if err != nil {
		newsletterServerError(w, "failed to check lists", err)
		return nil, false
	}
	if n != len(lists) {
		writeError(w, http.StatusBadRequest, "one or more lists do not exist")
		return nil, false
	}
	return lists, true
}

// ---------------------------------------------------------------- stats

// @Summary Newsletter stats
// @Description Subscriber and campaign counts by status, and subscribed members per list.
// @Tags newsletter
// @Produce json
// @Success 200 {object} models.NewsletterStatsResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/stats [get]
func GetNewsletterStats(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats, err := db.GetNewsletterStats(r.Context(), conn)
		if err != nil {
			newsletterServerError(w, "failed to fetch newsletter stats", err)
			return
		}
		writeJSON(w, http.StatusOK, stats)
	})
}

// ---------------------------------------------------------------- lists

// @Summary List newsletter lists
// @Tags newsletter
// @Produce json
// @Success 200 {object} models.NewsletterListsResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/lists [get]
func GetNewsletterLists(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lists, err := db.ListNewsletterLists(r.Context(), conn)
		if err != nil {
			newsletterServerError(w, "failed to fetch lists", err)
			return
		}
		writeJSON(w, http.StatusOK, models.NewsletterListsResponse{Lists: lists})
	})
}

func validListName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maxNewsletterListName
}

// @Summary Create a newsletter list
// @Description New lists get ids from 1000 up; lower ids are reserved for lists imported from WordPress.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param body body models.NewsletterListCreateRequest true "List"
// @Success 201 {object} models.NewsletterList
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/lists [post]
func PostNewsletterList(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.NewsletterListCreateRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterAdminBody, &body) {
			return
		}
		name := strings.TrimSpace(body.Name)
		description := strings.TrimSpace(body.Description)
		if !validListName(name) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("name is required and at most %d characters", maxNewsletterListName))
			return
		}
		if utf8.RuneCountInString(description) > maxNewsletterListDescription {
			writeError(w, http.StatusBadRequest, "description is too long")
			return
		}
		isPublic := true
		if body.IsPublic != nil {
			isPublic = *body.IsPublic
		}
		list, err := db.CreateNewsletterList(r.Context(), conn, name, description, isPublic)
		if errors.Is(err, db.ErrNewsletterDuplicate) {
			writeError(w, http.StatusConflict, "a list with that name already exists")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to create list", err)
			return
		}
		activity.LogRequest(r, "newsletter_list_created", "Newsletter list "+name+" created")
		writeJSON(w, http.StatusCreated, list)
	})
}

// @Summary Update a newsletter list
// @Tags newsletter
// @Accept json
// @Produce json
// @Param id path int true "List ID"
// @Param body body models.NewsletterListPatchRequest true "Fields to change"
// @Success 200 {object} models.NewsletterList
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/lists/{id} [patch]
func PatchNewsletterList(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body models.NewsletterListPatchRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterAdminBody, &body) {
			return
		}
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if !validListName(name) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("name is required and at most %d characters", maxNewsletterListName))
				return
			}
			body.Name = &name
		}
		if body.Description != nil {
			d := strings.TrimSpace(*body.Description)
			if utf8.RuneCountInString(d) > maxNewsletterListDescription {
				writeError(w, http.StatusBadRequest, "description is too long")
				return
			}
			body.Description = &d
		}
		list, err := db.UpdateNewsletterList(r.Context(), conn, id, body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "list not found")
		case errors.Is(err, db.ErrNewsletterDuplicate):
			writeError(w, http.StatusConflict, "a list with that name already exists")
		case err != nil:
			newsletterServerError(w, "failed to update list", err)
		default:
			activity.LogRequest(r, "newsletter_list_updated", "Newsletter list "+strconv.FormatInt(id, 10)+" updated")
			writeJSON(w, http.StatusOK, list)
		}
	})
}

// ---------------------------------------------------------------- subscribers

// @Summary List newsletter subscribers
// @Tags newsletter
// @Produce json
// @Param status query string false "Filter by status" Enums(subscribed, unsubscribed, all)
// @Param list_id query int false "Only members of this list"
// @Param q query string false "Substring of email or name"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Page size" default(50)
// @Success 200 {object} models.NewsletterSubscribersResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/subscribers [get]
func GetNewsletterSubscribers(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		status := strings.ToLower(strings.TrimSpace(q.Get("status")))
		switch status {
		case "", "all":
			status = ""
		case models.NewsletterStatusSubscribed, models.NewsletterStatusUnsubscribed:
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		var listID int64
		if raw := strings.TrimSpace(q.Get("list_id")); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list_id")
				return
			}
			listID = v
		}
		search := strings.TrimSpace(q.Get("q"))
		if len(search) > 254 {
			writeError(w, http.StatusBadRequest, "search is too long")
			return
		}
		page, limit, offset := listParams(r, 50)
		subs, total, err := db.ListNewsletterSubscribers(r.Context(), conn, db.SubscriberFilter{Status: status, ListID: listID, Query: search}, limit, offset)
		if err != nil {
			newsletterServerError(w, "failed to fetch subscribers", err)
			return
		}
		counts, err := db.CountNewsletterSubscribersByStatus(r.Context(), conn)
		if err != nil {
			newsletterServerError(w, "failed to fetch subscribers", err)
			return
		}
		writeJSON(w, http.StatusOK, models.NewsletterSubscribersResponse{
			Subscribers: subs,
			Pagination:  paginationResponse(page, limit, offset, offset+len(subs) < total, total),
			Counts:      counts,
		})
	})
}

// @Summary Add a newsletter subscriber
// @Tags newsletter
// @Accept json
// @Produce json
// @Param body body models.NewsletterSubscriberCreateRequest true "Subscriber"
// @Success 201 {object} models.NewsletterSubscriber
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/subscribers [post]
func PostNewsletterSubscriber(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.NewsletterSubscriberCreateRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterAdminBody, &body) {
			return
		}
		email, ok := normalizeNewsletterEmail(body.Email)
		if !ok {
			writeError(w, http.StatusBadRequest, "a valid email is required")
			return
		}
		name, ok := normalizeNewsletterName(body.Name)
		if !ok {
			writeError(w, http.StatusBadRequest, "name is invalid or too long")
			return
		}
		lists, ok := checkListIDs(w, r, conn, body.ListIDs)
		if !ok {
			return
		}
		sub, err := db.CreateNewsletterSubscriber(r.Context(), conn, email, name, lists)
		if errors.Is(err, db.ErrNewsletterDuplicate) {
			writeError(w, http.StatusConflict, "that address is already on file")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to add subscriber", err)
			return
		}
		activity.LogRequest(r, "newsletter_subscriber_created", "Newsletter subscriber "+strconv.FormatInt(sub.ID, 10)+" added")
		writeJSON(w, http.StatusCreated, sub)
	})
}

// @Summary Update a newsletter subscriber
// @Description Unsubscribing keeps the row and its token; it never deletes.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param id path int true "Subscriber ID"
// @Param body body models.NewsletterSubscriberPatchRequest true "Fields to change"
// @Success 200 {object} models.NewsletterSubscriber
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/subscribers/{id} [patch]
func PatchNewsletterSubscriber(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body models.NewsletterSubscriberPatchRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterAdminBody, &body) {
			return
		}
		if body.Status != nil && *body.Status != models.NewsletterStatusSubscribed && *body.Status != models.NewsletterStatusUnsubscribed {
			writeError(w, http.StatusBadRequest, "status must be subscribed or unsubscribed")
			return
		}
		if body.Name != nil {
			name, ok := normalizeNewsletterName(*body.Name)
			if !ok {
				writeError(w, http.StatusBadRequest, "name is invalid or too long")
				return
			}
			body.Name = &name
		}
		if body.ListIDs != nil {
			lists, ok := checkListIDs(w, r, conn, *body.ListIDs)
			if !ok {
				return
			}
			body.ListIDs = &lists
		}
		sub, err := db.UpdateNewsletterSubscriber(r.Context(), conn, id, body)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "subscriber not found")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to update subscriber", err)
			return
		}
		activity.LogRequest(r, "newsletter_subscriber_updated", "Newsletter subscriber "+strconv.FormatInt(id, 10)+" updated", "status", sub.Status)
		writeJSON(w, http.StatusOK, sub)
	})
}

// @Summary Erase a newsletter subscriber
// @Description Hard delete, for erasure requests only. To stop mail, unsubscribe instead.
// @Tags newsletter
// @Param id path int true "Subscriber ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/subscribers/{id} [delete]
func DeleteNewsletterSubscriber(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := db.DeleteNewsletterSubscriber(r.Context(), conn, id)
		if err != nil {
			newsletterServerError(w, "failed to delete subscriber", err)
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "subscriber not found")
			return
		}
		activity.LogRequest(r, "newsletter_subscriber_erased", "Newsletter subscriber "+strconv.FormatInt(id, 10)+" erased")
		w.WriteHeader(http.StatusNoContent)
	})
}

// ---------------------------------------------------------------- campaigns

// @Summary List newsletter campaigns
// @Tags newsletter
// @Produce json
// @Param status query string false "Filter by status" Enums(draft, scheduled, sent, all)
// @Param q query string false "Substring of the subject"
// @Param page query int false "Page" default(1)
// @Param limit query int false "Page size" default(25)
// @Success 200 {object} models.NewsletterCampaignsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns [get]
func GetNewsletterCampaigns(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		status := strings.ToLower(strings.TrimSpace(q.Get("status")))
		switch status {
		case "", "all":
			status = ""
		case models.CampaignStatusDraft, models.CampaignStatusScheduled, models.CampaignStatusSent:
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		search := strings.TrimSpace(q.Get("q"))
		if len(search) > maxSubjectLen {
			writeError(w, http.StatusBadRequest, "search is too long")
			return
		}
		page, limit, offset := listParams(r, 25)
		items, total, err := db.ListNewsletterCampaigns(r.Context(), conn, status, search, limit, offset)
		if err != nil {
			newsletterServerError(w, "failed to fetch campaigns", err)
			return
		}
		counts, err := db.CountNewsletterCampaignsByStatus(r.Context(), conn)
		if err != nil {
			newsletterServerError(w, "failed to fetch campaigns", err)
			return
		}
		writeJSON(w, http.StatusOK, models.NewsletterCampaignsResponse{
			Campaigns:  items,
			Pagination: paginationResponse(page, limit, offset, offset+len(items) < total, total),
			Counts:     counts,
		})
	})
}

// @Summary Get a newsletter campaign
// @Tags newsletter
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} models.NewsletterCampaign
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns/{id} [get]
func GetNewsletterCampaign(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		c, err := db.GetNewsletterCampaign(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "campaign not found")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to fetch campaign", err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
}

func validSubject(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= maxSubjectLen
}

// normalizeCampaignBody parses and normalizes a submitted block document and
// returns the canonical JSON to store. It writes the 400/500 itself.
func normalizeCampaignBody(w http.ResponseWriter, r *http.Request, conn *sql.DB, siteURL string, raw json.RawMessage) (json.RawMessage, bool) {
	doc, err := newsletter.Parse(raw)
	if err == nil {
		doc, err = newsletter.Normalize(r.Context(), doc, &db.NewsletterArticleLookup{Conn: conn}, siteURL)
	}
	var ve *newsletter.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusBadRequest, ve.Msg)
		return nil, false
	}
	if err != nil {
		newsletterServerError(w, "failed to check the newsletter body", err)
		return nil, false
	}
	out, err := json.Marshal(doc)
	if err != nil {
		newsletterServerError(w, "failed to save the newsletter body", err)
		return nil, false
	}
	return out, true
}

// @Summary Create a newsletter campaign
// @Description Always created as a draft. Links to the site's articles are locked to the article id.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param body body models.NewsletterCampaignCreateRequest true "Campaign"
// @Success 201 {object} models.NewsletterCampaign
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns [post]
func PostNewsletterCampaign(conn *sql.DB, siteURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.NewsletterCampaignCreateRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterCampaignBody, &body) {
			return
		}
		body.Subject = strings.TrimSpace(body.Subject)
		body.PreviewText = strings.TrimSpace(body.PreviewText)
		if !validSubject(body.Subject) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("subject is required and at most %d characters", maxSubjectLen))
			return
		}
		if utf8.RuneCountInString(body.PreviewText) > maxPreviewLen {
			writeError(w, http.StatusBadRequest, "preview_text is too long")
			return
		}
		lists, ok := checkListIDs(w, r, conn, body.ListIDs)
		if !ok {
			return
		}
		blocks, ok := normalizeCampaignBody(w, r, conn, siteURL, body.BodyBlocks)
		if !ok {
			return
		}
		body.ListIDs, body.BodyBlocks = lists, blocks
		c, err := db.CreateNewsletterCampaign(r.Context(), conn, body, newsletterActor(r))
		if err != nil {
			newsletterServerError(w, "failed to create campaign", err)
			return
		}
		activity.LogRequest(r, "newsletter_campaign_created", "Newsletter campaign "+strconv.FormatInt(c.ID, 10)+" created")
		writeJSON(w, http.StatusCreated, c)
	})
}

// @Summary Update a newsletter campaign
// @Description Partial update of a draft. Changing status is refused with 409 until sending exists; a sent campaign cannot be edited.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param id path int true "Campaign ID"
// @Param body body models.NewsletterCampaignPatchRequest true "Fields to change"
// @Success 200 {object} models.NewsletterCampaign
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns/{id} [patch]
func PatchNewsletterCampaign(conn *sql.DB, siteURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var body models.NewsletterCampaignPatchRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterCampaignBody, &body) {
			return
		}
		// Checked before anything is written: until delivery exists, nothing
		// may claim to be scheduled or sent.
		if body.Status != nil && *body.Status != models.CampaignStatusDraft {
			writeError(w, http.StatusConflict, errSendingUnavailable)
			return
		}
		if body.Subject != nil {
			s := strings.TrimSpace(*body.Subject)
			if !validSubject(s) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("subject is required and at most %d characters", maxSubjectLen))
				return
			}
			body.Subject = &s
		}
		if body.PreviewText != nil {
			p := strings.TrimSpace(*body.PreviewText)
			if utf8.RuneCountInString(p) > maxPreviewLen {
				writeError(w, http.StatusBadRequest, "preview_text is too long")
				return
			}
			body.PreviewText = &p
		}
		if body.ListIDs != nil {
			lists, ok := checkListIDs(w, r, conn, *body.ListIDs)
			if !ok {
				return
			}
			body.ListIDs = &lists
		}
		if body.BodyBlocks != nil {
			blocks, ok := normalizeCampaignBody(w, r, conn, siteURL, body.BodyBlocks)
			if !ok {
				return
			}
			body.BodyBlocks = blocks
		}
		c, err := db.UpdateNewsletterCampaign(r.Context(), conn, id, body, newsletterActor(r))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "campaign not found")
		case errors.Is(err, db.ErrCampaignSent):
			writeError(w, http.StatusConflict, "a sent campaign cannot be edited")
		case err != nil:
			newsletterServerError(w, "failed to update campaign", err)
		default:
			activity.LogRequest(r, "newsletter_campaign_updated", "Newsletter campaign "+strconv.FormatInt(id, 10)+" updated")
			writeJSON(w, http.StatusOK, c)
		}
	})
}

// @Summary Delete a draft newsletter campaign
// @Tags newsletter
// @Param id path int true "Campaign ID"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns/{id} [delete]
func DeleteNewsletterCampaign(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		deleted, err := db.DeleteNewsletterCampaign(r.Context(), conn, id)
		switch {
		case errors.Is(err, db.ErrCampaignNotDraft):
			writeError(w, http.StatusConflict, "only draft campaigns can be deleted")
		case err != nil:
			newsletterServerError(w, "failed to delete campaign", err)
		case !deleted:
			writeError(w, http.StatusNotFound, "campaign not found")
		default:
			activity.LogRequest(r, "newsletter_campaign_deleted", "Newsletter campaign "+strconv.FormatInt(id, 10)+" deleted")
			w.WriteHeader(http.StatusNoContent)
		}
	})
}

// @Summary Count a campaign's recipients
// @Description Distinct subscribed members of the campaign's lists, as the send would see them now.
// @Tags newsletter
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} models.NewsletterRecipientCountResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns/{id}/recipients/count [get]
func GetNewsletterRecipientCount(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		var exists int
		err := conn.QueryRowContext(r.Context(), "SELECT 1 FROM newsletter_campaigns WHERE id = ?", id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "campaign not found")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to count recipients", err)
			return
		}
		n, err := db.NewsletterRecipientCount(r.Context(), conn, id)
		if err != nil {
			newsletterServerError(w, "failed to count recipients", err)
			return
		}
		writeJSON(w, http.StatusOK, models.NewsletterRecipientCountResponse{Count: n})
	})
}

// ---------------------------------------------------------------- preview

func renderResponse(rendered newsletter.Rendered) models.NewsletterRenderResponse {
	out := models.NewsletterRenderResponse{HTML: rendered.HTML, Warnings: rendered.Warnings, Articles: []models.NewsletterLinkedArticle{}}
	for _, a := range rendered.Articles {
		out.Articles = append(out.Articles, models.NewsletterLinkedArticle{ID: a.ID, Title: a.Title, Published: a.Published})
	}
	return out
}

// previewOptions leaves the per-recipient links inert: a preview is not
// addressed to anyone.
func previewOptions(siteURL, subject, preview string) newsletter.RenderOptions {
	return newsletter.RenderOptions{
		SiteURL: siteURL, Subject: subject, PreviewText: preview,
		UnsubscribeURL: "#", ManageURL: "#", ViewOnlineURL: "#",
	}
}

// @Summary Render unsaved newsletter blocks
// @Description Normalizes and renders a block document for the editor's preview. Writes nothing.
// @Tags newsletter
// @Accept json
// @Produce json
// @Param body body models.NewsletterRenderRequest true "Blocks to render"
// @Success 200 {object} models.NewsletterRenderResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/render [post]
func PostNewsletterRender(conn *sql.DB, siteURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.NewsletterRenderRequest
		if !decodeNewsletterJSON(w, r, maxNewsletterCampaignBody, &body) {
			return
		}
		lookup := &db.NewsletterArticleLookup{Conn: conn}
		doc, err := newsletter.Parse(body.BodyBlocks)
		if err == nil {
			doc, err = newsletter.Normalize(r.Context(), doc, lookup, siteURL)
		}
		var ve *newsletter.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, ve.Msg)
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to render newsletter", err)
			return
		}
		rendered, err := newsletter.Render(r.Context(), doc, lookup, previewOptions(siteURL, body.Subject, body.PreviewText))
		if err != nil {
			newsletterServerError(w, "failed to render newsletter", err)
			return
		}
		writeJSON(w, http.StatusOK, renderResponse(rendered))
	})
}

// @Summary Preview a saved newsletter campaign
// @Description Renders the campaign with live article URLs. Per-recipient links are left as "#".
// @Tags newsletter
// @Produce json
// @Param id path int true "Campaign ID"
// @Success 200 {object} models.NewsletterRenderResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/newsletter/campaigns/{id}/preview [get]
func GetNewsletterCampaignPreview(conn *sql.DB, siteURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := newsletterIDParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid id")
			return
		}
		c, err := db.GetNewsletterCampaign(r.Context(), conn, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "campaign not found")
			return
		}
		if err != nil {
			newsletterServerError(w, "failed to fetch campaign", err)
			return
		}
		doc, err := newsletter.Parse(c.BodyBlocks)
		if err != nil {
			newsletterServerError(w, "stored newsletter body is invalid", err)
			return
		}
		rendered, err := newsletter.Render(r.Context(), doc, &db.NewsletterArticleLookup{Conn: conn}, previewOptions(siteURL, c.Subject, c.PreviewText))
		if err != nil {
			newsletterServerError(w, "failed to render newsletter", err)
			return
		}
		writeJSON(w, http.StatusOK, renderResponse(rendered))
	})
}
