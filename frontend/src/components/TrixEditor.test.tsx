import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

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

import TrixEditor, { type TrixEditorHandle } from "./TrixEditor"

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

describe("TrixEditor image links", () => {
  it("keeps a link on an image through an edit, but not a link to the image file itself", async () => {
    const onChange = vi.fn()
    const value =
      '<p>Lede</p><figure><a href="https://www.thetriangle.org/article/comic"><img src="https://m.test/comic.png" alt="Comic"></a></figure>' +
      '<figure class="wp-caption"><a href="https://m.test/full.jpg"><img src="https://m.test/photo.jpg" alt="Photo"></a></figure>'
    const { container } = render(<TrixEditor onChange={onChange} value={value} />)
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(2))

    act(() => {
      editor.editor.setSelectedRange([0, 0])
      editor.editor.insertHTML("New ")
    })

    await waitFor(() => expect(onChange).toHaveBeenCalled())
    const saved = new DOMParser().parseFromString(onChange.mock.calls.at(-1)?.[0] as string, "text/html")
    const linkOf = (src: string) => saved.querySelector(`img[src="${src}"]`)?.closest("a")?.getAttribute("href") ?? null
    expect(linkOf("https://m.test/comic.png")).toBe("https://www.thetriangle.org/article/comic")
    expect(linkOf("https://m.test/photo.jpg")).toBeNull()
  })
})

describe("TrixEditor article cards", () => {
  const READ_MORE = "Click Here to Read More"
  const card = (title: string, slug: string, image?: string) =>
    `<h1>${title}</h1>` +
    (image ? `<figure><a href="/article/${slug}"><img src="${image}" alt="${title}"></a></figure>` : "") +
    `<p>${title} excerpt</p><p><a href="/article/${slug}">${READ_MORE}</a></p>`
  const BODY = `<p>Intro</p>${card("Alpha", "alpha", "https://m.test/a.jpg")}${card("Beta", "beta")}<p>Question of the day</p>`

  beforeEach(() => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        disconnect() {}
      },
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const renderCards = async () => {
    const onChange = vi.fn()
    const onEdit = vi.fn<(kind: string, replace: (html: string) => void) => void>()
    const { container } = render(
      <TrixEditor articleCards={{ endLinkText: READ_MORE, onEdit }} onChange={onChange} value={BODY} />,
    )
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await screen.findByRole("button", { name: "Swap article: Beta" })
    return { editor, onChange, onEdit }
  }
  const lastHtml = (onChange: ReturnType<typeof vi.fn>) => String(onChange.mock.calls.at(-1)?.[0])

  it("swaps only the chosen card's blocks, in place, leaving other images alone", async () => {
    // Alpha's image is off-site, so if the swap re-added it the editor would
    // try to re-import it, and delete it when that failed.
    const fetchSpy = vi.fn(() => Promise.reject(new Error("offline")))
    vi.stubGlobal("fetch", fetchSpy)
    const { editor, onChange, onEdit } = await renderCards()

    fireEvent.click(screen.getByRole("button", { name: "Swap article: Beta" }))
    act(() => onEdit.mock.calls[0][1](card("Gamma", "gamma", "/wp-content/uploads/g.jpg")))

    await waitFor(() => expect(lastHtml(onChange)).toContain("Gamma"))
    // Trix's own serialization: jsdom never loads the images, so the saved
    // HTML has no figures to check.
    const saved = editor.value
    expect(saved).not.toContain("Beta")
    const positions = [
      "Intro",
      "Alpha",
      "https://m.test/a.jpg",
      "/article/alpha",
      "Gamma",
      "/wp-content/uploads/g.jpg",
      "Gamma excerpt",
      "/article/gamma",
      "Question of the day",
    ].map((text) => saved.indexOf(text))
    expect(positions.every((p) => p >= 0)).toBe(true)
    expect(positions).toEqual([...positions].sort((a, b) => a - b))
    expect(fetchSpy).not.toHaveBeenCalled()
    await screen.findByRole("button", { name: "Swap article: Gamma" })
  })

  it("is one undo step", async () => {
    const { editor, onChange, onEdit } = await renderCards()
    fireEvent.click(screen.getByRole("button", { name: "Swap article: Beta" }))
    act(() => onEdit.mock.calls[0][1](card("Gamma", "gamma")))
    await waitFor(() => expect(lastHtml(onChange)).toContain("Gamma"))

    act(() => editor.editor.undo())
    await waitFor(() => expect(editor.value).toContain("Beta excerpt"))
    expect(editor.value).not.toContain("Gamma")
  })

  it("refuses to guess when the card was removed while the picker was open", async () => {
    const { editor, onChange, onEdit } = await renderCards()
    fireEvent.click(screen.getByRole("button", { name: "Swap article: Beta" }))
    act(() => editor.editor.loadHTML(`<p>Intro</p>${card("Alpha", "alpha")}`))
    act(() => onEdit.mock.calls[0][1](card("Gamma", "gamma")))

    expect(await screen.findByRole("status")).toHaveTextContent("That article changed")
    expect(onChange.mock.calls.some((call) => String(call[0]).includes("Gamma"))).toBe(false)
  })

  it("shows no swap buttons unless asked to", async () => {
    const { container } = render(<TrixEditor onChange={vi.fn()} value={BODY} />)
    await waitFor(() => expect(container.querySelector("trix-editor h1")).not.toBeNull())
    expect(screen.queryByRole("button", { name: /Swap article/ })).toBeNull()
  })

  // On our media host, so nothing is sideloaded.
  const MOVE_BODY = `<p>Intro</p>${card("Alpha", "alpha", "/wp-content/uploads/a.jpg")}${card("Beta", "beta")}<p>Outro</p>`
  const blockTexts = (editor: TrixEditorElement) =>
    Array.from(editor.children).map((block) =>
      block.querySelector("img") ? "[image]" : (block.textContent ?? "").trim(),
    )

  it("moves an image's whole article card, past the next card, as one undo step", async () => {
    const { container } = render(
      <TrixEditor articleCards={{ endLinkText: READ_MORE, onEdit: vi.fn() }} onChange={vi.fn()} value={MOVE_BODY} />,
    )
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(1))
    const before = blockTexts(editor)
    selectImage(editor, 0)

    fireEvent.click(await screen.findByTitle("Move article down"))

    await waitFor(() =>
      expect(blockTexts(editor)).toEqual([
        "Intro",
        "Beta",
        "Beta excerpt",
        READ_MORE,
        "Alpha",
        "[image]",
        "Alpha excerpt",
        READ_MORE,
        "Outro",
      ]),
    )
    act(() => editor.editor.undo())
    await waitFor(() => expect(blockTexts(editor)).toEqual(before))
  })

  it("still moves just the image block in an ordinary article", async () => {
    const { container } = render(<TrixEditor onChange={vi.fn()} value={MOVE_BODY} />)
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(1))
    selectImage(editor, 0)

    fireEvent.click(await screen.findByTitle("Move image down"))

    await waitFor(() =>
      expect(blockTexts(editor).slice(0, 4)).toEqual(["Intro", "Alpha", "Alpha excerpt", "[image]"]),
    )
  })

  it("keeps an inserted card's image from another host instead of importing it", async () => {
    const fetchSpy = vi.fn(() => Promise.reject(new Error("offline")))
    vi.stubGlobal("fetch", fetchSpy)
    const { editor, onChange, onEdit } = await renderCards()
    fireEvent.click(screen.getByRole("button", { name: "Swap article: Beta" }))
    act(() => onEdit.mock.calls[0][1](card("Gamma", "gamma", "https://delta.test/wp-content/g.jpg")))

    await waitFor(() => expect(lastHtml(onChange)).toContain("Gamma"))
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(editor.value).toContain("https://delta.test/wp-content/g.jpg")
  })

  it("inserts a card after the card the caret is in, not inside it", async () => {
    const handle = { current: null as TrixEditorHandle | null }
    const { container } = render(
      <TrixEditor
        articleCards={{ endLinkText: READ_MORE, onEdit: vi.fn() }}
        handleRef={handle}
        onChange={vi.fn()}
        value={MOVE_BODY}
      />,
    )
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(1))
    // In Alpha's headline.
    const doc = editor.editor.getDocument()
    act(() => editor.editor.setSelectedRange(doc.positionFromLocation({ index: 1, offset: 2 })))

    act(() => handle.current?.insertHTML(card("Gamma", "gamma")))

    await waitFor(() =>
      expect(blockTexts(editor)).toEqual([
        "Intro",
        "Alpha",
        "[image]",
        "Alpha excerpt",
        READ_MORE,
        "Gamma",
        "Gamma excerpt",
        READ_MORE,
        "Beta",
        "Beta excerpt",
        READ_MORE,
        "Outro",
      ]),
    )
  })

  it("swaps the comic, a linked image outside any card", async () => {
    const comic = (slug: string, src: string) =>
      `<figure><a href="https://www.thetriangle.org/article/${slug}"><img src="${src}" alt="${slug}"></a></figure>`
    const onEdit = vi.fn<(kind: string, replace: (html: string) => void) => void>()
    const { container } = render(
      <TrixEditor
        articleCards={{ endLinkText: READ_MORE, onEdit }}
        onChange={vi.fn()}
        value={`${card("Alpha", "alpha")}<p>Random article</p>${comic("old-comic", "/wp-content/uploads/old.png")}`}
      />,
    )
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")

    fireEvent.click(await screen.findByRole("button", { name: "Swap comic" }))
    expect(onEdit.mock.calls[0][0]).toBe("comic")
    act(() => onEdit.mock.calls[0][1](comic("new-comic", "/wp-content/uploads/new.png")))

    await waitFor(() => expect(editor.value).toContain("/wp-content/uploads/new.png"))
    expect(editor.value).not.toContain("old.png")
    expect(editor.value).toContain("https://www.thetriangle.org/article/new-comic")
    expect(blockTexts(editor)).toEqual(["Alpha", "Alpha excerpt", READ_MORE, "Random article", "[image]"])
  })

  it("adds a new comic after the last one, wherever the caret is", async () => {
    const comic = (slug: string) =>
      `<figure><a href="https://www.thetriangle.org/article/${slug}"><img src="/wp-content/uploads/${slug}.png" alt="${slug}"></a></figure>`
    const handle = { current: null as TrixEditorHandle | null }
    const { container } = render(
      <TrixEditor
        articleCards={{ endLinkText: READ_MORE, onEdit: vi.fn() }}
        handleRef={handle}
        onChange={vi.fn()}
        value={`<p>Intro</p>${card("Alpha", "alpha")}${comic("one")}<p>Sign-off</p>`}
      />,
    )
    const editor = container.querySelector<TrixEditorElement>("trix-editor")
    if (!editor) throw new Error("no trix-editor")
    await waitFor(() => expect(editor.editor.getDocument().getAttachments()).toHaveLength(1))
    act(() => editor.editor.setSelectedRange(0))

    act(() => handle.current?.insertHTML(comic("two"), { afterComics: true }))

    await waitFor(() => expect(editor.value).toContain("/wp-content/uploads/two.png"))
    expect(blockTexts(editor)).toEqual(["Intro", "Alpha", "Alpha excerpt", READ_MORE, "[image]", "[image]", "Sign-off"])
    expect(editor.value.indexOf("one.png")).toBeLessThan(editor.value.indexOf("two.png"))
  })
})
