import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import EditArticleView from "./editArticleView"

// The publish-timing radios are intent, not action: only the publish button may
// move an article across the draft/live line. These tests pin that, because the
// failure mode is silent -- an editor clicking "Publish now" to read the blurb
// would have had the article on the public site 2.5 seconds later.

const ARTICLE_SLUG = "live-story"

type ApiCall = { url: string; method: string; body: Record<string, unknown> | null }

let apiCalls: ApiCall[] = []
let articleStatus: string
let articlePublishedDate: string | undefined

const jsonResponse = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  })

const apiFetchStub = vi.fn(async (url: string, init?: RequestInit): Promise<Response> => {
  const method = (init?.method ?? "GET").toUpperCase()
  apiCalls.push({
    url,
    method,
    body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
  })

  if (url.includes("/edit-lock")) {
    return new Response(null, { status: 204 })
  }
  if (url.startsWith("/v1/authors")) {
    return jsonResponse([{ id: 7, display_name: "Reporter" }])
  }
  if (url.startsWith("/v1/taxonomy")) {
    return jsonResponse([
      { slug: "news", canonical_title: "News", type: "section", parent_slug: null },
    ])
  }
  if (method === "GET" && url.startsWith(`/v1/articles/${ARTICLE_SLUG}`)) {
    return jsonResponse({
      id: 1,
      title: "Live story",
      slug: ARTICLE_SLUG,
      content: "<p>Body</p>",
      excerpt: "Excerpt",
      status: articleStatus,
      published_date: articlePublishedDate,
      comment_status: "open",
      categories: [{ name: "News", slug: "news" }],
      authors: [{ id: 7, name: "Reporter" }],
    })
  }
  if (method === "PATCH") {
    return new Response(null, { status: 204 })
  }
  return jsonResponse({}, 404)
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))

// Trix and the Yoast analysis both drag in browser-only machinery that has
// nothing to do with publish timing.
vi.mock("../components/TrixEditor", () => ({
  default: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => (
    <textarea data-testid="article-body" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))
vi.mock("../components/MediaPicker", () => ({ default: () => null }))
vi.mock("../components/SeoAnalysis", () => ({ default: () => null }))

const patchCalls = () => apiCalls.filter((call) => call.method === "PATCH")

const renderEditor = async () => {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  render(
    <MemoryRouter initialEntries={[`/articles/${ARTICLE_SLUG}/edit`]}>
      <Routes>
        <Route path="/articles/:slug/edit" element={<EditArticleView />} />
      </Routes>
    </MemoryRouter>,
  )
  await waitFor(() => expect(screen.getByTestId("article-body")).toBeInTheDocument())
  return user
}

// Long enough to clear AUTOSAVE_DELAY_MS and settle the save it would fire.
const waitOutAutosave = async () => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
}

describe("EditArticleView autosave", () => {
  beforeEach(() => {
    apiCalls = []
    articleStatus = "draft"
    articlePublishedDate = undefined
    apiFetchStub.mockClear()
    // shouldAdvanceTime keeps Testing Library's own waitFor polling alive while
    // the autosave debounce stays under our control.
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it("does not publish a draft when the editor only selects Publish now", async () => {
    const user = await renderEditor()

    await user.click(screen.getByRole("radio", { name: /Publish now/ }))
    await waitOutAutosave()

    expect(patchCalls()).toHaveLength(0)
    expect(screen.getByText(/Publish timing is not autosaved/)).toBeInTheDocument()
  })

  it("autosaves body edits made after picking Publish now without sending a status", async () => {
    const user = await renderEditor()

    await user.click(screen.getByRole("radio", { name: /Publish now/ }))
    await user.type(screen.getByTestId("article-body"), " and more")
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].body).not.toHaveProperty("status")
    expect(patches[0].body).not.toHaveProperty("published_date")
    expect(patches[0].body?.content).toContain("and more")
  })

  it("does not unpublish a live article when the editor only selects Draft", async () => {
    articleStatus = "published"
    articlePublishedDate = "2025-03-04T15:30:00Z"
    const user = await renderEditor()

    await user.click(screen.getByRole("radio", { name: /Draft/ }))
    await user.type(screen.getByTestId("article-body"), " and more")
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].body).not.toHaveProperty("status")
  })

  it("publishes only when the publish button is pressed", async () => {
    const user = await renderEditor()

    await user.click(screen.getByRole("radio", { name: /Publish now/ }))
    await user.click(screen.getByRole("button", { name: "Publish" }))
    await waitFor(() => expect(patchCalls()).toHaveLength(1))

    expect(patchCalls()[0].body?.status).toBe("published")
    // Once the transition is on file, later autosaves stop warning about it.
    await waitOutAutosave()
    expect(screen.queryByText(/Publish timing is not autosaved/)).not.toBeInTheDocument()
  })
})
