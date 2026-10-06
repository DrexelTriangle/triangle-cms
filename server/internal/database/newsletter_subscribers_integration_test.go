package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"sync"
	"testing"

	"server/internal/models"
)

var tokenShape = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func tokenOf(t *testing.T, email string, conn *sql.DB) string {
	t.Helper()
	var token string
	if err := conn.QueryRow("SELECT token FROM newsletter_subscribers WHERE email = ?", email).Scan(&token); err != nil {
		t.Fatalf("read token for %s: %v", email, err)
	}
	return token
}

func subscriberIDOf(t *testing.T, conn *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow("SELECT id FROM newsletter_subscribers WHERE email = ?", email).Scan(&id); err != nil {
		t.Fatalf("read id for %s: %v", email, err)
	}
	return id
}

func membershipsOf(t *testing.T, conn *sql.DB, email string) []int64 {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `
		SELECT sl.list_id FROM newsletter_subscriber_lists sl
		JOIN newsletter_subscribers s ON s.id = sl.subscriber_id
		WHERE s.email = ? ORDER BY sl.list_id`, email)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]int64(nil), a...)
	b = append([]int64(nil), b...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func threeLists(t *testing.T, conn *sql.DB) (a, b, c int64) {
	t.Helper()
	ctx := context.Background()
	la, err := CreateNewsletterList(ctx, conn, "A", "", true)
	if err != nil {
		t.Fatal(err)
	}
	lb, _ := CreateNewsletterList(ctx, conn, "B", "", true)
	lc, _ := CreateNewsletterList(ctx, conn, "C", "", true)
	return la.ID, lb.ID, lc.ID
}

func TestSubscribePublic_NewAddress(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, _ := threeLists(t, conn)

	outcome, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "new@example.com", Name: "New", ListIDs: []int64{a, b}, IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SubscribeCreated {
		t.Errorf("outcome = %q, want %q", outcome, SubscribeCreated)
	}
	var status, source, ip, name string
	if err := conn.QueryRow("SELECT status, source, signup_ip, name FROM newsletter_subscribers WHERE email = 'new@example.com'").Scan(&status, &source, &ip, &name); err != nil {
		t.Fatal(err)
	}
	if status != "subscribed" || source != "public_form" || ip != "203.0.113.9" || name != "New" {
		t.Errorf("row = status %q source %q ip %q name %q", status, source, ip, name)
	}
	if tok := tokenOf(t, "new@example.com", conn); !tokenShape.MatchString(tok) {
		t.Errorf("token %q is not 43 base64url chars", tok)
	}
	if got := membershipsOf(t, conn, "new@example.com"); !equalIDs(got, []int64{a, b}) {
		t.Errorf("memberships = %v, want %v", got, []int64{a, b})
	}
}

func TestSubscribePublic_AlreadySubscribedUnionsLists(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, _ := threeLists(t, conn)
	if _, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "x@example.com", ListIDs: []int64{a}}); err != nil {
		t.Fatal(err)
	}
	before := tokenOf(t, "x@example.com", conn)

	outcome, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "x@example.com", ListIDs: []int64{b}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SubscribeUpdated {
		t.Errorf("outcome = %q, want %q", outcome, SubscribeUpdated)
	}
	if got := membershipsOf(t, conn, "x@example.com"); !equalIDs(got, []int64{a, b}) {
		t.Errorf("memberships = %v, want union %v", got, []int64{a, b})
	}
	if after := tokenOf(t, "x@example.com", conn); after != before {
		t.Error("token changed on a repeat subscribe")
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestSubscribePublic_ResubscribeReplacesListsKeepsToken(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, c := threeLists(t, conn)
	if _, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "r@example.com", Name: "Old", ListIDs: []int64{a, b}}); err != nil {
		t.Fatal(err)
	}
	original := tokenOf(t, "r@example.com", conn)
	id := subscriberIDOf(t, conn, "r@example.com")
	if _, err := UpdateNewsletterSubscriber(ctx, conn, id, models.NewsletterSubscriberPatchRequest{Status: strPtr("unsubscribed")}); err != nil {
		t.Fatal(err)
	}

	outcome, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "r@example.com", Name: "New", ListIDs: []int64{c}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != SubscribeResubscribed {
		t.Errorf("outcome = %q, want %q", outcome, SubscribeResubscribed)
	}
	got, err := GetNewsletterSubscriber(ctx, conn, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "subscribed" || got.Name != "New" || got.UnsubscribedAt != nil {
		t.Errorf("after resubscribe: %+v", got)
	}
	if !equalIDs(got.ListIDs, []int64{c}) {
		t.Errorf("lists = %v, want only %v (stale memberships must not come back)", got.ListIDs, []int64{c})
	}
	if tok := tokenOf(t, "r@example.com", conn); tok != original {
		t.Error("token changed on resubscribe; old unsubscribe links would break")
	}
}

func TestUnsubscribe_KeepsRowTokenAndMemberships(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, _ := threeLists(t, conn)
	if _, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "u@example.com", ListIDs: []int64{a, b}}); err != nil {
		t.Fatal(err)
	}
	token := tokenOf(t, "u@example.com", conn)
	id := subscriberIDOf(t, conn, "u@example.com")

	updated, err := UpdateNewsletterSubscriber(ctx, conn, id, models.NewsletterSubscriberPatchRequest{Status: strPtr("unsubscribed")})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "unsubscribed" || updated.UnsubscribedAt == nil {
		t.Errorf("unsubscribe result = %+v", updated)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = 'u@example.com'"); n != 1 {
		t.Fatalf("row count after unsubscribe = %d, want 1", n)
	}
	if tok := tokenOf(t, "u@example.com", conn); tok != token {
		t.Error("token changed on unsubscribe")
	}
	if got := membershipsOf(t, conn, "u@example.com"); !equalIDs(got, []int64{a, b}) {
		t.Errorf("memberships = %v, want kept %v", got, []int64{a, b})
	}
}

func TestSubscribePublic_CaseInsensitiveEmail(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)
	mustExec(t, conn, `INSERT INTO newsletter_subscribers (email, token, source, subscribed_at) VALUES ('Foo@Example.com', ?, 'admin', UTC_TIMESTAMP())`, tokenFor(1))

	outcome, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "foo@example.com", ListIDs: []int64{a}})
	if err != nil {
		t.Fatalf("subscribe with different case: %v", err)
	}
	if outcome != SubscribeUpdated {
		t.Errorf("outcome = %q, want %q (same address, different case)", outcome, SubscribeUpdated)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestSubscribePublic_ConcurrentSameEmail(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := SubscribePublic(ctx, conn, PublicSubscription{Email: "race@example.com", ListIDs: []int64{a}}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent subscribe failed: %v", err)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscribers WHERE email = 'race@example.com'"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestCreateNewsletterSubscriber_Duplicate(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)
	created, err := CreateNewsletterSubscriber(ctx, conn, "admin-added@example.com", "Ann", []int64{a})
	if err != nil {
		t.Fatal(err)
	}
	if created.Source != "admin" || created.Status != "subscribed" || !equalIDs(created.ListIDs, []int64{a}) {
		t.Errorf("created = %+v", created)
	}
	if _, err := CreateNewsletterSubscriber(ctx, conn, "admin-added@example.com", "", nil); !errors.Is(err, ErrNewsletterDuplicate) {
		t.Fatalf("duplicate err = %v, want ErrNewsletterDuplicate", err)
	}
}

func TestListSubscribers_Filters(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, _ := threeLists(t, conn)
	mustSub := func(email, name string, lists []int64) int64 {
		s, err := CreateNewsletterSubscriber(ctx, conn, email, name, lists)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	mustSub("ann@example.com", "Ann Lee", []int64{a})
	bob := mustSub("bob@example.com", "Bob", []int64{b})
	mustSub("cat@example.com", "Cat", []int64{a, b})
	if _, err := UpdateNewsletterSubscriber(ctx, conn, bob, models.NewsletterSubscriberPatchRequest{Status: strPtr("unsubscribed")}); err != nil {
		t.Fatal(err)
	}

	emails := func(f SubscriberFilter) ([]string, int) {
		subs, total, err := ListNewsletterSubscribers(ctx, conn, f, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, s := range subs {
			out = append(out, s.Email)
		}
		sort.Strings(out)
		return out, total
	}
	if got, total := emails(SubscriberFilter{Status: "subscribed"}); total != 2 || len(got) != 2 || got[0] != "ann@example.com" || got[1] != "cat@example.com" {
		t.Errorf("status filter = %v (total %d)", got, total)
	}
	if got, total := emails(SubscriberFilter{ListID: b}); total != 2 || got[0] != "bob@example.com" || got[1] != "cat@example.com" {
		t.Errorf("list filter = %v (total %d)", got, total)
	}
	if got, total := emails(SubscriberFilter{Query: "lee"}); total != 1 || got[0] != "ann@example.com" {
		t.Errorf("name query = %v (total %d)", got, total)
	}
	if got, total := emails(SubscriberFilter{Query: "bob@"}); total != 1 || got[0] != "bob@example.com" {
		t.Errorf("email query = %v (total %d)", got, total)
	}
	subs, total, err := ListNewsletterSubscribers(ctx, conn, SubscriberFilter{}, 2, 0)
	if err != nil || total != 3 || len(subs) != 2 {
		t.Errorf("paged: len %d total %d err %v, want 2 of 3", len(subs), total, err)
	}

	counts, err := CountNewsletterSubscribersByStatus(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if counts["subscribed"] != 2 || counts["unsubscribed"] != 1 || counts["all"] != 3 {
		t.Errorf("counts = %v", counts)
	}
}

func TestCountNewsletterSubscribersByStatus_EmptyHasKeys(t *testing.T) {
	conn := newsletterTestDB(t)
	counts, err := CountNewsletterSubscribersByStatus(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"subscribed", "unsubscribed", "all"} {
		if v, ok := counts[k]; !ok || v != 0 {
			t.Errorf("counts[%q] = %d (present %v), want 0 and present", k, v, ok)
		}
	}
}

func TestListSubscribers_QueryEscapesWildcards(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	for _, e := range []string{"a_b@x.io", "axb@x.io", "pct%@x.io", "pctz@x.io"} {
		if _, err := CreateNewsletterSubscriber(ctx, conn, e, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]string{"a_b": "a_b@x.io", "pct%": "pct%@x.io"} {
		subs, total, err := ListNewsletterSubscribers(ctx, conn, SubscriberFilter{Query: q}, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(subs) != 1 || subs[0].Email != want {
			t.Errorf("q=%q matched %d rows (%v), want only %s", q, total, subs, want)
		}
	}
}

func TestUpdateNewsletterSubscriber_ReplacesLists(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, b, c := threeLists(t, conn)
	s, _ := CreateNewsletterSubscriber(ctx, conn, "l@example.com", "", []int64{a, b})
	updated, err := UpdateNewsletterSubscriber(ctx, conn, s.ID, models.NewsletterSubscriberPatchRequest{ListIDs: idsPtr([]int64{c})})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(updated.ListIDs, []int64{c}) || !equalIDs(membershipsOf(t, conn, "l@example.com"), []int64{c}) {
		t.Errorf("lists = %v, want %v", updated.ListIDs, []int64{c})
	}
}

func TestDeleteNewsletterSubscriber_RemovesMemberships(t *testing.T) {
	conn := newsletterTestDB(t)
	ctx := context.Background()
	a, _, _ := threeLists(t, conn)
	s, _ := CreateNewsletterSubscriber(ctx, conn, "gone@example.com", "", []int64{a})

	deleted, err := DeleteNewsletterSubscriber(ctx, conn, s.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if n := mustCount(t, conn, "SELECT COUNT(*) FROM newsletter_subscriber_lists WHERE subscriber_id = ?", s.ID); n != 0 {
		t.Errorf("memberships left = %d", n)
	}
	if again, err := DeleteNewsletterSubscriber(ctx, conn, s.ID); err != nil || again {
		t.Errorf("second delete = %v, %v; want false, nil", again, err)
	}
}
