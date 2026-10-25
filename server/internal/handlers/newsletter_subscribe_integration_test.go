package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"server/internal/models"
)

func subscribe(t *testing.T, h http.Handler, body any) (int, models.NewsletterSubscribeResponse) {
	t.Helper()
	rec := serveNewsletter(t, h, http.MethodPost, "/v1/newsletter-subscribers/subscribe", body, nil)
	var got models.NewsletterSubscribeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode subscribe response %q: %v", rec.Body.String(), err)
	}
	return rec.Code, got
}

func TestNewsletterHTTP_Subscribe(t *testing.T) {
	conn := newsletterTestDB(t)
	h := PostNewsletterSubscribe(conn)

	lists := func(email string) []int {
		t.Helper()
		rows, err := conn.Query(`
			SELECT l.list_id FROM newsletter_subscriber_lists l JOIN newsletter_subscribers s ON s.id = l.subscriber_id
			WHERE s.email = ? ORDER BY l.list_id`, email)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	statusOf := func(email string) (status string, count int) {
		t.Helper()
		if err := conn.QueryRow("SELECT COALESCE(MAX(status), ''), COUNT(*) FROM newsletter_subscribers WHERE email = ?", email).Scan(&status, &count); err != nil {
			t.Fatal(err)
		}
		return status, count
	}

	if code, got := subscribe(t, h, map[string]any{"email": " reader@drexel.edu ", "lists": []int{1, 2, 2}}); code != http.StatusOK || !got.Success {
		t.Fatalf("new address: %d %+v", code, got)
	}
	if status, n := statusOf("reader@drexel.edu"); status != models.SubscriberStatusConfirmed || n != 1 {
		t.Fatalf("expected one confirmed row, got %q x%d", status, n)
	}
	if got := lists("reader@drexel.edu"); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("expected lists [1 2], got %v", got)
	}

	// The same reader again, typed differently, with another list: no second
	// row, and the new list joins the old ones.
	if code, got := subscribe(t, h, map[string]any{"email": "Reader@Drexel.edu", "lists": []int{5}}); code != http.StatusOK || !got.Success {
		t.Fatalf("repeat: %d %+v", code, got)
	}
	if _, n := statusOf("reader@drexel.edu"); n != 1 {
		t.Fatalf("a repeat subscribe made %d rows", n)
	}
	if got := lists("reader@drexel.edu"); len(got) != 3 {
		t.Fatalf("expected lists [1 2 5], got %v", got)
	}

	// An unsubscribe stands: the form answers as always but changes nothing.
	if _, err := conn.Exec("UPDATE newsletter_subscribers SET status = ? WHERE email = ?", models.SubscriberStatusUnsubscribed, "reader@drexel.edu"); err != nil {
		t.Fatal(err)
	}
	if code, got := subscribe(t, h, map[string]any{"email": "reader@drexel.edu", "lists": []int{}}); code != http.StatusOK || !got.Success {
		t.Fatalf("unsubscribed address: %d %+v", code, got)
	}
	if status, _ := statusOf("reader@drexel.edu"); status != models.SubscriberStatusUnsubscribed {
		t.Fatalf("the form resubscribed someone who had unsubscribed: %q", status)
	}

	// A bot filling the honeypot is told it worked and stores nothing.
	if code, got := subscribe(t, h, map[string]any{"email": "bot@example.org", "website": "http://spam.example"}); code != http.StatusOK || !got.Success {
		t.Fatalf("honeypot: %d %+v", code, got)
	}
	if _, n := statusOf("bot@example.org"); n != 0 {
		t.Fatal("the honeypot submission was stored")
	}

	for name, body := range map[string]any{
		"not an address": map[string]any{"email": "nope"},
		"no domain dot":  map[string]any{"email": "a@localhost"},
		"display name":   map[string]any{"email": "Ada <ada@example.org>"},
		"unknown list":   map[string]any{"email": "ada@example.org", "lists": []int{8}},
		"not JSON":       "email=ada@example.org",
	} {
		if code, got := subscribe(t, h, body); code != http.StatusBadRequest || got.Success || got.Error == "" {
			t.Errorf("%s: expected 400 with a message, got %d %+v", name, code, got)
		}
	}
	if _, n := statusOf("ada@example.org"); n != 0 {
		t.Fatal("a refused submission was stored")
	}

	// The honeypot answers before validation, so a bot can't tell good input
	// from bad.
	if code, got := subscribe(t, h, map[string]any{"email": "nope", "website": "x"}); code != http.StatusOK || !got.Success {
		t.Fatalf("honeypot with a bad address: %d %+v", code, got)
	}

	// Simultaneous first submits of one address: one row, every list kept.
	var wg sync.WaitGroup
	for list := 1; list <= 7; list++ {
		wg.Go(func() {
			if code, got := subscribe(t, h, map[string]any{"email": "race@example.org", "lists": []int{list}}); code != http.StatusOK || !got.Success {
				t.Errorf("concurrent subscribe: %d %+v", code, got)
			}
		})
	}
	wg.Wait()
	if _, n := statusOf("race@example.org"); n != 1 {
		t.Fatalf("concurrent subscribes made %d rows", n)
	}
	if got := lists("race@example.org"); len(got) != 7 {
		t.Fatalf("concurrent subscribes lost lists: kept %v", got)
	}
}
