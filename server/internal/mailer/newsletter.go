package mailer

import (
	"html"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

// The placeholders the dashboard writes into email_html (see
// frontend/src/lib/newsletter/template.ts) for the sender to fill per copy.
const (
	tokenUnsubscribe = "{{unsubscribe_url}}"
	tokenManage      = "{{manage_subscription_url}}"
	tokenViewOnline  = "{{view_online_url}}"
)

// TestToken stands in for a subscriber token in test sends, whose recipients
// need not be subscribers. The unsubscribe page must treat it as a no-op.
const TestToken = "test"

// Newsletter turns a saved issue into one personalised Message per recipient.
type Newsletter struct {
	From *mail.Address
	// PublicURL is the origin readers reach the CMS API on, e.g.
	// https://cms.thetriangle.org. Every link in the email is built from it.
	PublicURL string
}

// Issue is the part of a saved newsletter the email is built from.
type Issue struct {
	ID        int64
	Subject   string
	EmailHTML string
}

func (n *Newsletter) UnsubscribeURL(token string) string {
	return strings.TrimRight(n.PublicURL, "/") + "/v1/newsletter/unsubscribe?t=" + url.QueryEscape(token)
}

func (n *Newsletter) ViewOnlineURL(issueID int64) string {
	return strings.TrimRight(n.PublicURL, "/") + "/v1/newsletter/issues/" + strconv.FormatInt(issueID, 10)
}

// Compose builds the copy for one recipient. token is the subscriber's
// unsubscribe token, or TestToken for a test send, which also marks the
// subject so nobody mistakes the test for the real issue.
func (n *Newsletter) Compose(issue Issue, to *mail.Address, token string) *Message {
	unsubscribe := n.UnsubscribeURL(token)
	viewOnline := n.ViewOnlineURL(issue.ID)
	body := strings.NewReplacer(
		tokenUnsubscribe, html.EscapeString(unsubscribe),
		// There is no separate preferences page yet; the unsubscribe page is
		// the only thing a subscriber can manage.
		tokenManage, html.EscapeString(unsubscribe),
		tokenViewOnline, html.EscapeString(viewOnline),
	).Replace(issue.EmailHTML)

	subject := issue.Subject
	if token == TestToken {
		subject = "[Test] " + subject
	}
	return &Message{
		From:           n.From,
		To:             to,
		Subject:        subject,
		HTML:           body,
		Text:           issue.Subject + "\n\nRead this issue online: " + viewOnline + "\n\nUnsubscribe: " + unsubscribe + "\n",
		UnsubscribeURL: unsubscribe,
	}
}
