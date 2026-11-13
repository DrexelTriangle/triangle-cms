import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import NewsletterView from "./newsletterView"

// Without VITE_NEWSLETTER_API=live the page runs on the in-browser mock, so
// the network is never touched.
vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => vi.fn(),
}))

describe("NewsletterView", () => {
  afterEach(() => vi.unstubAllGlobals())
  beforeEach(() => {
    // Under recent Node the global localStorage is Node's own and absent.
    const storage = new Map<string, string>()
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => storage.get(k) ?? null,
      setItem: (k: string, v: string) => storage.set(k, v),
    })
    storage.set(
      "delta.newsletters.v1",
      JSON.stringify({
        nextId: 2,
        newsletters: [
          {
            id: 1,
            subject: "To The Point",
            preheader: "",
            status: "sent",
            sponsor: null,
            body_html: "",
            email_html: `<a href="https://www.thetriangle.org/a">A</a>`,
            scheduled_for: null,
            sent_at: "2026-10-31T18:00:00Z",
            recipient_count: 590,
            open_count: 280,
            click_count: 60,
            created_at: "2026-10-30T00:00:00Z",
            updated_at: "2026-10-31T18:00:00Z",
          },
        ],
      }),
    )
  })

  it("has a Stats tab without the search field", async () => {
    const user = userEvent.setup()
    render(
      <MemoryRouter>
        <NewsletterView />
      </MemoryRouter>,
    )
    const tabs = screen.getByRole("tablist")
    expect(screen.getByRole("searchbox", { name: /search campaigns/i })).toBeInTheDocument()

    await user.click(await screen.findByRole("tab", { name: "Stats" }))
    expect(screen.getByRole("tab", { name: "Stats" })).toHaveAttribute("aria-selected", "true")
    expect(tabs).toBeInTheDocument()
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument()
    expect(await screen.findByLabelText("Campaign")).toHaveValue("1")
    expect(await screen.findByRole("table", { name: "Links" })).toBeInTheDocument()
  })
})
