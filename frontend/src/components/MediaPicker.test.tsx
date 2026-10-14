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

// What POST /v1/media answers: a fresh asset at a content-addressed path.
const UPLOADED = {
  id: 77,
  path: "wp-content/uploads/2026/10/protest-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  url: "/wp-content/uploads/2026/10/protest-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  content_type: "image/jpeg",
  width: 1200,
  height: 800,
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
  if (method === "POST") {
    return jsonResponse(UPLOADED, 201)
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

    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ alt_text: "Students marching" }), "library")
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

// Replacing an article's image is an upload from this picker. The caller has to
// know that is what happened: a just-uploaded file has no library alt text yet,
// so adopting the library's (empty) description would silently wipe the one the
// author already wrote for this spot.
describe("MediaPicker selection source", () => {
  beforeEach(() => {
    apiCalls = []
    patchStatus = 200
    apiFetchStub.mockClear()
  })

  it("reports an uploaded file as an upload, with the new asset's URL", async () => {
    const { onSelect, user } = await renderPicker()
    const input = document.querySelector<HTMLInputElement>('input[type="file"]')
    if (!input) throw new Error("no file input")

    await user.upload(input, new File(["bytes"], "protest.jpg", { type: "image/jpeg" }))

    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1))
    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ id: 77, url: UPLOADED.url, width: 1200, height: 800 }),
      "upload",
    )
    expect(apiCalls.filter((call) => call.method === "POST").map((call) => call.url)).toEqual(["/v1/media"])
  })

  it("reports a library tile as a library pick", async () => {
    const { onSelect, user } = await renderPicker()

    await user.click(screen.getByTitle("protest.jpg"))

    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 42 }), "library")
  })
})

describe("MediaPicker thumbnails", () => {
  // The library is mostly camera originals, so a tile without its renditions
  // downloads a full-size photo to draw a 150px square.
  it("offers the renditions to the browser and keeps the original as src", async () => {
    apiFetchStub.mockImplementationOnce(async () =>
      jsonResponse({
        media: [
          {
            ...GALLERY_ITEM,
            alt_text: "Students marching",
            variants: [{ url: "/media/variants/protest.jpg.480w.webp", width: 480, height: 320 }],
          },
        ],
      }),
    )
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)

    const thumbnail = await screen.findByAltText("Students marching")
    expect(thumbnail).toHaveAttribute("src", GALLERY_ITEM.url)
    expect(thumbnail).toHaveAttribute("srcset", "/media/variants/protest.jpg.480w.webp 480w")
    expect(thumbnail).toHaveAttribute("sizes")
  })
})

describe("MediaPicker audio", () => {
  const AUDIO_ITEM: MediaPickerItem = {
    id: 9,
    url: "https://m.test/wp-content/uploads/2026/10/ep.mp3",
    file_name: "ep.mp3",
    mime_type: "audio/mpeg",
  }

  beforeEach(() => {
    apiFetchStub.mockClear()
  })

  it("shows an audio row with an audio icon", async () => {
    apiFetchStub.mockImplementationOnce(async () => jsonResponse({ media: [AUDIO_ITEM] }))
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)

    expect(await screen.findByLabelText("Audio file")).toBeInTheDocument()
  })

  it("renders no image for an audio row", async () => {
    apiFetchStub.mockImplementationOnce(async () => jsonResponse({ media: [AUDIO_ITEM] }))
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)
    await screen.findByText("ep.mp3")

    expect(document.querySelector('img[src$="ep.mp3"]')).toBeNull()
  })

  it("offers no alt text field for an audio row", async () => {
    apiFetchStub.mockImplementationOnce(async () => jsonResponse({ media: [AUDIO_ITEM] }))
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)
    await screen.findByText("ep.mp3")

    expect(screen.queryByRole("button", { name: /alt text/i })).toBeNull()
  })

  it("asks the server for images only when imagesOnly is set", async () => {
    render(<MediaPicker imagesOnly onClose={vi.fn()} onSelect={vi.fn()} />)
    await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())

    expect(String(apiFetchStub.mock.calls[0][0])).toContain("mime_type=image%2F")
  })

  it("refuses audio uploads when imagesOnly is set", async () => {
    render(<MediaPicker imagesOnly onClose={vi.fn()} onSelect={vi.fn()} />)
    await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())

    expect((document.querySelector('input[type="file"]') as HTMLInputElement).accept).not.toContain("audio")
  })

  it("does not filter by type for the body editor", async () => {
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)
    await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())

    expect(String(apiFetchStub.mock.calls[0][0])).not.toContain("mime_type")
  })

  it("accepts audio uploads for the body editor", async () => {
    render(<MediaPicker onClose={vi.fn()} onSelect={vi.fn()} />)
    await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())

    expect((document.querySelector('input[type="file"]') as HTMLInputElement).accept).toContain("audio/mpeg")
  })
})
