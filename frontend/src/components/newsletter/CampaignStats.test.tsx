import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import CampaignStats from "./CampaignStats"
import type {
  Newsletter,
  NewsletterApi,
  NewsletterCampaignStats,
  NewsletterCampaignSummary,
} from "../../lib/newsletter/types"

const issue = (id: number, status: Newsletter["status"], sent_at: string | null, subject = `Issue ${id}`) =>
  ({ id, status, sent_at, subject }) as Newsletter

const summary = (id: number, overrides: Partial<NewsletterCampaignSummary> = {}): NewsletterCampaignSummary => ({
  id,
  subject: `Issue ${id}`,
  status: "sent",
  sent_at: "2026-10-31T18:00:00Z",
  detailed_tracking: true,
  recipients: { queued: 600, sent: 596, failed: 3, skipped: 1, in_flight: 0 },
  unique_opens: 282,
  unique_clickers: 61,
  total_clicks: 97,
  unsubscribes: 2,
  open_rate: 282 / 596,
  click_rate: 61 / 596,
  click_to_open_rate: 61 / 282,
  unsubscribe_rate: 2 / 596,
  ...overrides,
})

const detailed = (id: number, overrides: Partial<NewsletterCampaignStats> = {}): NewsletterCampaignStats => ({
  ...summary(id),
  links: [
    { id: 1, url: "https://x.com/thetriangle", total_clicks: 4, unique_clickers: 3 },
    { id: 2, url: "https://www.thetriangle.org/news/big-story", total_clicks: 40, unique_clickers: 31 },
  ],
  errors: [{ message: "gmail: 400 Invalid To header", count: 3 }],
  recent: [summary(id), summary(id + 100, { subject: "Older issue", sent_at: "2026-10-24T18:00:00Z" })],
  ...overrides,
})

const partial = (id: number): NewsletterCampaignStats => ({
  ...summary(id, { detailed_tracking: false, unsubscribes: null, unsubscribe_rate: null, subject: "Old issue" }),
  links: [{ id: 9, url: "https://www.thetriangle.org/a", total_clicks: 12, unique_clickers: null }],
  errors: [],
  recent: [summary(id, { detailed_tracking: false, unsubscribes: null, unsubscribe_rate: null, subject: "Old issue" })],
})

const stubApi = (campaignStats: NewsletterApi["campaignStats"]) => ({ campaignStats }) as unknown as NewsletterApi

const campaigns = [
  issue(7, "sent", "2026-10-24T18:00:00Z", "Older issue"),
  issue(8, "draft", null, "Draft issue"),
  issue(9, "sent", "2026-10-31T18:00:00Z", "Newest issue"),
  issue(10, "sending", null, "Going out now"),
]

describe("CampaignStats", () => {
  it("offers sent campaigns only, newest first, and loads the newest", async () => {
    const load = vi.fn(async (id: number) => detailed(id))
    render(<CampaignStats api={stubApi(load)} campaigns={campaigns} />)

    const select = screen.getByLabelText("Campaign")
    const options = within(select).getAllByRole("option")
    expect(options.map((o) => o.textContent)).toEqual([
      expect.stringContaining("Newest issue"),
      expect.stringContaining("Older issue"),
    ])
    expect(select).toHaveValue("9")
    await waitFor(() => expect(load).toHaveBeenCalledWith(9))
  })

  it("shows the headline numbers with rates", async () => {
    render(<CampaignStats api={stubApi(async (id) => detailed(id))} campaigns={campaigns} />)

    const opens = await screen.findByRole("group", { name: "Unique opens" })
    expect(within(opens).getByText("282")).toBeInTheDocument()
    expect(within(opens).getByText(/47\.3%/)).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Recipients" })).getByText("596")).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Recipients" })).getByText(/of 600 queued/)).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Send errors" })).getByText("3")).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Unique clickers" })).getByText(/10\.2%/)).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Click-to-open" })).getByText("21.6%")).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Total clicks" })).getByText("97")).toBeInTheDocument()
    expect(within(screen.getByRole("group", { name: "Unsubscribes" })).getByText("2")).toBeInTheDocument()
    expect(screen.getByText("Detailed tracking")).toBeInTheDocument()
    expect(screen.getByRole("cell", { name: "gmail: 400 Invalid To header" })).toBeInTheDocument()
  })

  it("lists links by total clicks", async () => {
    render(<CampaignStats api={stubApi(async (id) => detailed(id))} campaigns={campaigns} />)

    const table = await screen.findByRole("table", { name: "Links" })
    const rows = within(table).getAllByRole("row").slice(1)
    expect(rows[0]).toHaveTextContent("big-story")
    expect(rows[0]).toHaveTextContent("40")
    expect(rows[1]).toHaveTextContent("x.com")
    const link = within(rows[0]).getByRole("link")
    expect(link).toHaveAttribute("href", "https://www.thetriangle.org/news/big-story")
    expect(link).toHaveAttribute("rel", "noopener noreferrer")
  })

  it("never shows an unknown number as zero for a campaign before detailed tracking", async () => {
    render(<CampaignStats api={stubApi(async (id) => partial(id))} campaigns={campaigns} />)

    expect(await screen.findByText(/sent before per-link and unsubscribe tracking/i)).toBeInTheDocument()
    expect(screen.getByText("Partial tracking")).toBeInTheDocument()
    const unsubscribes = screen.getByRole("group", { name: "Unsubscribes" })
    expect(within(unsubscribes).getByText("Unavailable")).toBeInTheDocument()
    expect(within(unsubscribes).queryByText("0")).not.toBeInTheDocument()
    const linkRow = within(screen.getByRole("table", { name: "Links" })).getAllByRole("row")[1]
    expect(within(linkRow).getByText("Unavailable")).toBeInTheDocument()
    expect(within(linkRow).queryByText("0")).not.toBeInTheDocument()
    const recentRow = within(screen.getByRole("table", { name: "Recent campaigns" })).getAllByRole("row")[1]
    expect(within(recentRow).getByText("Partial")).toBeInTheDocument()
    expect(within(recentRow).getByLabelText("Not available")).toBeInTheDocument()
  })

  it("switches campaign from the selector and from the comparison table", async () => {
    const user = userEvent.setup()
    // The comparison row must be a real sent campaign (7), not detailed()'s id+100 placeholder.
    const load = vi.fn(async (id: number) =>
      detailed(id, { recent: [summary(9), summary(7, { subject: "Older issue", sent_at: "2026-10-24T18:00:00Z" })] }),
    )
    render(<CampaignStats api={stubApi(load)} campaigns={campaigns} />)
    await screen.findByRole("table", { name: "Recent campaigns" })

    await user.selectOptions(screen.getByLabelText("Campaign"), "7")
    await waitFor(() => expect(load).toHaveBeenLastCalledWith(7))

    load.mockClear()
    await user.selectOptions(screen.getByLabelText("Campaign"), "9")
    const recent = await screen.findByRole("table", { name: "Recent campaigns" })
    await user.click(within(recent).getByRole("button", { name: "Older issue" }))
    await waitFor(() => expect(load).toHaveBeenLastCalledWith(7))
    expect(screen.getByLabelText("Campaign")).toHaveValue("7")
  })

  it("marks the selected campaign in the comparison", async () => {
    render(<CampaignStats api={stubApi(async (id) => detailed(id))} campaigns={campaigns} />)

    const recent = await screen.findByRole("table", { name: "Recent campaigns" })
    const rows = within(recent).getAllByRole("row").slice(1)
    expect(rows[0]).toHaveAttribute("aria-current", "true")
    expect(rows[1]).not.toHaveAttribute("aria-current")
  })

  it("ignores a slow answer for a campaign no longer selected", async () => {
    const user = userEvent.setup()
    let resolveSlow: (s: NewsletterCampaignStats) => void = () => {}
    const load = vi.fn((id: number) =>
      id === 9
        ? new Promise<NewsletterCampaignStats>((resolve) => {
            resolveSlow = resolve
          })
        : Promise.resolve(detailed(id, { subject: "Older issue" })),
    )
    render(<CampaignStats api={stubApi(load)} campaigns={campaigns} />)
    await user.selectOptions(screen.getByLabelText("Campaign"), "7")
    expect(await screen.findByRole("heading", { name: "Older issue" })).toBeInTheDocument()

    resolveSlow(detailed(9, { subject: "Newest issue" }))
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.getByRole("heading", { name: "Older issue" })).toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: "Newest issue" })).not.toBeInTheDocument()
  })

  it("shows a load error", async () => {
    render(
      <CampaignStats
        api={stubApi(async () => {
          throw new Error("Could not load campaign stats (500)")
        })}
        campaigns={campaigns}
      />,
    )
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load campaign stats (500)")
  })

  it("has an empty state before anything is sent", () => {
    const load = vi.fn()
    render(<CampaignStats api={stubApi(load)} campaigns={[issue(8, "draft", null)]} />)
    expect(screen.getByText("No sent campaigns yet")).toBeInTheDocument()
    expect(load).not.toHaveBeenCalled()
  })
})
