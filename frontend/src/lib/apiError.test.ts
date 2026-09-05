import { describe, expect, it } from "vitest"
import { readErrorMessage } from "./apiError"

describe("readErrorMessage", () => {
  it("explains a bare permission error without assuming an admin-only action", async () => {
    expect(await readErrorMessage(Response.json({ error: "forbidden" }, { status: 403 })))
      .toBe("You don't have permission to perform this action.")
    expect(await readErrorMessage(Response.json({ error: "Article is locked." }, { status: 403 })))
      .toBe("Article is locked.")
  })
  it("returns a trimmed server error", async () => {
    const response = Response.json({ error: "  Article is locked.  " }, { status: 409 })
    expect(await readErrorMessage(response, "Save failed")).toBe("Article is locked.")
  })

  it.each([null, {}, { error: " " }, { error: 500 }, { error: ["error"] }])(
    "uses the fallback for an invalid error body: %j",
    async (body) => {
      expect(await readErrorMessage(Response.json(body), "Save failed")).toBe("Save failed")
    },
  )

  it("handles non-JSON proxy errors", async () => {
    const response = new Response("<html>Bad gateway</html>", { status: 502 })
    expect(await readErrorMessage(response)).toBe("Could not complete request (502)")
  })

  it("handles an already-consumed response", async () => {
    const response = Response.json({ error: "Server error" })
    await response.json()
    expect(await readErrorMessage(response, "Save failed")).toBe("Save failed")
  })
})
