package handlers

import (
	"testing"
	"time"

	db "server/internal/database"
)

// The editor renders state and nothing else, and an empty one takes the page
// down rather than degrading, so every view has to carry one.
func TestPollView_AlwaysCarriesState(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour)
	future := time.Now().Add(24 * time.Hour)

	cases := []struct {
		name   string
		poll   db.Poll
		liveID int64
		want   string
	}{
		{
			name:   "live poll",
			poll:   db.Poll{ID: 1, Status: db.PollStatusActive, StartsAt: &past},
			liveID: 1,
			want:   db.PollStateLive,
		},
		{
			name:   "active poll queued behind its start date",
			poll:   db.Poll{ID: 2, Status: db.PollStatusActive, StartsAt: &future},
			liveID: 1,
			want:   db.PollStateScheduled,
		},
		{
			name: "draft",
			poll: db.Poll{ID: 3, Status: db.PollStatusDraft},
			want: db.PollStateDraft,
		},
		{
			name: "closed",
			poll: db.Poll{ID: 4, Status: db.PollStatusClosed},
			want: db.PollStateClosed,
		},
		{
			name:   "active poll past its end date",
			poll:   db.Poll{ID: 5, Status: db.PollStatusActive, StartsAt: &past, EndsAt: &past},
			liveID: 1,
			want:   db.PollStateEnded,
		},
		{
			name:   "nothing live at all",
			poll:   db.Poll{ID: 6, Status: db.PollStatusActive, StartsAt: &past},
			liveID: 0,
			want:   db.PollStateSuperseded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := pollView(tc.poll, tc.liveID)
			if view.State == "" {
				t.Fatal("poll view has no state; the editor cannot render this")
			}
			if view.State != tc.want {
				t.Fatalf("state = %q, want %q", view.State, tc.want)
			}
		})
	}
}

// A poll scheduled for 9am in Philadelphia once went live at 5am, because a
// zoneless timestamp parsed as UTC. Silently guessing a zone is the bug, so
// the parse has to fail instead.
func TestParsePollTime(t *testing.T) {
	ptr := func(s string) *string { return &s }

	t.Run("absent leaves the column unchanged", func(t *testing.T) {
		value, clear, err := parsePollTime(nil)
		if value != nil || clear || err != nil {
			t.Fatalf("got (%v, %v, %v), want (nil, false, nil)", value, clear, err)
		}
	})

	t.Run("empty clears the column", func(t *testing.T) {
		value, clear, err := parsePollTime(ptr("  "))
		if value != nil || !clear || err != nil {
			t.Fatalf("got (%v, %v, %v), want (nil, true, nil)", value, clear, err)
		}
	})

	t.Run("offset is preserved as the instant it names", func(t *testing.T) {
		value, clear, err := parsePollTime(ptr("2026-08-07T09:00:00-04:00"))
		if err != nil || clear || value == nil {
			t.Fatalf("got (%v, %v, %v), want a parsed time", value, clear, err)
		}
		if want := time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC); !value.Equal(want) {
			t.Fatalf("parsed %s, want %s", value.UTC(), want)
		}
	})

	for _, raw := range []string{"2026-08-07T09:00", "2026-08-07T09:00:00", "2026-08-07"} {
		t.Run("zoneless "+raw+" is rejected", func(t *testing.T) {
			if _, _, err := parsePollTime(ptr(raw)); err == nil {
				t.Fatalf("parsePollTime(%q) succeeded; a zoneless timestamp must not be assumed UTC", raw)
			}
		})
	}

	t.Run("nonsense is rejected", func(t *testing.T) {
		if _, _, err := parsePollTime(ptr("next tuesday")); err == nil {
			t.Fatal("parsePollTime accepted a non-timestamp")
		}
	})
}
