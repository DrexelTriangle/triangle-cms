import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { describe, expect, it, vi } from "vitest"
import CommentsView from "./commentsView"

const apiFetch = vi.fn<(url: string) => Promise<Response>>()
vi.mock("../hooks/useApiFetch", () => ({ useApiFetch: () => apiFetch }))

describe("comment markup", () => {
  it.each([
    '<div><img src="missing" onerror="alert(1)"><a href="javascript:alert(1)">Reader text</a></div>',
    '<section><div><p onclick="alert(1)">Reader text</p></div></section>',
    '<svg><foreignObject><p onmouseover="alert(1)">Reader text</p></foreignObject></svg>',
  ])("sanitizes descendants of unsupported wrappers: %s", async (content) => {
    apiFetch.mockResolvedValue(Response.json({ comments: [{
      id: 1, article_id: 2, article_title: "Campus news", article_slug: "campus-news",
      parent_id: 0, author_name: "Reader", content, status: "approved", type: "comment",
    }] }))
    const { container } = render(<MemoryRouter><CommentsView /></MemoryRouter>)
    expect(await screen.findByText("Reader text")).toBeInTheDocument()
    expect(container.querySelector('[onerror], [onclick], [onmouseover], a[href^="javascript:"]')).toBeNull()
    expect(container.querySelector('img[src="missing"], foreignObject')).toBeNull()
  })

  it("preserves emphasis and safe links inside unsupported wrappers", async () => {
    apiFetch.mockResolvedValue(Response.json({ comments: [{
      id: 1, article_id: 2, article_title: "Campus news", article_slug: "campus-news",
      parent_id: 0, author_name: "Reader", status: "approved", type: "comment",
      content: '<div><strong>Reader text</strong><a href="https://example.test/source">Source</a></div>',
    }] }))
    render(<MemoryRouter><CommentsView /></MemoryRouter>)
    expect((await screen.findByText("Reader text")).tagName).toBe("STRONG")
    const link = screen.getByRole("link", { name: "Source" })
    expect(link).toHaveAttribute("href", "https://example.test/source")
    expect(link).toHaveAttribute("rel", "noreferrer noopener")
  })
})
