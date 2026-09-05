import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom"
import { beforeEach, describe, expect, it, vi } from "vitest"
import DashboardPage from "./DashboardPage"

const apiFetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>()
vi.mock("../hooks/useApiFetch", () => ({ useApiFetch: () => apiFetch }))

function Destination() {
  return <p>{useLocation().pathname}</p>
}

beforeEach(async () => {
  apiFetch.mockReset()
  apiFetch.mockImplementation(async (url, init) => {
    if (init?.method === "POST") {
      return Response.json({ id: 42, slug: "campus-news-2" }, { status: 201 })
    }
    if (url.startsWith("/v1/taxonomy")) {
      return Response.json([{ type: "section", slug: "news" }])
    }
    if (url.startsWith("/v1/authors")) return Response.json([])
    return Response.json({ articles: [], pagination: { total_count: 0 } })
  })
  await act(async () => { render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/articles/:id/:slug/edit" element={<Destination />} />
      </Routes>
    </MemoryRouter>,
  ) })
})

describe("quick draft", () => {
  it("uses the saved ID and deduplicated slug without additional lookups", async () => {
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText("Article headline..."), "Campus news")
    await user.click(screen.getByRole("button", { name: "Save draft" }))
    expect(await screen.findByText("/articles/42/campus-news-2/edit")).toBeInTheDocument()
    const writes = apiFetch.mock.calls.filter(([, init]) => init?.method === "POST")
    expect(writes).toHaveLength(1)
    expect(JSON.parse(String(writes[0][1]?.body))).toMatchObject({
      title: "Campus news", slug: "campus-news", status: "draft",
    })
    expect(apiFetch.mock.calls.some(([url]) => url.startsWith("/v1/articles/"))).toBe(false)
  })

  it("keeps the draft and shows the server error when creation fails", async () => {
    const defaultFetch = apiFetch.getMockImplementation()!
    apiFetch.mockImplementation((url, init) => init?.method === "POST"
      ? Promise.resolve(Response.json({ error: "Article writes are unavailable." }, { status: 503 }))
      : defaultFetch(url, init))
    const user = userEvent.setup()
    await user.type(screen.getByPlaceholderText("Article headline..."), "Campus news")
    await user.click(screen.getByRole("button", { name: "Save draft" }))
    expect(await screen.findByText("Article writes are unavailable.")).toBeInTheDocument()
    expect(screen.getByPlaceholderText("Article headline...")).toHaveValue("Campus news")
  })
})
