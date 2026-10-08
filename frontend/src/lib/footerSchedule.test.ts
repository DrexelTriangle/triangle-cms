import { describe, expect, it } from "vitest"

import { defaultVisibleFrom, isoToLocalInput, localInputToISO } from "./footerSchedule"

// Expected values are built with the local Date constructor, so these hold in
// whatever zone the suite runs in rather than only in the author's.
const utcSeconds = (date: Date) => date.toISOString().replace(/\.\d{3}Z$/, "Z")

describe("footer schedule conversion", () => {
  it("turns a local wall time into the UTC RFC3339 the server stores", () => {
    expect(localInputToISO("2026-10-10T09:00")).toBe(utcSeconds(new Date(2026, 9, 10, 9, 0)))
    expect(localInputToISO("2026-10-10T09:00")).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00Z$/)
  })

  it("shows an RFC3339 instant as the viewer's local wall time", () => {
    const instant = new Date(2026, 9, 10, 9, 30)
    expect(isoToLocalInput(instant.toISOString())).toBe("2026-10-10T09:30")
  })

  it("round-trips without drifting by the viewer's UTC offset", () => {
    const stored = utcSeconds(new Date(2026, 0, 5, 18, 45))
    expect(localInputToISO(isoToLocalInput(stored))).toBe(stored)
  })

  it("treats blank or unreadable values as unscheduled", () => {
    expect(isoToLocalInput(undefined)).toBe("")
    expect(isoToLocalInput("")).toBe("")
    expect(isoToLocalInput("soon")).toBe("")
    expect(localInputToISO("")).toBe("")
    expect(localInputToISO("not a date")).toBe("")
  })

  it("starts a new schedule at 9am tomorrow", () => {
    const now = new Date(2026, 9, 7, 22, 15)
    expect(defaultVisibleFrom(now)).toBe(utcSeconds(new Date(2026, 9, 8, 9, 0)))
  })
})
