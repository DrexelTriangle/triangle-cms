import type { Newsletter } from "./types"

// A 0..1 rate as the old WordPress reports showed it, "47.3%". null means the
// rate is unknown; callers show that as unavailable, never as 0%.
export const formatRate = (rate: number | null): string | null =>
  rate == null ? null : `${(rate * 100).toFixed(1)}%`

// The campaigns that have stats to show: sent ones, newest first.
export const sentCampaigns = (newsletters: Newsletter[]): Newsletter[] =>
  newsletters
    .filter((n) => n.status === "sent")
    .sort((a, b) => (b.sent_at ?? "").localeCompare(a.sent_at ?? "") || b.id - a.id)

export const rateOf = (count: number, of: number): number | null => (of > 0 ? count / of : null)
