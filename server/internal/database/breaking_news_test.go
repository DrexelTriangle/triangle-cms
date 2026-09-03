package database

import (
	"testing"
)

func TestNormalizeBreakingNewsWindow(t *testing.T) {
	cases := []struct {
		name  string
		hours int
		want  int
	}{
		{"zero is no limit", 0, breakingNewsWindowUnlimited},
		{"negative is no limit", -3, breakingNewsWindowUnlimited},
		{"in range is kept", 6, 6},
		{"the maximum is kept", maxBreakingNewsWindowHours, maxBreakingNewsWindowHours},
		{"above the maximum is clamped", maxBreakingNewsWindowHours + 1, maxBreakingNewsWindowHours},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeBreakingNewsWindow(tc.hours); got != tc.want {
				t.Errorf("NormalizeBreakingNewsWindow(%d) = %d, want %d", tc.hours, got, tc.want)
			}
		})
	}
}
