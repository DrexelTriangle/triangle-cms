import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import NewsletterView from "./newsletterView"

// The page used to render hardcoded campaigns, invented subscriber addresses
// and an open rate nobody measured. These tests pin that every number now
// comes from the API, that Send cannot deliver anything, and that the block
// editor posts exactly the document the server expects.

type ApiCall = { url: string; method: string; body: unknown }

let apiCalls: ApiCall[] = []
let role = { role: "admin", isAdmin: true, isLoading: false }

type Stats = {
  subscribers: Record<string, number>
  campaigns: Record<string, number>
  lists: Array<{ id: number; name: string; description: string; is_public: boolean; subscribed_count: number }>
}

let statsStatus: number
let statsPayload: Stats | { error: string }
let campaignsPayload: unknown[]
let subscribersPayload: unknown[]
let recipientCount: number
let renderPayload: unknown
let articleSearch: Array<{ id: number; title: string; slug: string }>

const LISTS = [
  { id: 1, name: "Drexel Students", description: "", is_public: true, subscribed_count: 1000 },
  { id: 2, name: "Alumni", description: "", is_public: true, subscribed_count: 300 },
]

const json = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } })

const apiFetchStub = vi.fn(async (url: string, init?: RequestInit): Promise<Response> => {
  const method = (init?.method ?? "GET").toUpperCase()
  apiCalls.push({ url, method, body: typeof init?.body === "string" ? JSON.parse(init.body) : null })

  if (url.startsWith("/v1/newsletter/stats")) return json(statsPayload, statsStatus)
  if (method === "GET" && url.startsWith("/v1/newsletter/campaigns?")) {
    return json({ campaigns: campaignsPayload, pagination: {}, counts: {} })
  }
  if (url.match(/\/v1\/newsletter\/campaigns\/\d+\/recipients\/count/)) return json({ count: recipientCount })
  if (method === "POST" && url === "/v1/newsletter/campaigns") {
    return json({ id: 99, status: "draft", ...(apiCalls.at(-1)?.body as object) }, 201)
  }
  if (method === "GET" && url.startsWith("/v1/newsletter/subscribers")) {
    return json({ subscribers: subscribersPayload, pagination: {}, counts: {} })
  }
  if (method === "PATCH" && url.startsWith("/v1/newsletter/subscribers/")) {
    const id = Number(url.split("/").pop())
    const current = (subscribersPayload as Array<{ id: number }>).find((s) => s.id === id)
    return json({ ...current, ...(apiCalls.at(-1)?.body as object), unsubscribed_at: "2026-10-06T00:00:00Z" })
  }
  if (url === "/v1/newsletter/render") return json(renderPayload)
  if (url.startsWith("/v1/articles?")) return json({ articles: articleSearch })
  return json({ error: `unexpected ${method} ${url}` }, 500)
})

vi.mock("../hooks/useApiFetch", () => ({ useApiFetch: () => apiFetchStub }))
vi.mock("../hooks/useCurrentUserRole", () => ({ useCurrentUserRole: () => role }))
// Trix needs a real browser; the block editor only needs value/onChange.
vi.mock("../components/TrixEditor", () => ({
  default: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => (
    <textarea data-testid="trix" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))

const writes = () => apiCalls.filter((c) => c.method !== "GET" && c.url !== "/v1/newsletter/render")

beforeEach(() => {
  apiCalls = []
  apiFetchStub.mockClear()
  role = { role: "admin", isAdmin: true, isLoading: false }
  statsStatus = 200
  statsPayload = {
    subscribers: { subscribed: 1234, unsubscribed: 56, all: 1290 },
    campaigns: { draft: 2, scheduled: 0, sent: 0, all: 2 },
    lists: LISTS,
  }
  campaignsPayload = [
    { id: 7, subject: "Week 5", preview_text: "Denim Day and more", status: "draft", list_ids: [1, 2], updated_at: "2026-10-01T12:00:00Z" },
  ]
  subscribersPayload = [
    { id: 5, email: "reader@example.com", name: "Rea Der", status: "subscribed", source: "public_form", list_ids: [1] },
  ]
  recipientCount = 1234
  renderPayload = { html: "<p>X</p>", warnings: [], articles: [] }
  articleSearch = []
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe("NewsletterView", () => {
  it("renders stat tiles from the API and no open-rate", async () => {
    render(<NewsletterView />)
    const tiles = await screen.findByRole("region", { name: "Newsletter stats" })
    expect(within(tiles).getByText("Subscribed").nextSibling).toHaveTextContent("1,234")
    expect(within(tiles).getByText("Unsubscribed").nextSibling).toHaveTextContent("56")
    expect(within(tiles).getByText("Drafts").nextSibling).toHaveTextContent("2")
    expect(within(tiles).getByText("Sent").nextSibling).toHaveTextContent("0")
    expect(screen.queryByText(/open rate/i)).toBeNull()
    expect(screen.queryByText(/recipients reached/i)).toBeNull()
    expect(screen.queryByText("jsmith@drexel.edu")).toBeNull()
    expect(await screen.findByText("Week 5")).toBeInTheDocument()
  })

  it("send dialog shows recipient count and cannot send", async () => {
    const user = userEvent.setup()
    render(<NewsletterView />)
    await user.click(await screen.findByRole("button", { name: "Send Week 5" }))

    const dialog = await screen.findByRole("dialog", { name: "Send campaign" })
    expect(await within(dialog).findByText("Would reach 1,234 subscribers in Drexel Students, Alumni.")).toBeInTheDocument()
    expect(within(dialog).getByText("Delivery isn't built yet.")).toBeInTheDocument()
    const send = within(dialog).getByRole("button", { name: "Send" })
    expect(send).toBeDisabled()
    await user.click(send)

    expect(writes()).toEqual([])
    expect(apiCalls.some((c) => c.url.includes("/send"))).toBe(false)
    expect(apiCalls.some((c) => c.url === "/v1/newsletter/campaigns/7/recipients/count")).toBe(true)
  })

  it("creating a campaign posts the blocks", async () => {
    const user = userEvent.setup()
    articleSearch = [{ id: 11, title: "Denim Day", slug: "denim-day" }]
    render(<NewsletterView />)
    await user.click(await screen.findByRole("button", { name: "New campaign" }))
    const editor = await screen.findByRole("dialog", { name: "New campaign" })

    await user.type(within(editor).getByLabelText("Subject"), "Week 6")
    await user.click(within(editor).getByRole("button", { name: "Add heading" }))
    await user.type(within(editor).getByLabelText("Heading text"), "More From News")
    await user.click(within(editor).getByRole("button", { name: "Add article" }))
    await user.type(within(editor).getByLabelText("Search articles"), "den")
    await user.click(await within(editor).findByRole("button", { name: "Denim Day" }))
    await user.click(within(editor).getByRole("checkbox", { name: "Alumni" }))
    await user.click(within(editor).getByRole("button", { name: "Save draft" }))

    await waitFor(() => expect(writes()).toHaveLength(1))
    expect(writes()[0]).toEqual({
      url: "/v1/newsletter/campaigns",
      method: "POST",
      body: {
        subject: "Week 6",
        preview_text: "",
        list_ids: [2],
        body_blocks: {
          version: 1,
          blocks: [
            { type: "heading", text: "More From News" },
            { type: "article", article_id: 11, show_image: true, show_excerpt: true },
          ],
        },
      },
    })
    expect(apiCalls.find((c) => c.url.startsWith("/v1/articles?"))?.url).toContain("title=den")
  })

  it("block editor reorders and deletes", async () => {
    const user = userEvent.setup()
    render(<NewsletterView />)
    await user.click(await screen.findByRole("button", { name: "New campaign" }))
    const editor = await screen.findByRole("dialog", { name: "New campaign" })
    await user.type(within(editor).getByLabelText("Subject"), "Order")
    for (const text of ["A", "B", "C"]) {
      await user.click(within(editor).getByRole("button", { name: "Add heading" }))
      const inputs = within(editor).getAllByLabelText("Heading text")
      await user.type(inputs[inputs.length - 1], text)
    }
    await user.click(within(editor).getByRole("button", { name: "Move block 3 up" }))
    await user.click(within(editor).getByRole("button", { name: "Remove block 1" }))
    await user.click(within(editor).getByRole("button", { name: "Save draft" }))

    await waitFor(() => expect(writes()).toHaveLength(1))
    const body = writes()[0].body as { body_blocks: { blocks: Array<{ text: string }> } }
    expect(body.body_blocks.blocks.map((b) => b.text)).toEqual(["C", "B"])
  })

  it("preview renders server html in a sandboxed iframe with warnings", async () => {
    const user = userEvent.setup()
    renderPayload = {
      html: "<p>X</p>",
      warnings: ['Article 12 ("Draft piece") is not published'],
      articles: [{ id: 11, title: "Denim Day", published: true }],
    }
    render(<NewsletterView />)
    await user.click(await screen.findByRole("button", { name: "New campaign" }))
    const editor = await screen.findByRole("dialog", { name: "New campaign" })
    await user.click(within(editor).getByRole("button", { name: "Add text" }))
    await user.type(within(editor).getByTestId("trix"), "Hello")

    const frame = await within(editor).findByTitle("Newsletter preview", {}, { timeout: 3000 })
    await waitFor(() => expect(frame.getAttribute("srcdoc")).toContain("<p>X</p>"), { timeout: 3000 })
    expect(frame.getAttribute("sandbox")).toBe("")
    expect(within(editor).getByText('Article 12 ("Draft piece") is not published')).toBeInTheDocument()
    expect(within(editor).getByText("🔒 Denim Day")).toBeInTheDocument()
    const renderCall = apiCalls.filter((c) => c.url === "/v1/newsletter/render").at(-1)
    expect(renderCall?.method).toBe("POST")
    expect((renderCall?.body as { body_blocks: { blocks: Array<{ html: string }> } }).body_blocks.blocks[0].html).toContain("Hello")
    expect(writes()).toEqual([])
  })

  it("unsubscribe toggles via PATCH and updates the row", async () => {
    const user = userEvent.setup()
    render(<NewsletterView />)
    await user.click(await screen.findByRole("button", { name: "Subscribers" }))
    const row = (await screen.findByText("reader@example.com")).closest("tr")!
    expect(within(row).getByText("Subscribed")).toBeInTheDocument()

    await user.click(within(row).getByRole("button", { name: "Unsubscribe reader@example.com" }))

    await waitFor(() => expect(within(row).getByText("Unsubscribed")).toBeInTheDocument())
    expect(writes()).toEqual([{ url: "/v1/newsletter/subscribers/5", method: "PATCH", body: { status: "unsubscribed" } }])
    expect(within(row).getByRole("button", { name: "Resubscribe reader@example.com" })).toBeInTheDocument()
  })

  it("editor sees no delete controls", async () => {
    const user = userEvent.setup()
    role = { role: "editor", isAdmin: false, isLoading: false }
    render(<NewsletterView />)
    await screen.findByText("Week 5")
    expect(screen.queryAllByRole("button", { name: /^Delete/ })).toHaveLength(0)
    await user.click(screen.getByRole("button", { name: "Subscribers" }))
    await screen.findByText("reader@example.com")
    expect(screen.queryAllByRole("button", { name: /^Delete/ })).toHaveLength(0)
  })

  it("admin sees delete on drafts and subscribers", async () => {
    const user = userEvent.setup()
    render(<NewsletterView />)
    expect(await screen.findByRole("button", { name: "Delete Week 5" })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Subscribers" }))
    expect(await screen.findByRole("button", { name: "Delete reader@example.com" })).toBeInTheDocument()
  })

  it("api error shows error state", async () => {
    statsStatus = 500
    statsPayload = { error: "boom" }
    render(<NewsletterView />)
    expect(await screen.findByRole("alert")).toHaveTextContent("boom")
    expect(screen.queryByRole("region", { name: "Newsletter stats" })).toBeNull()
    expect(screen.queryAllByRole("row")).toHaveLength(0)
  })

  it("renders empty states with no lists", async () => {
    const user = userEvent.setup()
    statsPayload = { subscribers: { subscribed: 0, unsubscribed: 0, all: 0 }, campaigns: { draft: 0, scheduled: 0, sent: 0, all: 0 }, lists: [] }
    campaignsPayload = []
    subscribersPayload = []
    render(<NewsletterView />)
    expect(await screen.findByText("No campaigns yet")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "New campaign" })).toBeDisabled()
    expect(screen.getByText("Create a list first")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Subscribers" }))
    expect(await screen.findByText("No subscribers yet")).toBeInTheDocument()
  })
})
