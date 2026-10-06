package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"server/internal/models"
)

const sampleBlocks = `{"version":1,"blocks":[{"type":"heading","text":"More From News"}]}`

func newCampaign(t *testing.T, conn *sql.DB, subject string, lists []int64) models.NewsletterCampaign {
	t.Helper()
	c, err := CreateNewsletterCampaign(context.Background(), conn, models.NewsletterCampaignCreateRequest{
		Subject:    subject,
		BodyBlocks: json.RawMessage(sampleBlocks),
		ListIDs:    lists,
	}, "editor@drexel.edu")
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	return c
}

func TestCreateNewsletterCampaign_AlwaysDraft(t *testing.T) {
	conn := newsletterTestDB(t)
	a, b, _ := threeLists(t, conn)
	c := newCampaign(t, conn, "Weekly digest", []int64{a, b})
	if c.Status != "draft" {
		t.Errorf("status = %q, want draft", c.Status)
	}
	if !equalIDs(c.ListIDs, []int64{a, b}) {
		t.Errorf("list ids = %v", c.ListIDs)
	}
	if c.CreatedBy != "editor@drexel.edu" || c.UpdatedBy != "editor@drexel.edu" {
		t.Errorf("actors = %q/%q", c.CreatedBy, c.UpdatedBy)
	}
	if c.RecipientCount != nil || c.SentAt != nil {
		t.Errorf("a new draft must not carry send data: %+v", c)
	}
}

func TestUpdateNewsletterCampaign_EditsDraft(t *testing.T) {
	conn := newsletterTestDB(t)
	a, b, _ := threeLists(t, conn)
	c := newCampaign(t, conn, "Before", []int64{a})
	updated, err := UpdateNewsletterCampaign(context.Background(), conn, c.ID, models.NewsletterCampaignPatchRequest{
		Subject: strPtr("After"),
		ListIDs: idsPtr([]int64{b}),
	}, "other@drexel.edu")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Subject != "After" || !equalIDs(updated.ListIDs, []int64{b}) || updated.UpdatedBy != "other@drexel.edu" || updated.CreatedBy != "editor@drexel.edu" {
		t.Errorf("updated = %+v", updated)
	}
	var body map[string]any
	if err := json.Unmarshal(updated.BodyBlocks, &body); err != nil || body["version"] != float64(1) {
		t.Errorf("body not kept when omitted from patch: %s (%v)", updated.BodyBlocks, err)
	}
}

func TestUpdateNewsletterCampaign_SentIsImmutable(t *testing.T) {
	conn := newsletterTestDB(t)
	a, _, _ := threeLists(t, conn)
	c := newCampaign(t, conn, "Original", []int64{a})
	mustExec(t, conn, "UPDATE newsletter_campaigns SET status = 'sent', sent_at = UTC_TIMESTAMP(), recipient_count = 10 WHERE id = ?", c.ID)

	_, err := UpdateNewsletterCampaign(context.Background(), conn, c.ID, models.NewsletterCampaignPatchRequest{Subject: strPtr("Rewritten")}, "x")
	if !errors.Is(err, ErrCampaignSent) {
		t.Fatalf("err = %v, want ErrCampaignSent", err)
	}
	got, err := GetNewsletterCampaign(context.Background(), conn, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "Original" {
		t.Errorf("subject = %q; a sent campaign was modified", got.Subject)
	}
	if got.RecipientCount == nil || *got.RecipientCount != 10 {
		t.Errorf("recipient count = %v, want 10", got.RecipientCount)
	}
}

func TestDeleteNewsletterCampaign_OnlyDrafts(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)
	draft := newCampaign(t, conn, "Draft", []int64{a})
	deleted, err := DeleteNewsletterCampaign(ctx, conn, draft.ID)
	if err != nil || !deleted {
		t.Fatalf("delete draft = %v, %v", deleted, err)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_campaign_lists WHERE campaign_id = ?", draft.ID); n != 0 {
		t.Errorf("targets left = %d", n)
	}

	sent := newCampaign(t, conn, "Sent", []int64{a})
	mustExec(t, conn, "UPDATE newsletter_campaigns SET status = 'sent' WHERE id = ?", sent.ID)
	if _, err := DeleteNewsletterCampaign(ctx, conn, sent.ID); !errors.Is(err, ErrCampaignNotDraft) {
		t.Fatalf("delete sent: err = %v, want ErrCampaignNotDraft", err)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_campaigns WHERE id = ?", sent.ID); n != 1 {
		t.Error("sent campaign was deleted")
	}
	if missing, err := DeleteNewsletterCampaign(ctx, conn, 999999); err != nil || missing {
		t.Errorf("delete missing = %v, %v; want false, nil", missing, err)
	}
}

// s1 in A+B, s2 in A, s3 in B but unsubscribed, s4 in no list: a campaign to
// A+B reaches s1 once and s2.
func seedRecipients(t *testing.T, conn *sql.DB) (a, b int64) {
	t.Helper()
	ctx := context.Background()
	a, b, _ = threeLists(t, conn)
	mk := func(email string, lists []int64) int64 {
		s, err := CreateNewsletterSubscriber(ctx, conn, email, "", lists)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	mk("s1@example.com", []int64{a, b})
	mk("s2@example.com", []int64{a})
	s3 := mk("s3@example.com", []int64{b})
	mk("s4@example.com", nil)
	if _, err := UpdateNewsletterSubscriber(ctx, conn, s3, models.NewsletterSubscriberPatchRequest{Status: strPtr("unsubscribed")}); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestRecipientCount_DistinctAcrossListsExcludesUnsubscribed(t *testing.T) {
	conn := newsletterTestDB(t)
	a, b := seedRecipients(t, conn)
	c := newCampaign(t, conn, "To A and B", []int64{a, b})
	n, err := NewsletterRecipientCount(context.Background(), conn, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("recipients = %d, want 2 (s1 once, s2; not s3 unsubscribed, not s4 unlisted)", n)
	}
}

func TestRecipientCount_IgnoresListPublicFlag(t *testing.T) {
	conn := newsletterTestDB(t)
	a, b := seedRecipients(t, conn)
	c := newCampaign(t, conn, "To A and B", []int64{a, b})
	if _, err := UpdateNewsletterList(context.Background(), conn, a, models.NewsletterListPatchRequest{IsPublic: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	n, err := NewsletterRecipientCount(context.Background(), conn, c.ID)
	if err != nil || n != 2 {
		t.Errorf("recipients after hiding list A = %d (%v), want 2", n, err)
	}
}

func TestGetNewsletterStats(t *testing.T) {
	conn := newsletterTestDB(t)
	a, b := seedRecipients(t, conn)
	newCampaign(t, conn, "Draft 1", []int64{a})
	sent := newCampaign(t, conn, "Sent 1", []int64{b})
	mustExec(t, conn, "UPDATE newsletter_campaigns SET status = 'sent' WHERE id = ?", sent.ID)

	stats, err := GetNewsletterStats(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Subscribers["subscribed"] != 3 || stats.Subscribers["unsubscribed"] != 1 {
		t.Errorf("subscriber counts = %v", stats.Subscribers)
	}
	if stats.Campaigns["draft"] != 1 || stats.Campaigns["sent"] != 1 || stats.Campaigns["scheduled"] != 0 || stats.Campaigns["all"] != 2 {
		t.Errorf("campaign counts = %v", stats.Campaigns)
	}
	per := map[int64]int{}
	for _, l := range stats.Lists {
		per[l.ID] = l.SubscribedCount
	}
	if per[a] != 2 || per[b] != 1 {
		t.Errorf("per-list subscribed = %v, want A=2 B=1", per)
	}
}

func TestListNewsletterCampaigns_OmitsBody(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)
	c := newCampaign(t, conn, "Has body", []int64{a})
	newCampaign(t, conn, "Other", nil)

	items, total, err := ListNewsletterCampaigns(ctx, conn, "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("list = %d items, total %d", len(items), total)
	}
	for _, it := range items {
		if it.BodyBlocks != nil {
			t.Errorf("list row %d carries a body", it.ID)
		}
	}
	filtered, total, err := ListNewsletterCampaigns(ctx, conn, "draft", "has", 50, 0)
	if err != nil || total != 1 || filtered[0].ID != c.ID || !equalIDs(filtered[0].ListIDs, []int64{a}) {
		t.Errorf("filtered = %+v total %d err %v", filtered, total, err)
	}

	got, err := GetNewsletterCampaign(ctx, conn, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var want, have any
	_ = json.Unmarshal([]byte(sampleBlocks), &want)
	if err := json.Unmarshal(got.BodyBlocks, &have); err != nil {
		t.Fatal(err)
	}
	if wb, _ := json.Marshal(want); string(wb) != mustMarshal(t, have) {
		t.Errorf("body = %s, want %s", got.BodyBlocks, sampleBlocks)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
