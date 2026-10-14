import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import FooterMenuEditor from "./FooterMenuEditor"

// A footer entry can be scheduled to appear later (the Games link at the
// Wordangle launch). The CMS only stores the instant; these tests pin that the
// editor never loses or reshapes it on the way through, and that what it sends
// is an instant rather than a zoneless wall time.

type FooterEntryPayload = {
  kind: string
  label: string
  href: string
  new_tab: boolean
  visible_from?: string
}
type FooterPayload = { columns: { entries: FooterEntryPayload[] }[] }

const SCHEDULED = new Date(2099, 0, 15, 9, 0)
const SCHEDULED_ISO = SCHEDULED.toISOString().replace(/\.\d{3}Z$/, "Z")

const loadedFooter = (): FooterPayload => ({
  columns: [
    {
      entries: [
        { kind: "heading", label: "Comics & Puzzles", href: "/comics-puzzles", new_tab: false },
        { kind: "link", label: "Crossword", href: "/crossword", new_tab: false },
        { kind: "link", label: "Games", href: "/games", new_tab: false, visible_from: SCHEDULED_ISO },
      ],
    },
  ],
})

let patches: FooterPayload[] = []

const jsonResponse = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json" } })

const apiFetchStub = vi.fn(async (_url: string, init?: RequestInit): Promise<Response> => {
  if ((init?.method ?? "GET").toUpperCase() === "PATCH") {
    const body = JSON.parse(String(init?.body)) as FooterPayload
    patches.push(body)
    return jsonResponse(body)
  }
  return jsonResponse(loadedFooter())
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))

const renderEditor = async () => {
  const user = userEvent.setup()
  render(<FooterMenuEditor />)
  // Open the collapsible section the way an editor would, but only if it is
  // closed: SettingsSection remembers its open state, so whether it starts
  // closed depends on the storage the environment provides.
  const toggle = screen.getAllByRole("button").find((button) => button.hasAttribute("aria-expanded"))
  if (!toggle) throw new Error("no section toggle")
  if (toggle.getAttribute("aria-expanded") === "false") await user.click(toggle)
  expect(toggle).toHaveAttribute("aria-expanded", "true")
  await screen.findByDisplayValue("Games")
  return user
}

// jsdom's localStorage persists across tests in a file; under some Node
// versions the global is Node's own and absent, hence the optional chaining.
const SECTION_STORAGE_KEY = "cms.settings.section.footer"

// Each entry is the group around its Label input.
const entryFor = (label: string) => {
  const input = screen.getByDisplayValue(label)
  const entry = input.closest(".group\\/entry")
  if (!(entry instanceof HTMLElement)) throw new Error(`no entry for ${label}`)
  return within(entry)
}

const savedEntry = (label: string) =>
  patches.at(-1)?.columns.flatMap((column) => column.entries).find((entry) => entry.label === label)

describe("FooterMenuEditor scheduling", () => {
  beforeEach(() => {
    patches = []
    apiFetchStub.mockClear()
    globalThis.localStorage?.removeItem(SECTION_STORAGE_KEY)
  })

  it("preserves a loaded schedule through an unrelated save", async () => {
    const user = await renderEditor()

    expect(entryFor("Games").getByRole("textbox", { name: "Show from" })).toBeInTheDocument()
    expect(entryFor("Crossword").queryByRole("textbox", { name: "Show from" })).not.toBeInTheDocument()
    expect(entryFor("Games").getByText(/appears on its own/)).toBeInTheDocument()

    await user.type(screen.getByDisplayValue("Crossword"), "s")
    await user.click(screen.getByRole("button", { name: "Save footer" }))

    await waitFor(() => expect(patches).toHaveLength(1))
    expect(savedEntry("Games")?.visible_from).toBe(SCHEDULED_ISO)
    // An unscheduled entry must not grow an empty key.
    expect(savedEntry("Crosswords")).not.toHaveProperty("visible_from")
  })

  it("schedules an entry and sends the time as a UTC instant", async () => {
    const user = await renderEditor()

    await user.click(entryFor("Crossword").getByRole("button", { name: "Schedule" }))
    const field = entryFor("Crossword").getByRole("textbox", { name: "Show from" })
    await user.clear(field)
    await user.type(field, "2099-03-01T18:30{Enter}")
    await user.click(screen.getByRole("button", { name: "Save footer" }))

    await waitFor(() => expect(patches).toHaveLength(1))
    expect(savedEntry("Crossword")?.visible_from).toBe(
      new Date(2099, 2, 1, 18, 30).toISOString().replace(/\.\d{3}Z$/, "Z"),
    )
  })

  it("clears a schedule so the entry saves without one", async () => {
    const user = await renderEditor()

    await user.click(entryFor("Games").getByRole("button", { name: "Clear date" }))
    expect(entryFor("Games").queryByRole("textbox", { name: "Show from" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Save footer" }))

    await waitFor(() => expect(patches).toHaveLength(1))
    expect(savedEntry("Games")).not.toHaveProperty("visible_from")
  })

  it("is clean again after scheduling and then unscheduling", async () => {
    const user = await renderEditor()

    const crossword = entryFor("Crossword")
    await user.click(crossword.getByRole("button", { name: "Schedule" }))
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument()
    await user.click(crossword.getByRole("button", { name: "Remove schedule" }))

    expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Save footer" })).toBeDisabled()
  })
})
