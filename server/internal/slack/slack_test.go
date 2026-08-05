package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClassified() Classified {
	return Classified{
		ID:      42,
		Name:    "Dana Reader",
		Email:   "dana@example.edu",
		Label:   "Housing",
		Message: "Subletting a room near campus.",
		EndDate: "2026-09-01",
	}
}

func TestNewClient_RejectsUnusableWebhookURLs(t *testing.T) {
	for name, webhookURL := range map[string]string{
		"empty":      "",
		"blank":      "   ",
		"not a URL":  "hooks.slack.com/services/T000",
		"plain http": "http://hooks.slack.com/services/T000",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(Config{WebhookURL: webhookURL}); err == nil {
				t.Fatalf("expected %q to be rejected", webhookURL)
			}
		})
	}
}

// The button values are the contract with the interactivity handler: it parses
// them straight back into a row id, so anything else silently breaks approvals.
func TestNotifyClassified_SendsActionableButtonsCarryingTheRowID(t *testing.T) {
	var body map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("webhook received invalid JSON: %v", err)
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()

	client, err := NewClient(Config{WebhookURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.NotifyClassified(context.Background(), testClassified()); err != nil {
		t.Fatalf("NotifyClassified: %v", err)
	}

	blocks, _ := body["blocks"].([]any)
	var actions map[string]any
	for _, block := range blocks {
		if b, ok := block.(map[string]any); ok && b["type"] == "actions" {
			actions = b
		}
	}
	if actions == nil {
		t.Fatal("expected an actions block with the Approve/Reject buttons")
	}

	elements, _ := actions["elements"].([]any)
	if len(elements) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(elements))
	}
	wantActionIDs := []string{ActionApprove, ActionReject}
	for i, element := range elements {
		button, _ := element.(map[string]any)
		if button["action_id"] != wantActionIDs[i] {
			t.Errorf("button %d: expected action_id %q, got %v", i, wantActionIDs[i], button["action_id"])
		}
		if button["value"] != "42" {
			t.Errorf("button %d: expected the row id as the value, got %v", i, button["value"])
		}
	}
}

// A revoked webhook or a deleted channel has to surface as an error, not a
// silently swallowed post.
func TestNotifyClassified_ReportsANonOKResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "no_service")
	}))
	defer server.Close()

	client, err := NewClient(Config{WebhookURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = client.NotifyClassified(context.Background(), testClassified())
	if err == nil {
		t.Fatal("expected an error for a non-200 webhook response")
	}
	// Slack's short body is the only diagnostic there is, so it must survive
	// into the log line.
	if !strings.Contains(err.Error(), "no_service") {
		t.Fatalf("expected Slack's response body in the error, got %q", err)
	}
}

// Reader-supplied text must not be able to forge links or mimic the CMS's own
// formatting in the channel.
func TestQuote_EscapesMarkupAndBlockquotesEveryLine(t *testing.T) {
	got := quote("<https://evil.example|click me>\nsecond & line")

	if strings.Contains(got, "<https://evil.example") {
		t.Fatalf("expected the link markup to be escaped, got %q", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Fatalf("expected & to be escaped, got %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, ">") {
			t.Fatalf("expected every line to be quoted, got %q", got)
		}
	}
}

// Slack rejects the whole message when a section runs past its limit, which
// would lose the notification entirely rather than just the tail of the text.
func TestQuote_TruncatesOverlongMessages(t *testing.T) {
	got := quote(strings.Repeat("a", maxSectionTextBytes*2))

	if len(got) > maxSectionTextBytes+len("…")+len(">") {
		t.Fatalf("expected the message to be truncated, got %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("expected a truncated message to be marked with an ellipsis")
	}
}
