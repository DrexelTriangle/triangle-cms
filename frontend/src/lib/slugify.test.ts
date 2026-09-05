import { describe, expect, it } from "vitest"
import { slugify } from "./slugify"

describe("slugify", () => {
  it.each([
    [" Campus NEWS! ", "campus-news"],
    ["a___b / c", "a-b-c"],
    ["---", ""],
    ["already-canonical", "already-canonical"],
  ])("canonicalizes %s", (value, expected) => {
    expect(slugify(value)).toBe(expected)
  })
})
