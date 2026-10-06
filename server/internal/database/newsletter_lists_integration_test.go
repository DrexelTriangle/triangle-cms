package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"server/internal/models"
)

func boolPtr(v bool) *bool      { return &v }
func strPtr(v string) *string   { return &v }
func idsPtr(v []int64) *[]int64 { return &v }

// tokenFor makes a distinct, correctly sized token for raw-SQL fixtures.
func tokenFor(i int) string { return fmt.Sprintf("%043d", i) }

func TestCreateNewsletterList_DuplicateName(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	first, err := CreateNewsletterList(ctx, conn, "Alumni", "Graduates", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.ID < 1000 || first.Name != "Alumni" || first.Description != "Graduates" || !first.IsPublic {
		t.Fatalf("created list = %+v", first)
	}
	if _, err := CreateNewsletterList(ctx, conn, "Alumni", "", false); !errors.Is(err, ErrNewsletterDuplicate) {
		t.Fatalf("duplicate name error = %v, want ErrNewsletterDuplicate", err)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_lists"); n != 1 {
		t.Fatalf("lists = %d, want 1", n)
	}
}

func TestListNewsletterLists_SubscribedCountExcludesUnsubscribed(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	list, err := CreateNewsletterList(ctx, conn, "Students", "", true)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := CreateNewsletterList(ctx, conn, "Parents", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"subscribed", "subscribed", "unsubscribed"} {
		mustExec(t, conn, `INSERT INTO newsletter_subscribers (id, email, status, token, source, subscribed_at)
			VALUES (?, ?, ?, ?, 'admin', UTC_TIMESTAMP())`, 100+i, string(rune('a'+i))+"@example.com", status, tokenFor(i))
		mustExec(t, conn, "INSERT INTO newsletter_subscriber_lists (subscriber_id, list_id) VALUES (?, ?)", 100+i, list.ID)
	}

	lists, err := ListNewsletterLists(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int64]int{}
	for _, l := range lists {
		counts[l.ID] = l.SubscribedCount
	}
	if counts[list.ID] != 2 {
		t.Errorf("Students subscribed count = %d, want 2 (unsubscribed member excluded)", counts[list.ID])
	}
	if c, ok := counts[empty.ID]; !ok || c != 0 {
		t.Errorf("Parents count = %d (present %v), want 0 and present", c, ok)
	}
}

func TestCountExistingNewsletterLists_PublicOnly(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _ := CreateNewsletterList(ctx, conn, "A", "", true)
	b, _ := CreateNewsletterList(ctx, conn, "B", "", false)

	cases := []struct {
		ids        []int64
		publicOnly bool
		want       int
	}{
		{[]int64{a.ID, b.ID}, true, 1},
		{[]int64{a.ID, b.ID}, false, 2},
		{[]int64{a.ID, 999999}, false, 1},
		{nil, false, 0},
	}
	for _, tc := range cases {
		got, err := CountExistingNewsletterLists(ctx, conn, tc.ids, tc.publicOnly)
		if err != nil {
			t.Fatalf("%v: %v", tc.ids, err)
		}
		if got != tc.want {
			t.Errorf("CountExistingNewsletterLists(%v, public=%v) = %d, want %d", tc.ids, tc.publicOnly, got, tc.want)
		}
	}
}

func TestUpdateNewsletterList_Partial(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	list, _ := CreateNewsletterList(ctx, conn, "Faculty", "Drexel faculty", true)

	updated, err := UpdateNewsletterList(ctx, conn, list.ID, models.NewsletterListPatchRequest{IsPublic: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsPublic || updated.Name != "Faculty" || updated.Description != "Drexel faculty" {
		t.Fatalf("partial update changed more than is_public: %+v", updated)
	}

	other, _ := CreateNewsletterList(ctx, conn, "Other", "", true)
	if _, err := UpdateNewsletterList(ctx, conn, other.ID, models.NewsletterListPatchRequest{Name: strPtr("Faculty")}); !errors.Is(err, ErrNewsletterDuplicate) {
		t.Fatalf("rename onto existing name: err = %v, want ErrNewsletterDuplicate", err)
	}
	if _, err := UpdateNewsletterList(ctx, conn, 424242, models.NewsletterListPatchRequest{Name: strPtr("X")}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing list: err = %v, want sql.ErrNoRows", err)
	}
}
