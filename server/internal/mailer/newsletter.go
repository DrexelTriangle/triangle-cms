package mailer

import (
	"html"
	"net/mail"
	"net/url"
	"regexp"
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

// The dashboard's template writes every link as a double-quoted href (it is
// serialised from a DOM), so a pattern finds them all without re-rendering the
// HTML, which a parser round trip would alter in ways mail clients notice.
var trackableHref = regexp.MustCompile(`(?i)\bhref="(https?://[^"]+)"`)

// TrackableURLs lists the distinct http(s) links in an issue, in order. The
// placeholders aren't URLs yet at this point, so the unsubscribe and
// view-online links are never among them: one-click unsubscribe has to reach
// the CMS directly.
func TrackableURLs(emailHTML string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, m := range trackableHref.FindAllStringSubmatch(emailHTML, -1) {
		u := html.UnescapeString(m[1])
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

// Tracking turns one copy's links into click redirects and adds an open
// pixel. Links maps each URL from TrackableURLs to its registered id; a URL
// missing from it is left as it is.
type Tracking struct {
	Delivery string
	Links    map[string]int64
}

func (n *Newsletter) ClickURL(delivery string, linkID int64) string {
	return strings.TrimRight(n.PublicURL, "/") + "/v1/newsletter/c/" + url.PathEscape(delivery) + "/" + strconv.FormatInt(linkID, 10)
}

func (n *Newsletter) OpenPixelURL(delivery string) string {
	return strings.TrimRight(n.PublicURL, "/") + "/v1/newsletter/o/" + url.PathEscape(delivery)
}

func (n *Newsletter) track(emailHTML string, t *Tracking) string {
	body := trackableHref.ReplaceAllStringFunc(emailHTML, func(attr string) string {
		u := html.UnescapeString(trackableHref.FindStringSubmatch(attr)[1])
		id, ok := t.Links[u]
		if !ok {
			return attr
		}
		return `href="` + html.EscapeString(n.ClickURL(t.Delivery, id)) + `"`
	})
	pixel := `<img src="` + html.EscapeString(n.OpenPixelURL(t.Delivery)) + `" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0;">`
	if i := strings.LastIndex(strings.ToLower(body), "</body>"); i >= 0 {
		return body[:i] + pixel + body[i:]
	}
	return body + pixel
}

// Compose builds the copy for one recipient. token is the subscriber's
// unsubscribe token, or TestToken for a test send, which also marks the
// subject so nobody mistakes the test for the real issue. tracking may be
// nil for an untracked copy.
func (n *Newsletter) Compose(issue Issue, to *mail.Address, token string, tracking *Tracking) *Message {
	unsubscribe := n.UnsubscribeURL(token)
	viewOnline := n.ViewOnlineURL(issue.ID)
	emailHTML := issue.EmailHTML
	if tracking != nil {
		emailHTML = n.track(emailHTML, tracking)
	}
	body := strings.NewReplacer(
		tokenUnsubscribe, html.EscapeString(unsubscribe),
		// There is no separate preferences page yet; the unsubscribe page is
		// the only thing a subscriber can manage.
		tokenManage, html.EscapeString(unsubscribe),
		tokenViewOnline, html.EscapeString(viewOnline),
	).Replace(emailHTML)

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
