package handlers

import (
	"testing"
	"time"

	db "server/internal/database"
)

// The editor renders state and nothing else -- an empty one takes the page down
// rather than degrading -- so every view has to carry one.
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
