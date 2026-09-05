import { renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useDashboardData } from "./useDashboardData"

const apiFetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>()
vi.mock("./useApiFetch", () => ({ useApiFetch: () => apiFetch }))

beforeEach(() => {
  apiFetch.mockReset()
  apiFetch.mockImplementation(async (url) => {
    if (url.includes("taxonomy")) return Response.json([])
    if (url.includes("authors")) return Response.json({ authors: [], pagination: { total_count: 123 } })
    return Response.json({ articles: [], pagination: { total_count: 0 } })
  })
})

describe("useDashboardData", () => {
  it("uses count metadata and preserves an empty section list", async () => {
    const { result } = renderHook(() => useDashboardData())
    await waitFor(() => expect(result.current.apiHealth).toBe("ok"))
    expect(result.current.stats).toEqual({
      totalArticles: 0, publishedArticles: 0, draftArticles: 0,
      totalAuthors: 123, totalSections: 0,
    })
    expect(apiFetch.mock.calls.map(([url]) => url)).toContain("/v1/authors?limit=1")
    expect(apiFetch.mock.calls.some(([url]) => url.includes("homepage"))).toBe(false)
  })

  it("keeps successful counts when independent requests fail", async () => {
    const fetch = apiFetch.getMockImplementation()!
    apiFetch.mockImplementation((url, init) => url.includes("status=draft") || url.includes("health")
      ? Promise.resolve(Response.json({ error: "Unavailable" }, { status: 503 }))
      : fetch(url, init))
    const { result } = renderHook(() => useDashboardData())
    await waitFor(() => expect(result.current.apiHealth).toBe("error"))
    expect(result.current.stats.draftArticles).toBeNull()
    expect(result.current.stats.totalAuthors).toBe(123)
    expect(result.current.stats.publishedArticles).toBe(0)
  })

  it("aborts every outstanding request on unmount", () => {
    apiFetch.mockImplementation(() => new Promise(() => {}))
    const { unmount } = renderHook(() => useDashboardData())
    const signals = apiFetch.mock.calls.map(([, init]) => init?.signal)
    expect(signals).toHaveLength(6)
    expect(signals.every((signal) => signal && !signal.aborted)).toBe(true)
    unmount()
    expect(signals.every((signal) => signal?.aborted)).toBe(true)
  })
})
