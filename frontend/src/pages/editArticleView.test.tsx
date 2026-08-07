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
// Slugs belonging to other articles, so a GET for them answers 200 the way the
// API would rather than the 404 that means "free".
let takenSlugs: Set<string>
// SEO tags the article already carries, what /v1/tags offers unfiltered, and
// what a search over the archive can turn up.
let articleTags: string[]
let popularTagsPayload: Array<{ name: string; uses: number }>
let archiveTagsPayload: Array<{ name: string; uses: number }>

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
      featured_image: "https://example.com/a.jpg",
      featured_image_alt: "",
      categories: [{ name: "News", slug: "news" }],
      authors: [{ id: 7, name: "Reporter" }],
      seo: { tags: articleTags.map((name) => ({ name, slug: name })) },
    })
  }
  if (url.startsWith("/v1/tags")) {
    const query = new URL(url, "http://cms.test").searchParams.get("q")?.trim().toLowerCase() ?? ""
    if (!query) {
      return jsonResponse(popularTagsPayload)
    }
    // Stands in for the server's search over the whole archive: the archive
    // holds tags the popular list does not.
    return jsonResponse(archiveTagsPayload.filter((tag) => tag.name.toLowerCase().includes(query)))
  }
  if (method === "GET" && url.startsWith("/v1/articles/")) {
    const candidate = decodeURIComponent(url.slice("/v1/articles/".length))
    if (takenSlugs.has(candidate)) {
      return jsonResponse({ id: 2, title: "Someone else's story", slug: candidate, content: "" })
    }
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
    takenSlugs = new Set()
    articleTags = []
    popularTagsPayload = []
    archiveTagsPayload = []
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

  // The SEO fields live outside the body editor, so they are only autosaved if
  // they are part of the dirty-check snapshot. They were not, and a toggle on
  // its own was silently discarded.
  it("autosaves a noindex toggle on its own", async () => {
    const user = await renderEditor()

    await user.click(screen.getByRole("checkbox", { name: /Hide from search engines/ }))
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].body?.noindex).toBe(true)
  })

  // Same failure mode as the noindex toggle: the featured image's alt text sits
  // outside the body editor, so it only autosaves if it is in the snapshot.
  it("autosaves featured image alt text on its own", async () => {
    const user = await renderEditor()

    await user.type(screen.getByLabelText("Alt text"), "A protester holds a sign")
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].body?.photo_alt).toBe("A protester holds a sign")
  })

  // Regenerating rewrites the article's public URL, so it must wait for a
  // deliberate save rather than riding along on the 2.5s autosave.
  it("does not send a regenerated slug on autosave", async () => {
    const user = await renderEditor()

    await user.clear(screen.getByLabelText("Title"))
    await user.type(screen.getByLabelText("Title"), "Brand new headline")
    await user.click(screen.getByRole("button", { name: /Regenerate from title/ }))
    await waitFor(() => expect(screen.getByLabelText("Slug")).toHaveValue("brand-new-headline"))
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches.length).toBeGreaterThan(0)
    for (const patch of patches) {
      expect(patch.body).not.toHaveProperty("slug")
    }
  })

  it("sends the regenerated slug on an explicit save", async () => {
    const user = await renderEditor()

    await user.clear(screen.getByLabelText("Title"))
    await user.type(screen.getByLabelText("Title"), "Brand new headline")
    await user.click(screen.getByRole("button", { name: /Regenerate from title/ }))
    await waitFor(() => expect(screen.getByLabelText("Slug")).toHaveValue("brand-new-headline"))
    await user.click(screen.getByRole("button", { name: "Save Draft" }))

    await waitFor(() => expect(patchCalls().some((call) => call.body?.slug)).toBe(true))
    expect(patchCalls().find((call) => call.body?.slug)?.body?.slug).toBe("brand-new-headline")
  })

  // Two articles on one slug would make the public URL ambiguous, and the schema
  // has no unique index to stop it.
  it("suffixes a regenerated slug that another article already holds", async () => {
    takenSlugs.add("brand-new-headline")
    const user = await renderEditor()

    await user.clear(screen.getByLabelText("Title"))
    await user.type(screen.getByLabelText("Title"), "Brand new headline")
    await user.click(screen.getByRole("button", { name: /Regenerate from title/ }))

    await waitFor(() => expect(screen.getByLabelText("Slug")).toHaveValue("brand-new-headline-2"))
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

// The tag box used to be a bare text field, so the boilerplate tags a desk adds
// to nearly every article were retyped by hand each time. These pin the
// shortcut that replaced that.
describe("EditArticleView SEO tag suggestions", () => {
  beforeEach(() => {
    apiCalls = []
    articleStatus = "draft"
    articlePublishedDate = undefined
    takenSlugs = new Set()
    articleTags = []
    popularTagsPayload = [
      { name: "triangle", uses: 900 },
      { name: "drexel", uses: 800 },
      { name: "drexel triangle", uses: 400 },
      { name: "civil rights", uses: 20 },
    ]
    // The archive is everything, popular or not. "Men's Lacrosse" is the case
    // that matters: nobody retypes that spelling correctly, and it is nowhere
    // near the popular list.
    archiveTagsPayload = [...popularTagsPayload, { name: "Men's Lacrosse", uses: 46 }]
    apiFetchStub.mockClear()
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const suggestion = (name: string) => screen.getByRole("button", { name: `Add tag ${name}` })

  it("adds a frequently-used tag when its suggestion is clicked", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("triangle")).toBeInTheDocument())

    await user.click(suggestion("triangle"))
    await waitOutAutosave()

    expect(screen.getByRole("button", { name: "Remove tag triangle" })).toBeInTheDocument()
    expect(patchCalls()[0].body?.tags).toEqual(["triangle"])
  })

  // A suggestion for a tag the article already carries is a dead control, and
  // clicking it would look broken -- the add is a no-op against the dedupe.
  it("does not suggest a tag the article already carries", async () => {
    articleTags = ["triangle"]
    await renderEditor()
    await waitFor(() => expect(suggestion("drexel")).toBeInTheDocument())

    expect(screen.queryByRole("button", { name: "Add tag triangle" })).not.toBeInTheDocument()
  })

  it("narrows the suggestions to what the editor has typed", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("civil rights")).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText(/Type a tag, press Enter/), "drex")

    expect(suggestion("drexel")).toBeInTheDocument()
    expect(suggestion("drexel triangle")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Add tag civil rights" })).not.toBeInTheDocument()
  })

  // Clicking a suggestion blurs the tag input, and blur commits whatever is in
  // it. Finishing a half-typed word by clicking must not leave the fragment
  // behind as a second tag.
  it("does not also commit the half-typed draft when a suggestion is clicked", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("drexel")).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText(/Type a tag, press Enter/), "drex")
    await user.click(suggestion("drexel"))
    await waitOutAutosave()

    expect(screen.queryByRole("button", { name: "Remove tag drex" })).not.toBeInTheDocument()
    expect(patchCalls()[0].body?.tags).toEqual(["drexel"])
  })
  // The reason the search exists: the tag is in the archive, but it is not
  // popular enough to be offered, and retyping "Men's Lacrosse" from memory is
  // how a near-duplicate of it gets coined.
  it("finds a tag that is in the archive but not in the popular list", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("triangle")).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText(/Type a tag, press Enter/), "lacrosse")
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    await waitFor(() => expect(suggestion("Men's Lacrosse")).toBeInTheDocument())
    await user.click(suggestion("Men's Lacrosse"))
    await waitOutAutosave()

    expect(patchCalls()[0].body?.tags).toEqual(["Men's Lacrosse"])
  })

  // Typing a whole word must not be a request per keystroke.
  it("searches once for a word typed in one go", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("triangle")).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText(/Type a tag, press Enter/), "lacrosse")
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    const searches = apiCalls.filter((call) => call.url.includes("q="))
    expect(searches).toHaveLength(1)
    expect(searches[0].url).toContain("q=lacrosse")
  })

  // A tag nobody has used is still a tag worth adding -- but the editor should
  // be told that is what they are doing.
  it("says so when nothing in the archive matches", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(suggestion("triangle")).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText(/Type a tag, press Enter/), "quidditch")
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(screen.getByText(/No existing tag matches/)).toBeInTheDocument()
  })
})
