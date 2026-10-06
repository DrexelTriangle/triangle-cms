package models

import (
	"encoding/json"
	"time"
)

// Subscriber states. Unsubscribing is a state change, never a delete, so the
// row and its token outlive it.
const (
	NewsletterStatusSubscribed   = "subscribed"
	NewsletterStatusUnsubscribed = "unsubscribed"
)

// Campaign states. Only draft is reachable until sending exists; the send work
// adds "sending".
const (
	CampaignStatusDraft     = "draft"
	CampaignStatusScheduled = "scheduled"
	CampaignStatusSent      = "sent"
)

// Where a subscriber's latest (re)subscribe came from.
const (
	SubscriberSourcePublicForm      = "public_form"
	SubscriberSourceAdmin           = "admin"
	SubscriberSourceWordPressImport = "wordpress_import"
)

// NewsletterList is an audience a reader can join and a campaign can target.
type NewsletterList struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	IsPublic        bool       `json:"is_public"`
	SubscribedCount int        `json:"subscribed_count"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
}

// NewsletterSubscriber deliberately has no token field: the unsubscribe token
// is a credential and never leaves the server in an API response.
type NewsletterSubscriber struct {
	ID             int64      `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	ListIDs        []int64    `json:"list_ids"`
	SubscribedAt   *time.Time `json:"subscribed_at,omitempty"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at,omitempty"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

// NewsletterCampaign is one issue. BodyBlocks is the versioned block document
// (see internal/newsletter); list responses omit it.
type NewsletterCampaign struct {
	ID             int64           `json:"id"`
	Subject        string          `json:"subject"`
	PreviewText    string          `json:"preview_text"`
	BodyBlocks     json.RawMessage `json:"body_blocks,omitempty" swaggertype:"object"`
	Status         string          `json:"status"`
	ListIDs        []int64         `json:"list_ids"`
	ScheduledAt    *time.Time      `json:"scheduled_at,omitempty"`
	SentAt         *time.Time      `json:"sent_at,omitempty"`
	RecipientCount *int            `json:"recipient_count,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	UpdatedBy      string          `json:"updated_by,omitempty"`
	CreatedAt      *time.Time      `json:"created_at,omitempty"`
	UpdatedAt      *time.Time      `json:"updated_at,omitempty"`
}

// NewsletterSubscribeRequest is what the public subscribe form posts. Website
// is a honeypot: people never see the field, bots fill it in.
type NewsletterSubscribeRequest struct {
	Email   string  `json:"email"`
	Name    string  `json:"name"`
	Lists   []int64 `json:"lists"`
	Website string  `json:"website"`
}

type NewsletterSubscribeResponse struct {
	OK bool `json:"ok"`
}

type NewsletterListCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPublic    *bool  `json:"is_public"`
}

type NewsletterListPatchRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	IsPublic    *bool   `json:"is_public"`
}

type NewsletterListsResponse struct {
	Lists []NewsletterList `json:"lists"`
}

type NewsletterSubscriberCreateRequest struct {
	Email   string  `json:"email"`
	Name    string  `json:"name"`
	ListIDs []int64 `json:"list_ids"`
}

type NewsletterSubscriberPatchRequest struct {
	Name    *string  `json:"name"`
	Status  *string  `json:"status"`
	ListIDs *[]int64 `json:"list_ids"`
}

type NewsletterSubscribersResponse struct {
	Subscribers []NewsletterSubscriber `json:"subscribers"`
	Pagination  Pagination             `json:"pagination"`
	Counts      map[string]int         `json:"counts"`
}

type NewsletterCampaignCreateRequest struct {
	Subject     string          `json:"subject"`
	PreviewText string          `json:"preview_text"`
	BodyBlocks  json.RawMessage `json:"body_blocks" swaggertype:"object"`
	ListIDs     []int64         `json:"list_ids"`
}

// NewsletterCampaignPatchRequest is a partial update: a nil field is left
// alone. Status exists only so the API can refuse it explicitly.
type NewsletterCampaignPatchRequest struct {
	Subject     *string         `json:"subject"`
	PreviewText *string         `json:"preview_text"`
	Status      *string         `json:"status"`
	BodyBlocks  json.RawMessage `json:"body_blocks" swaggertype:"object"`
	ListIDs     *[]int64        `json:"list_ids"`
}

type NewsletterCampaignsResponse struct {
	Campaigns  []NewsletterCampaign `json:"campaigns"`
	Pagination Pagination           `json:"pagination"`
	Counts     map[string]int       `json:"counts"`
}

type NewsletterRecipientCountResponse struct {
	Count int `json:"count"`
}

type NewsletterStatsResponse struct {
	Subscribers map[string]int   `json:"subscribers"`
	Campaigns   map[string]int   `json:"campaigns"`
	Lists       []NewsletterList `json:"lists"`
}

type NewsletterRenderRequest struct {
	Subject     string          `json:"subject"`
	PreviewText string          `json:"preview_text"`
	BodyBlocks  json.RawMessage `json:"body_blocks" swaggertype:"object"`
}

type NewsletterLinkedArticle struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Published bool   `json:"published"`
}

type NewsletterRenderResponse struct {
	HTML     string                    `json:"html"`
	Warnings []string                  `json:"warnings"`
	Articles []NewsletterLinkedArticle `json:"articles"`
}
