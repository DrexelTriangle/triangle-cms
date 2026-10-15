import { useEffect, type MutableRefObject } from "react"
import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { MediaPickerItem, MediaPickerSource } from "../components/MediaPicker"
import EditArticleView from "./editArticleView"

// The publish-timing radios are intent, not action: only the publish button may
// move an article across the draft/live line. These tests pin that, because the
// failure mode is silent: an editor clicking "Publish now" to read the blurb
// would have had the article on the public site 2.5 seconds later.

const ARTICLE_SLUG = "live-story"

type ApiCall = { url: string; method: string; body: Record<string, unknown> | null }

let apiCalls: ApiCall[] = []
let articleStatus: string
let articleContent: string
// What the mocked editor reports through apiRef and onPendingChange.
let pendingFlag = false
let articlePublishedDate: string | undefined
// Slugs belonging to other articles, so a GET for them answers 200 the way the
// API would rather than the 404 that means "free".
let takenSlugs: Set<string>
// SEO tags the article already carries, what /v1/tags offers unfiltered, and
// what a search over the archive can turn up.
let articleTags: string[]
let popularTagsPayload: Array<{ name: string; uses: number }>
let archiveTagsPayload: Array<{ name: string; uses: number }>
// The featured image's article-specific alt text as loaded.
let featuredImageAlt = ""

const ORIGINAL_FEATURED_URL = "https://example.com/a.jpg"
// A fresh upload: a new content-addressed asset with no library alt text yet.
const REPLACEMENT_UPLOAD: MediaPickerItem = {
  id: 77,
  url: "/wp-content/uploads/2026/10/a-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  file_name: "a-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  mime_type: "image/jpeg",
  width: 1200,
  height: 800,
}
const LIBRARY_IMAGE: MediaPickerItem = {
  id: 12,
  url: "/wp-content/uploads/2024/02/quad.jpg",
  file_name: "quad.jpg",
  mime_type: "image/jpeg",
  alt_text: "Students on the quad",
}

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
      content: articleContent,
      excerpt: "Excerpt",
      status: articleStatus,
      published_date: articlePublishedDate,
      comment_status: "open",
      featured_image: ORIGINAL_FEATURED_URL,
      featured_image_alt: featuredImageAlt,
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
vi.mock("../components/TrixEditor", () => {
  const MockTrixEditor = ({
    value,
    onChange,
    apiRef,
    onPendingChange,
  }: {
    value: string
    onChange: (next: string) => void
    apiRef?: MutableRefObject<{ hasPendingUploads: () => boolean } | null>
    onPendingChange?: (pending: boolean) => void
  }) => {
    useEffect(() => {
      if (apiRef) apiRef.current = { hasPendingUploads: () => pendingFlag }
      onPendingChange?.(pendingFlag)
    }, [apiRef, onPendingChange])
    return <textarea data-testid="article-body" value={value} onChange={(e) => onChange(e.target.value)} />
  }
  return { default: MockTrixEditor }
})
// Stands in for the library picker with the two ways it can hand back an image:
// a file the author just uploaded (how an image is replaced), or a tile from
// the library.
vi.mock("../components/MediaPicker", () => ({
  default: ({ onSelect }: { onSelect: (item: MediaPickerItem, source: MediaPickerSource) => void }) => (
    <div>
      <button onClick={() => onSelect(REPLACEMENT_UPLOAD, "upload")} type="button">
        Upload replacement
      </button>
      <button onClick={() => onSelect(LIBRARY_IMAGE, "library")} type="button">
        Pick library image
      </button>
    </div>
  ),
}))
vi.mock("../components/SeoAnalysis", () => ({ default: () => null }))

const patchCalls = () => apiCalls.filter((call) => call.method === "PATCH")

const renderEditor = async (
  initialEntry = `/articles/${ARTICLE_SLUG}/edit`,
  routePath = "/articles/:slug/edit",
) => {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path={routePath} element={<EditArticleView />} />
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
    articleContent = "<p>Body</p>"
    pendingFlag = false
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

  it("loads and saves by id when the editor route carries one", async () => {
    const user = await renderEditor(
      `/articles/42/${ARTICLE_SLUG}/edit`,
      "/articles/:id/:slug/edit",
    )

    expect(apiCalls.some((call) => call.method === "GET" && call.url === `/v1/articles/${ARTICLE_SLUG}?id=42`)).toBe(true)

    await user.type(screen.getByTestId("article-body"), " and more")
    await waitOutAutosave()

    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].url).toBe(`/v1/articles/${ARTICLE_SLUG}?id=42`)
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

// Replacing the featured image is an upload plus this article's own save: the
// new asset's URL goes into this article's photo_url and nowhere else, and the
// old asset is left alone for whatever else uses it.
describe("EditArticleView featured image replacement", () => {
  beforeEach(() => {
    apiCalls = []
    articleStatus = "draft"
    articlePublishedDate = undefined
    takenSlugs = new Set()
    articleTags = []
    popularTagsPayload = []
    archiveTagsPayload = []
    featuredImageAlt = "Riot police at the barricade"
    apiFetchStub.mockClear()
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    featuredImageAlt = ""
    vi.useRealTimers()
  })

  it("saves the uploaded replacement to this article and keeps its alt text", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(screen.getByLabelText("Alt text")).toHaveValue("Riot police at the barricade"))

    await user.click(screen.getByRole("button", { name: "Change image" }))
    await user.click(screen.getByRole("button", { name: "Upload replacement" }))
    await waitOutAutosave()

    // The description was written for this article's image and a replacement
    // is the same picture, re-shot or re-cropped: wiping it would publish the
    // new image with nothing for screen readers.
    expect(screen.getByLabelText("Alt text")).toHaveValue("Riot police at the barricade")
    const patches = patchCalls()
    expect(patches).toHaveLength(1)
    expect(patches[0].url).toBe(`/v1/articles/${ARTICLE_SLUG}`)
    expect(patches[0].body?.photo_url).toBe(REPLACEMENT_UPLOAD.url)
    expect(patches[0].body?.photo_alt).toBe("Riot police at the barricade")
    // Nothing touches the media library or any other article: no global
    // rewrite of the old asset and no delete of it.
    expect(apiCalls.filter((call) => call.method !== "GET" && !call.url.startsWith(`/v1/articles/${ARTICLE_SLUG}`))).toEqual([])
  })

  it("adopts the library's description when a different library image is chosen", async () => {
    const user = await renderEditor()
    await waitFor(() => expect(screen.getByLabelText("Alt text")).toHaveValue("Riot police at the barricade"))

    await user.click(screen.getByRole("button", { name: "Change image" }))
    await user.click(screen.getByRole("button", { name: "Pick library image" }))
    await waitOutAutosave()

    expect(patchCalls()[0].body?.photo_url).toBe(LIBRARY_IMAGE.url)
    expect(patchCalls()[0].body?.photo_alt).toBe("Students on the quad")
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
  // clicking it would look broken: the add is a no-op against the dedupe.
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

  // A tag nobody has used is still a tag worth adding, but the editor should
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

describe("save guard for embeds and uploads", () => {
  // The edit-lock heartbeat is a PUT the page sends on load; it is not a save.
  const nonGetCalls = () =>
    apiCalls.filter((c) => c.method !== "GET" && !c.url.includes("/edit-lock")).length

  beforeEach(() => {
    apiCalls = []
    articleStatus = "draft"
    articleContent = "<p>Body</p>"
    pendingFlag = false
    takenSlugs = new Set()
    articleTags = []
    popularTagsPayload = []
    archiveTagsPayload = []
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it("disables Save while an upload is pending", async () => {
    pendingFlag = true
    await renderEditor()
    await waitFor(() => expect(screen.getByRole("button", { name: /^save draft$/i })).toBeDisabled())
  })

  it("refuses an autosave while an upload is pending, sending nothing", async () => {
    pendingFlag = true
    const user = await renderEditor()
    await user.type(screen.getByTestId("article-body"), " more")
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000)
    })
    expect(await screen.findByText(/waiting for upload to finish/i)).toBeInTheDocument()
    expect(nonGetCalls()).toBe(0)
  })

  it("refuses to save an article with an invalid embed and names the block", async () => {
    articleContent = `<div><figure data-trix-attachment='${JSON.stringify({ contentType: "application/x-triangle-embed", embedProvider: "spotify", embedUrl: "nope" })}' data-trix-attributes='{}'></figure></div>`
    const user = await renderEditor()
    await user.click(screen.getByRole("button", { name: /^save draft$/i }))
    expect(await screen.findByText(/embed 1 has an invalid url/i)).toBeInTheDocument()
    expect(nonGetCalls()).toBe(0)
  })
})
