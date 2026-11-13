package models

import "time"

// Newsletter issue statuses. Editors move an issue between draft and scheduled;
// sending and sent belong to the sender, and once an issue reaches either it is
// frozen: what went out must stay what the record says went out.
const (
	NewsletterStatusDraft     = "draft"
	NewsletterStatusScheduled = "scheduled"
	NewsletterStatusSending   = "sending"
	NewsletterStatusSent      = "sent"
)

// Subscriber statuses, after the WordPress Newsletter plugin's C/U/B.
const (
	SubscriberStatusConfirmed    = "confirmed"
	SubscriberStatusUnsubscribed = "unsubscribed"
	SubscriberStatusBounced      = "bounced"
)

// NewsletterSponsor is the paid banner above the masthead.
type NewsletterSponsor struct {
	ImageURL string `json:"image_url"`
	LinkURL  string `json:"link_url"`
	Alt      string `json:"alt"`
}

// Newsletter is one issue of To The Point. EmailHTML is rendered by the
// dashboard and stored verbatim; the sender substitutes {{unsubscribe_url}},
// {{manage_subscription_url}} and {{view_online_url}} per recipient. See
// docs/newsletter-api.md.
type Newsletter struct {
	ID             int64              `json:"id"`
	Subject        string             `json:"subject"`
	Preheader      string             `json:"preheader"`
	Status         string             `json:"status"`
	Sponsor        *NewsletterSponsor `json:"sponsor"`
	BodyHTML       string             `json:"body_html"`
	EmailHTML      string             `json:"email_html"`
	ScheduledFor   *time.Time         `json:"scheduled_for"`
	SentAt         *time.Time         `json:"sent_at"`
	RecipientCount int                `json:"recipient_count"`
	// Unique openers and unique clickers among real (not test) deliveries.
	OpenCount  int       `json:"open_count"`
	ClickCount int       `json:"click_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewsletterInput is what the dashboard sends on create and save.
type NewsletterInput struct {
	Subject   string             `json:"subject"`
	Preheader string             `json:"preheader"`
	Sponsor   *NewsletterSponsor `json:"sponsor"`
	BodyHTML  string             `json:"body_html"`
	EmailHTML string             `json:"email_html"`
}

type NewslettersResponse struct {
	Newsletters []Newsletter `json:"newsletters"`
}

type NewsletterScheduleRequest struct {
	SendAt time.Time `json:"send_at"`
}

type NewsletterTestRequest struct {
	Emails []string `json:"emails"`
}

// NewsletterSubscribeRequest is the public subscribe form. Website is a
// honeypot: hidden from readers, so only a bot fills it in.
type NewsletterSubscribeRequest struct {
	Email   string `json:"email"`
	Lists   []int  `json:"lists"`
	Website string `json:"website,omitempty"`
}

// NewsletterSubscribeResponse keeps the WordPress plugin's success flag, which
// the public site's form already reads.
type NewsletterSubscribeResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// NewsletterStats feeds the dashboard's summary tiles. AvgOpenRate and
// AvgClickRate are 0..1 across sent issues with at least one recipient, and
// null when there are none.
type NewsletterStats struct {
	SubscriberCount   int      `json:"subscriber_count"`
	SentCount         int      `json:"sent_count"`
	RecipientsReached int      `json:"recipients_reached"`
	AvgOpenRate       *float64 `json:"avg_open_rate"`
	AvgClickRate      *float64 `json:"avg_click_rate"`
}

// NewsletterCampaignRecipients is where an issue's real (not test) copies
// ended up. Queued is every copy the send started with; InFlight is pending or
// mid-send.
type NewsletterCampaignRecipients struct {
	Queued   int `json:"queued"`
	Sent     int `json:"sent"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	InFlight int `json:"in_flight"`
}

// NewsletterCampaignSummary is one issue's headline numbers. A click counts as
// an open. Rates are 0..1 and null when their denominator is 0: OpenRate,
// ClickRate and UnsubscribeRate are over Recipients.Sent, ClickToOpenRate is
// UniqueClickers / UniqueOpens.
//
// DetailedTracking says every copy that went out carried per-link and
// unsubscribe tracking. Without it Unsubscribes is null: unknown, not zero.
type NewsletterCampaignSummary struct {
	ID               int64                        `json:"id"`
	Subject          string                       `json:"subject"`
	Status           string                       `json:"status"`
	SentAt           *time.Time                   `json:"sent_at"`
	DetailedTracking bool                         `json:"detailed_tracking"`
	Recipients       NewsletterCampaignRecipients `json:"recipients"`
	UniqueOpens      int                          `json:"unique_opens"`
	UniqueClickers   int                          `json:"unique_clickers"`
	TotalClicks      int                          `json:"total_clicks"`
	Unsubscribes     *int                         `json:"unsubscribes"`
	OpenRate         *float64                     `json:"open_rate"`
	ClickRate        *float64                     `json:"click_rate"`
	ClickToOpenRate  *float64                     `json:"click_to_open_rate"`
	UnsubscribeRate  *float64                     `json:"unsubscribe_rate"`
}

// NewsletterCampaignLink is one tracked link. UniqueClickers is null for an
// issue without detailed tracking.
type NewsletterCampaignLink struct {
	ID             int64  `json:"id"`
	URL            string `json:"url"`
	TotalClicks    int    `json:"total_clicks"`
	UniqueClickers *int   `json:"unique_clickers"`
}

// NewsletterSendError is a final send error, with any address in it masked,
// and how many copies ended with it.
type NewsletterSendError struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// NewsletterCampaignStats is GET /v1/newsletters/{id}/stats. Links are most
// clicked first, errors most frequent first, and Recent is this issue and the
// sent issues before it, newest first.
type NewsletterCampaignStats struct {
	NewsletterCampaignSummary
	Links  []NewsletterCampaignLink    `json:"links"`
	Errors []NewsletterSendError       `json:"errors"`
	Recent []NewsletterCampaignSummary `json:"recent"`
}

// NewsletterSubscriber is one address on the mailing list. Lists holds the
// WordPress plugin's list ids (1-7), which the CMS keeps unchanged.
type NewsletterSubscriber struct {
	ID             int64      `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Lists          []int      `json:"lists"`
	SubscribedAt   time.Time  `json:"subscribed_at"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at,omitempty"`
}

type NewsletterSubscribersResponse struct {
	Subscribers []NewsletterSubscriber `json:"subscribers"`
}
