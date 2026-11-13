import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createMockNewsletterApi } from "./mockStore"
import type { Newsletter } from "./types"

const stored = (id: number, overrides: Partial<Newsletter>): Newsletter => ({
  id,
  subject: `Issue ${id}`,
  preheader: "",
  status: "sent",
  sponsor: null,
  body_html: "",
  email_html: `<a href="https://www.thetriangle.org/a">A</a><a href="https://x.com/thetriangle">X</a><a href="{{unsubscribe_url}}">U</a>`,
  scheduled_for: null,
  sent_at: "2026-10-31T18:00:00Z",
  recipient_count: 590,
  open_count: 280,
  click_count: 60,
  created_at: "2026-10-30T00:00:00Z",
  updated_at: "2026-10-31T18:00:00Z",
  ...overrides,
})

// Under recent Node the global localStorage is Node's own and absent, so the
// store gets a fresh in-memory one per test.
let storage: Map<string, string>
const seed = (newsletters: Newsletter[]) =>
  storage.set("delta.newsletters.v1", JSON.stringify({ nextId: 10, newsletters }))

describe("mock campaignStats", () => {
  beforeEach(() => {
    storage = new Map()
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => storage.get(k) ?? null,
      setItem: (k: string, v: string) => storage.set(k, v),
    })
  })
  afterEach(() => vi.unstubAllGlobals())

  it("reports a recent campaign in detail and compares it with earlier ones", async () => {
    seed([
      stored(1, { sent_at: "2026-10-17T18:00:00Z" }),
      stored(2, {}),
      stored(3, { status: "draft", sent_at: null }),
    ])
    const s = await createMockNewsletterApi().campaignStats(2)
    expect(s.detailed_tracking).toBe(true)
    expect(s.recipients.sent).toBe(590)
    expect(s.unique_opens).toBe(280)
    expect(s.unsubscribes).not.toBeNull()
    expect(s.links.map((l) => l.url)).toEqual(["https://www.thetriangle.org/a", "https://x.com/thetriangle"])
    expect(s.links.every((l) => l.unique_clickers !== null)).toBe(true)
    expect(s.recent.map((r) => r.id)).toEqual([2, 1])
    expect(s.recent[1].detailed_tracking).toBe(false)
    expect(s.recent[1].unsubscribes).toBeNull()
  })

  it("leaves an older campaign's unknowns null", async () => {
    seed([stored(1, { sent_at: "2026-10-17T18:00:00Z" })])
    const s = await createMockNewsletterApi().campaignStats(1)
    expect(s.detailed_tracking).toBe(false)
    expect(s.unsubscribes).toBeNull()
    expect(s.links.every((l) => l.unique_clickers === null)).toBe(true)
  })

  it("refuses an unsent campaign", async () => {
    seed([stored(3, { status: "draft", sent_at: null })])
    await expect(createMockNewsletterApi().campaignStats(3)).rejects.toThrow()
  })
})
