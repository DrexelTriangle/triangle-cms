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
