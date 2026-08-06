import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import MediaPicker, { type MediaPickerItem } from "./MediaPicker"

// Alt text is edited from the picker so an author never has to leave a
// half-written article for the Media library to describe an image. These tests
// pin the two things that makes it worth having: the edit reaches the library
// record, and the grid stops warning about the image it just described.

type ApiCall = { url: string; method: string; body: Record<string, unknown> | null }

let apiCalls: ApiCall[] = []

const jsonResponse = (payload: unknown, status = 200) =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  })

const GALLERY_ITEM: MediaPickerItem = {
  id: 42,
  url: "/media/2026/07/protest.jpg",
  file_name: "protest.jpg",
  mime_type: "image/jpeg",
}

let patchStatus = 200

const apiFetchStub = vi.fn(async (url: string, init?: RequestInit): Promise<Response> => {
  const method = (init?.method ?? "GET").toUpperCase()
  apiCalls.push({
    url,
    method,
    body: typeof init?.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : null,
  })

  if (method === "PATCH") {
    return patchStatus === 200
      ? jsonResponse({ ...GALLERY_ITEM, alt_text: "Students marching on Market Street" })
      : jsonResponse({ error: "media not found" }, patchStatus)
  }
  return jsonResponse({ media: [GALLERY_ITEM] })
})

vi.mock("../hooks/useApiFetch", () => ({
  useApiFetch: () => apiFetchStub,
}))

const renderPicker = async () => {
  const onSelect = vi.fn()
  const user = userEvent.setup()
  render(<MediaPicker onClose={vi.fn()} onSelect={onSelect} />)
  await screen.findByRole("button", { name: /no alt text/i })
  return { onSelect, user }
}

describe("MediaPicker alt text", () => {
  beforeEach(() => {
    apiCalls = []
    patchStatus = 200
    apiFetchStub.mockClear()
  })

  it("saves alt text to the library record without leaving the picker", async () => {
    const { user } = await renderPicker()

    await user.click(screen.getByRole("button", { name: /no alt text/i }))
    await user.type(
      screen.getByLabelText("Alt text for protest.jpg"),
      "Students marching on Market Street",
    )
    await user.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() => {
      expect(apiCalls.filter((call) => call.method === "PATCH")).toEqual([
        {
          url: "/v1/media/42",
          method: "PATCH",
          body: { alt_text: "Students marching on Market Street" },
        },
      ])
    })

    // The warning is gone and the saved description is on the tile, so the
    // author can see the image is now described and pick it.
    expect(screen.queryByText("No alt text")).not.toBeInTheDocument()
    expect(await screen.findByText("Students marching on Market Street")).toBeInTheDocument()
  })

  it("hands the newly saved alt text to the caller on select", async () => {
    const { onSelect, user } = await renderPicker()

    await user.click(screen.getByRole("button", { name: /no alt text/i }))
    await user.type(screen.getByLabelText("Alt text for protest.jpg"), "Students marching")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await screen.findByText("Students marching")

    await user.click(screen.getByTitle("protest.jpg"))

    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ alt_text: "Students marching" }))
  })

  it("keeps the typed text on screen when the save fails", async () => {
    patchStatus = 404
    const { user } = await renderPicker()

    await user.click(screen.getByRole("button", { name: /no alt text/i }))
    const field = screen.getByLabelText("Alt text for protest.jpg")
    await user.type(field, "Students marching")
    await user.click(screen.getByRole("button", { name: "Save" }))

    expect(await screen.findByText("media not found")).toBeInTheDocument()
    expect(field).toHaveValue("Students marching")
  })
})
