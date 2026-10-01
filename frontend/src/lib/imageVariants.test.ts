import { describe, expect, it } from "vitest"

import { srcSetFor } from "./imageVariants"

describe("srcSetFor", () => {
  it("lists every rendition with its width", () => {
    expect(
      srcSetFor([
        { url: "https://m.example/a.480w.webp", width: 480, height: 320 },
        { url: "https://m.example/a.960w.webp", width: 960, height: 640 },
      ]),
    ).toBe("https://m.example/a.480w.webp 480w, https://m.example/a.960w.webp 960w")
  })

  // An image the sidecar has not reached yet has no renditions, and the
  // attribute must then be absent rather than empty: an empty srcset is still
  // a srcset, and some browsers then show nothing instead of the src.
  it("is undefined when there is nothing to offer", () => {
    expect(srcSetFor(undefined)).toBeUndefined()
    expect(srcSetFor(null)).toBeUndefined()
    expect(srcSetFor([])).toBeUndefined()
    expect(srcSetFor([{ url: "", width: 480, height: 320 }])).toBeUndefined()
  })
})
