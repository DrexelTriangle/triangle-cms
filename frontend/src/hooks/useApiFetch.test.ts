import { renderHook } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { useApiFetch } from "./useApiFetch"

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

describe("useApiFetch", () => {
  it("has stable identity without a provider and preserves request options", async () => {
    vi.stubEnv("VITE_API_BASE_URL", "https://cms.example.test/")
    const response = Response.json({ error: "forbidden" }, { status: 403 })
    const fetch = vi.fn().mockResolvedValue(response)
    vi.stubGlobal("fetch", fetch)
    const { result, rerender } = renderHook(() => useApiFetch())
    const initial = result.current
    rerender()
    expect(result.current).toBe(initial)
    const controller = new AbortController()
    const options = { method: "PATCH", body: "{}", signal: controller.signal }
    expect(await result.current("/v1/settings/site", options)).toBe(response)
    expect(fetch).toHaveBeenCalledWith("https://cms.example.test/v1/settings/site", {
      ...options, credentials: "include",
    })
    expect(response.bodyUsed).toBe(false)
  })

  it("leaves absolute URLs intact and propagates network errors", async () => {
    const failure = new TypeError("Network unavailable")
    const fetch = vi.fn().mockRejectedValue(failure)
    vi.stubGlobal("fetch", fetch)
    const { result } = renderHook(() => useApiFetch())
    await expect(result.current("https://cms.example.test/v1/articles")).rejects.toBe(failure)
    expect(fetch).toHaveBeenCalledWith("https://cms.example.test/v1/articles", { credentials: "include" })
  })
})
