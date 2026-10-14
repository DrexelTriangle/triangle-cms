import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type { MediaPickerItem, MediaPickerSource } from "./MediaPicker"

// jsdom has ElementInternals but not its form-validity half, which the
// <trix-editor> custom element calls as soon as it connects. Hoisted so it is
// in place before the import below registers the element.
vi.hoisted(() => {
  type Internals = Record<string, unknown>
  const proto = HTMLElement.prototype as unknown as { attachInternals?: (this: HTMLElement) => Internals }
  const original = proto.attachInternals
  proto.attachInternals = function (this: HTMLElement) {
    const internals = original ? original.call(this) : {}
    internals.setValidity ??= () => {}
    internals.setFormValue ??= () => {}
    return internals
  }
})

import TrixEditor from "./TrixEditor"

const OLD_URL = "/wp-content/uploads/2024/01/protest.jpg"
// A fresh upload: a new content-addressed asset with no library alt text yet.
const REPLACEMENT_UPLOAD: MediaPickerItem = {
  id: 77,
  url: "/wp-content/uploads/2026/10/protest-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  file_name: "protest-0f3a9c2b7d1e4f5a6b7c8d9e0f1a2b3c.jpg",
  mime_type: "image/jpeg",
  width: 1200,
  height: 800,
}
const LIBRARY_IMAGE: MediaPickerItem = {
  id: 12,
  url: "/wp-content/uploads/2024/02/quad.jpg",
  file_name: "quad.jpg",
  mime_type: "image/jpeg",
  alt_text: "Students on the quad",
}

vi.mock("./MediaPicker", () => ({
  default: ({
    onSelect,
    title,
  }: {
    onSelect: (item: MediaPickerItem, source: MediaPickerSource) => void
    title?: string
  }) => (
    <div aria-label={title} role="dialog">
      <button onClick={() => onSelect(REPLACEMENT_UPLOAD, "upload")} type="button">
        Upload replacement
      </button>
      <button onClick={() => onSelect(LIBRARY_IMAGE, "library")} type="button">
        Pick library image
      </button>
    </div>
  ),
}))

// The same old image used twice in one article, which is exactly the case
// where replacing "the asset" instead of "this image" would go wrong.
const ARTICLE = [
  "<p>Lede</p>",
  `<figure class="wp-caption"><img src="${OLD_URL}" alt="Riot police at the barricade"><figcaption class="wp-caption-text">Photo by Jane Doe</figcaption></figure>`,
  "<p>Middle</p>",
  `<figure class="wp-caption"><img src="${OLD_URL}" alt="The same scene, later"></figure>`,
].join("")

type SavedImage = { src: string | null; alt: string | null; caption: string | null }

const savedImages = (html: string): SavedImage[] =>
  Array.from(new DOMParser().parseFromString(html, "text/html").querySelectorAll("figure")).map((figure) => ({
    src: figure.querySelector("img")?.getAttribute("src") ?? null,
    alt: figure.querySelector("img")?.getAttribute("alt") ?? null,
    caption: figure.querySelector("figcaption")?.textContent ?? null,
  }))

const renderEditor = async () => {
  const onChange = vi.fn()
  const { container } = render(<TrixEditor onChange={onChange} value={ARTICLE} />)
  const editor = container.querySelector<TrixEditorElement>("trix-editor")
  if (!editor) throw new Error("no trix-editor")
  await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(2))
  return { editor, onChange }
}

// Selecting an image is what puts Trix's attachment toolbar (and our buttons on
// it) up; a mousedown on the figure does it in the browser.
const selectImage = (editor: TrixEditorElement, index: number) => {
  const attachment = editor.editor.getDocument().getAttachments()[index]
  act(() => {
    editor.editor.composition.editAttachment(attachment)
  })
}

const lastSaved = (onChange: ReturnType<typeof vi.fn>) => {
  const calls = onChange.mock.calls
  if (calls.length === 0) throw new Error("onChange never fired")
  return savedImages(calls[calls.length - 1][0] as string)
}

describe("TrixEditor image replacement", () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it("points only the selected image at the uploaded replacement, keeping its alt and caption", async () => {
    const { editor, onChange } = await renderEditor()
    selectImage(editor, 0)

    fireEvent.click(await screen.findByRole("button", { name: "Replace image" }))
    fireEvent.click(await screen.findByRole("button", { name: "Upload replacement" }))

    await waitFor(() =>
      expect(lastSaved(onChange)).toEqual([
        { src: REPLACEMENT_UPLOAD.url, alt: "Riot police at the barricade", caption: "Photo by Jane Doe" },
        // The other use of the old asset is untouched: replacement is per
        // image in this article, never a rewrite of every reference.
        { src: OLD_URL, alt: "The same scene, later", caption: null },
      ]),
    )
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  })

  // An upload or paste-import still in flight sets the image's URL when it
  // lands, which would silently undo a replacement chosen in the meantime.
  it("does not offer replacement while the image is still uploading", async () => {
    const { editor } = await renderEditor()
    const attachment = editor.editor.getDocument().getAttachments()[0]
    act(() => {
      attachment.setUploadProgress(40)
    })
    selectImage(editor, 0)

    expect(await screen.findByRole("button", { name: "Replace image" })).toBeDisabled()
  })

  it("takes a described library image's alt text but keeps the caption", async () => {
    const { editor, onChange } = await renderEditor()
    selectImage(editor, 1)

    fireEvent.click(await screen.findByRole("button", { name: "Replace image" }))
    fireEvent.click(await screen.findByRole("button", { name: "Pick library image" }))

    await waitFor(() =>
      expect(lastSaved(onChange)).toEqual([
        { src: OLD_URL, alt: "Riot police at the barricade", caption: "Photo by Jane Doe" },
        { src: LIBRARY_IMAGE.url, alt: "Students on the quad", caption: null },
      ]),
    )
  })
})
