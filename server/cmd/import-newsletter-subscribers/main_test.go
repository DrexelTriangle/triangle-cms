package main

import (
	"reflect"
	"testing"
	"time"
)

func TestMapLegacyRows(t *testing.T) {
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	raw := []wpRow{
		{id: 1, email: " a@drexel.edu ", name: "Ada", surname: "Lovelace", token: "abc", status: "C", created: created,
			lists: [wpListCount]bool{true, false, false, false, true}},
		{id: 2, email: "b@gmail.com", status: "U", created: created, updated: 1700000000, unsubTime: 1710000000},
		{id: 3, email: "c@gmail.com", status: "U", created: created, updated: 1700000000},
		{id: 4, email: "d@gmail.com", status: "S", created: created},
		{id: 5, email: "not-an-address", status: "C", created: created},
		{id: 6, email: "A@Drexel.edu", status: "C", created: created},
		{id: 7, email: "e@yahoo.com", status: "b", created: created},
	}

	subs, skipped := mapLegacyRows(raw)

	if len(subs) != 4 {
		t.Fatalf("expected 4 subscribers, got %d: %+v", len(subs), subs)
	}
	ada := subs[0]
	if ada.Email != "a@drexel.edu" || ada.Name != "Ada Lovelace" || ada.Token != "abc" || ada.Status != "confirmed" ||
		!reflect.DeepEqual(ada.Lists, []int{1, 5}) || ada.UnsubscribedAt != nil || !ada.SubscribedAt.Equal(created) {
		t.Fatalf("confirmed row mapped wrong: %+v", ada)
	}
	if got := subs[1]; got.Status != "unsubscribed" || got.UnsubscribedAt == nil || got.UnsubscribedAt.Unix() != 1710000000 {
		t.Fatalf("unsub_time should win over updated: %+v", got)
	}
	if got := subs[2]; got.UnsubscribedAt == nil || got.UnsubscribedAt.Unix() != 1700000000 {
		t.Fatalf("updated should stand in for a missing unsub_time: %+v", got)
	}
	if got := subs[3]; got.Status != "bounced" {
		t.Fatalf("lowercase status should still map: %+v", got)
	}

	want := map[string][]int64{
		"status S, never confirmed": {4},
		"invalid address":           {5},
		"duplicate address":         {6},
	}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
}
