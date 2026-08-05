// Package slack posts the classified-moderation notification into Slack.
//
// This is the outbound half of the moderation loop. The inbound half — the
// Approve/Reject clicks — is handled in internal/handlers/slack.go, which
// authenticates by request signature. The two halves are configured by
// different secrets and either can be absent, so nothing here assumes the
// other end exists.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// A submission is a reader waiting on a moderator, not a request the reader is
// blocked on, so the post gets a short deadline and no retry.
const defaultTimeout = 5 * time.Second

// Slack truncates section text at 3000 characters and rejects the whole message
// if it is longer, so an overlong classified must be trimmed rather than lost.
const maxSectionTextBytes = 2900

// The action_ids the interactivity handler in internal/handlers/slack.go
// switches on. Changing either of these breaks the buttons on every message
// already sitting in the channel.
const (
	ActionApprove = "approve"
	ActionReject  = "reject"
)

// Notifier posts a pending classified to Slack for moderation.
type Notifier interface {
	NotifyClassified(ctx context.Context, c Classified) error
}

// Classified is the subset of a submission the Slack message shows.
type Classified struct {
	ID      int64
	Name    string
	Email   string
	Label   string
	Message string
	EndDate string
}

type Config struct {
	// WebhookURL is a Slack incoming-webhook URL. The channel is fixed by the
	// webhook itself, not by anything this package sends.
	WebhookURL string
	// QueueURL, when set, is linked from the message so a moderator can open
	// the CMS queue instead of deciding from Slack.
	QueueURL   string
	HTTPClient *http.Client
}

type Client struct {
	webhookURL string
	queueURL   string
	httpClient *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	webhookURL := strings.TrimSpace(cfg.WebhookURL)
	if webhookURL == "" {
		return nil, fmt.Errorf("slack webhook url is required")
	}
	parsed, err := url.ParseRequestURI(webhookURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("slack webhook url must be a full https URL")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		webhookURL: webhookURL,
		queueURL:   strings.TrimSpace(cfg.QueueURL),
		httpClient: httpClient,
	}, nil
}

func (c *Client) NotifyClassified(ctx context.Context, classified Classified) error {
	payload, err := json.Marshal(c.buildMessage(classified))
	if err != nil {
		return fmt.Errorf("encode slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to slack webhook failed: %w", err)
	}
	defer resp.Body.Close()

	// A webhook answers "ok" with 200; everything else — a revoked webhook, a
	// deleted channel, a malformed block — comes back as a short body worth
	// putting in the log verbatim, because it is the only diagnostic Slack gives.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack webhook returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// buildMessage renders the Block Kit payload. The button values are the
// classified's row id as a decimal string, which is exactly what the
// interactivity handler parses back out.
func (c *Client) buildMessage(classified Classified) map[string]any {
	id := strconv.FormatInt(classified.ID, 10)

	fields := []map[string]any{
		{"type": "mrkdwn", "text": "*From*\n" + plain(classified.Name)},
		{"type": "mrkdwn", "text": "*Email*\n" + plain(classified.Email)},
	}
	if label := strings.TrimSpace(classified.Label); label != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Category*\n" + plain(label)})
	}
	if endDate := strings.TrimSpace(classified.EndDate); endDate != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Runs until*\n" + plain(endDate)})
	}

	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": "New classified #" + id},
		},
		{
			"type":   "section",
			"fields": fields,
		},
		{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": quote(classified.Message)},
		},
		{
			"type": "actions",
			"elements": []map[string]any{
				{
					"type":      "button",
					"action_id": ActionApprove,
					"style":     "primary",
					"text":      map[string]any{"type": "plain_text", "text": "Approve"},
					"value":     id,
				},
				{
					"type":      "button",
					"action_id": ActionReject,
					"style":     "danger",
					"text":      map[string]any{"type": "plain_text", "text": "Reject"},
					"value":     id,
				},
			},
		},
	}

	if c.queueURL != "" {
		blocks = append(blocks, map[string]any{
			"type": "context",
			"elements": []map[string]any{
				{"type": "mrkdwn", "text": "<" + c.queueURL + "|Open the moderation queue in the CMS>"},
			},
		})
	}

	return map[string]any{
		// Fallback text for notifications and clients that do not render blocks.
		"text":   "New classified #" + id + " from " + plain(classified.Name),
		"blocks": blocks,
	}
}

// quote renders the submission as a Slack blockquote so a reader cannot make it
// look like part of the CMS's own message.
func quote(message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		text = "_(no message)_"
	}
	text = plain(text)
	if len(text) > maxSectionTextBytes {
		text = text[:maxSectionTextBytes] + "…"
	}
	return ">" + strings.ReplaceAll(text, "\n", "\n>")
}

// plain escapes the three characters Slack treats as markup control characters.
// Everything here is reader-supplied.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func plain(value string) string {
	return slackEscaper.Replace(strings.TrimSpace(value))
}
