import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { clearArticleListCache, readSessionJSON, writeSessionJSON } from "./articleCache"

beforeEach(() => sessionStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("article cache", () => {
  it("round trips JSON and falls back for missing or malformed entries", () => {
    writeSessionJSON("valid", { page: 2 })
    sessionStorage.setItem("broken", "{")
    expect(readSessionJSON("valid", {})).toEqual({ page: 2 })
    expect(readSessionJSON("missing", [])).toEqual([])
    expect(readSessionJSON("broken", [])).toEqual([])
  })

  it("invalidates results without removing saved filters or unrelated entries", () => {
    for (const key of ["articleView:a:results", "articleView:b:results", "articleView:a:filters", "other"]) {
      sessionStorage.setItem(key, "1")
    }
    clearArticleListCache()
    expect(sessionStorage.length).toBe(2)
    expect(sessionStorage.getItem("articleView:a:filters")).toBe("1")
    clearArticleListCache("all")
    expect(sessionStorage.length).toBe(1)
    expect(sessionStorage.getItem("other")).toBe("1")
  })

  it("tolerates unavailable storage", () => {
    sessionStorage.setItem("articleView:a:results", "[]")
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked") })
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("full") })
    vi.spyOn(Storage.prototype, "key").mockImplementation(() => { throw new Error("blocked") })
    expect(readSessionJSON("missing", "fallback")).toBe("fallback")
    expect(() => writeSessionJSON("key", {})).not.toThrow()
    expect(() => clearArticleListCache("all")).not.toThrow()
  })
})
