// A footer entry's visible_from travels as an RFC3339 instant; DateTimeField
// speaks local wall time, "YYYY-MM-DDTHH:mm", with no zone. These are the two
// directions between them.

const pad = (n: number) => String(n).padStart(2, "0")

// RFC3339 -> the editor's local wall time. Built from the local getters rather
// than by slicing toISOString(), which is UTC and would shift the time shown
// by the viewer's offset. Unreadable input shows as unscheduled.
export function isoToLocalInput(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// Local wall time -> RFC3339 in UTC, the spelling the server stores, so a
// value round-trips without making the editor look dirty. A zoneless
// date-time string is parsed as local time, which is the point: the browser is
// the only side that knows which zone the editor picked the time in.
export function localInputToISO(value: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toISOString().replace(/\.\d{3}Z$/, "Z")
}

// The time a new schedule starts at: tomorrow, 09:00 local. Something has to
// be filled in for the field to appear, and "tomorrow morning" is a plausible
// launch that is obviously a placeholder; "now" would read as already live.
export function defaultVisibleFrom(now = new Date()): string {
  const next = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, 9, 0)
  return next.toISOString().replace(/\.\d{3}Z$/, "Z")
}
