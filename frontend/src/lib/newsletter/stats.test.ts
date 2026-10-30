import { describe, expect, it } from "vitest"

import { formatRate, sentCampaigns } from "./stats"
import type { Newsletter } from "./types"

const issue = (id: number, status: Newsletter["status"], sent_at: string | null) =>
  ({ id, status, sent_at, subject: `Issue ${id}` }) as Newsletter

describe("formatRate", () => {
  it("shows one decimal, like the old reports", () => {
    expect(formatRate(0.4733)).toBe("47.3%")
    expect(formatRate(0)).toBe("0.0%")
    expect(formatRate(1)).toBe("100.0%")
  })

  it("has nothing to show for an unknown rate", () => {
    expect(formatRate(null)).toBeNull()
  })
})

describe("sentCampaigns", () => {
  it("keeps sent issues only, newest first", () => {
    const got = sentCampaigns([
      issue(1, "sent", "2026-10-17T18:00:00Z"),
      issue(2, "draft", null),
      issue(3, "sent", "2026-10-24T18:00:00Z"),
      issue(4, "sending", null),
      issue(5, "scheduled", null),
    ])
    expect(got.map((n) => n.id)).toEqual([3, 1])
  })
})
