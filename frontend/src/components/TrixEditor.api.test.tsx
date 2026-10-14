import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest"

import TrixEditor, { type TrixEditorApi } from "./TrixEditor"
import { findInvalidEmbeds, trixHtmlToArticle, type QuizCardProblems } from "./trixArticleHtml"

// jsdom's ElementInternals lacks setFormValue and setValidity, which Trix calls.
const nativeAttachInternals = HTMLElement.prototype.attachInternals
beforeAll(() => {
  HTMLElement.prototype.attachInternals = function () {
    return Object.assign(nativeAttachInternals.call(this) ?? {}, {
      setFormValue: () => {},
      setValidity: () => {},
    }) as ElementInternals
  }
})
afterAll(() => {
  HTMLElement.prototype.attachInternals = nativeAttachInternals
})

describe("TrixEditor api", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("reports no pending uploads on a loaded article", async () => {
    const apiRef: { current: TrixEditorApi | null } = { current: null }
    render(<TrixEditor value="<p>Body</p>" onChange={() => {}} apiRef={apiRef} />)
    await waitFor(() => expect(apiRef.current).not.toBeNull())
    expect(apiRef.current?.hasPendingUploads()).toBe(false)
  })

  it("reports a pending upload while a file attachment has no url", async () => {
    // Keeps the upload in flight; jsdom would otherwise fail it and remove it.
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(() => {})
    const apiRef: { current: TrixEditorApi | null } = { current: null }
    render(<TrixEditor value="<p>Body</p>" onChange={() => {}} apiRef={apiRef} />)
    await waitFor(() => expect(apiRef.current).not.toBeNull())
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    act(() => {
      // attachmentForFile, not new Attachment({ file }): Trix only treats a
      // file as attached when it goes through setFile, which this calls.
      editor.editor.insertAttachment(
        window.Trix!.Attachment.attachmentForFile(
          new File([new Uint8Array(4)], "ep.mp3", { type: "audio/mpeg" }),
        ),
      )
    })
    expect(apiRef.current?.hasPendingUploads()).toBe(true)
  })

  it("clears apiRef on unmount", async () => {
    const apiRef: { current: TrixEditorApi | null } = { current: null }
    const { unmount } = render(<TrixEditor value="<p>Body</p>" onChange={() => {}} apiRef={apiRef} />)
    await waitFor(() => expect(apiRef.current).not.toBeNull())
    unmount()
    expect(apiRef.current).toBeNull()
  })
})

describe("embeds in the editor", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  const renderEditor = async (value = "<p>Body</p>") => {
    const onChange = vi.fn()
    render(<TrixEditor value={value} onChange={onChange} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    return { onChange }
  }

  const dispatchFileAccept = (file: File) => {
    const editor = document.querySelector("trix-editor") as HTMLElement
    const event = new Event("trix-file-accept", { bubbles: true, cancelable: true }) as Event & { file: File }
    event.file = file
    act(() => {
      editor.dispatchEvent(event)
    })
    return event
  }

  it("inserts a pasted YouTube link as a card in its own block", async () => {
    const user = userEvent.setup()
    const { onChange } = await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://youtu.be/dQw4w9WgXcQ")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    await waitFor(() => {
      const last = String(onChange.mock.calls.at(-1)?.[0] ?? "")
      expect(last).toMatch(/<div>\[youtube https:\/\/www\.youtube\.com\/watch\?v=dQw4w9WgXcQ\]<\/div>/)
    })
  })

  it("keeps the existing paragraph when an embed is inserted", async () => {
    const user = userEvent.setup()
    const { onChange } = await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://youtu.be/dQw4w9WgXcQ")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    await waitFor(() => expect(String(onChange.mock.calls.at(-1)?.[0] ?? "")).toContain("<div>Body</div>"))
  })

  it("refuses a short link with a specific message", async () => {
    const user = userEvent.setup()
    await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://vm.tiktok.com/ZMabc/")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    expect(await screen.findByText(/open the link in a browser and paste the full url/i)).toBeInTheDocument()
  })

  it("inserts nothing for a short link", async () => {
    const user = userEvent.setup()
    const { onChange } = await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://vm.tiktok.com/ZMabc/")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    await screen.findByText(/open the link in a browser/i)
    expect(onChange.mock.calls.flat().join("")).not.toContain("[tiktok")
  })

  it("refuses an unsupported host and names the accepted ones", async () => {
    const user = userEvent.setup()
    await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://vimeo.com/123")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    expect(await screen.findByText(/youtube, soundcloud, spotify, tiktok or instagram/i)).toBeInTheDocument()
  })

  it("refuses a dropped .wav", async () => {
    await renderEditor()
    dispatchFileAccept(new File([new Uint8Array(8)], "clip.wav", { type: "audio/wav" }))
    expect(await screen.findByText(/only images and mp3/i)).toBeInTheDocument()
  })

  it("starts no upload for a dropped .wav", async () => {
    const send = vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(() => {})
    await renderEditor()
    dispatchFileAccept(new File([new Uint8Array(8)], "clip.wav", { type: "audio/wav" }))
    expect(send).not.toHaveBeenCalled()
  })

  it("prevents Trix's default insert for an .mp3 with an empty browser type", async () => {
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(() => {})
    await renderEditor()
    const event = dispatchFileAccept(new File([new Uint8Array(8)], "Episode.MP3", { type: "" }))
    expect(event.defaultPrevented).toBe(true)
  })

  it("accepts an .mp3 with an empty browser type", async () => {
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(() => {})
    await renderEditor()
    dispatchFileAccept(new File([new Uint8Array(8)], "Episode.MP3", { type: "" }))
    expect(screen.queryByText(/only images and mp3/i)).toBeNull()
  })

  it("opens an embed card link in a new tab without leaving the editor", async () => {
    const { onChange } = await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const url = "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5"
    await act(async () => {
      editor.editor.insertHTML(
        `<figure data-trix-attachment='${JSON.stringify({
          contentType: "application/x-triangle-embed",
          embedProvider: "spotify",
          embedUrl: url,
        })}'></figure>`,
      )
    })
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    const open = vi.spyOn(window, "open").mockImplementation(() => null)
    const link = document.querySelector("trix-editor .embed-card a") as HTMLAnchorElement
    expect(link.getAttribute("href")).toBe(url)
    const followed = fireEvent.click(link)
    expect(followed).toBe(false)
    expect(open).toHaveBeenCalledWith(url, "_blank", "noopener,noreferrer")
  })

  it("shows the audio card while the upload is still in flight", async () => {
    vi.spyOn(XMLHttpRequest.prototype, "send").mockImplementation(() => {})
    await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    await act(async () => {
      editor.editor.insertAttachment(
        window.Trix!.Attachment.attachmentForFile(new File([new Uint8Array(4)], "ep.mp3", { type: "audio/mpeg" })),
      )
    })
    await waitFor(() => expect(document.querySelector("trix-editor .embed-card")?.textContent).toContain("ep.mp3"))
  })

  it("keeps the legacy block of an embed loaded after mount", async () => {
    const onChange = vi.fn()
    const { rerender } = render(<TrixEditor value="" onChange={onChange} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    const legacy = '<p class="media-box">[spotify url="https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5" /]</p>'
    await act(async () => {
      rerender(<TrixEditor value={legacy} onChange={onChange} />)
    })
    const data = document.querySelector("trix-editor figure")?.getAttribute("data-trix-attachment") ?? ""
    expect(data).toContain("embedOriginal")
  })

  it("keeps a loaded audio block whose origin is the media origin, not the CMS origin", async () => {
    const onChange = vi.fn()
    const { rerender } = render(<TrixEditor value="" onChange={onChange} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    const stored = '<div>[audio src="https://media.example.org/wp-content/uploads/2026/10/ep1.mp3" /]</div>'
    await act(async () => {
      rerender(<TrixEditor value={stored} onChange={onChange} />)
    })
    expect(document.querySelector("trix-editor .embed-card")?.textContent).toContain("ep1.mp3")
  })

  it("rebuilds the card of a pasted embed attachment", async () => {
    const { onChange } = await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const hostile = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/x-triangle-embed",
      embedProvider: "spotify",
      embedUrl: "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5",
      content: "<img src=x onerror=alert(1)>",
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(hostile)
    })
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    expect(document.querySelector("trix-editor .embed-card")).not.toBeNull()
  })

  it("never renders the stored content of a pasted embed", async () => {
    const { onChange } = await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    // Trix's sanitizer keeps class, so this survives unless the handler rebuilds the card.
    const hostile = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/x-triangle-embed",
      embedProvider: "spotify",
      embedUrl: "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5",
      content: '<span class="hostile-marker">x</span>',
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(hostile)
    })
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    expect(document.querySelector("trix-editor .hostile-marker")).toBeNull()
  })

  it("drops the embedOriginal of a pasted embed", async () => {
    const { onChange } = await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const pasted = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/x-triangle-embed",
      embedProvider: "spotify",
      embedUrl: "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5",
      embedOriginal: '<p class="media-box">[spotify url="https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5" /]</p>',
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(pasted)
    })
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    expect(document.querySelector("trix-editor figure")?.getAttribute("data-trix-attachment")).not.toContain("embedOriginal")
  })

  it("removes a pasted audio chip from a foreign origin", async () => {
    await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const chip = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "audio/mpeg",
      url: "https://evil.test/wp-content/uploads/a.mp3",
      filename: "a.mp3",
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(chip)
    })
    expect(document.querySelector("trix-editor figure")).toBeNull()
  })

  it("shows an audio card with the filename for an own-origin audio attachment", async () => {
    await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const url = `${window.location.origin}/wp-content/uploads/2026/10/ep1.mp3`
    await act(async () => {
      editor.editor.insertAttachment(
        new window.Trix!.Attachment({ url, href: url, contentType: "audio/mpeg", filename: "ep1.mp3" }),
      )
    })
    await waitFor(() => expect(document.querySelector("trix-editor .embed-card")?.textContent).toContain("ep1.mp3"))
  })

  it("tells the author a foreign-origin audio chip was removed", async () => {
    await renderEditor()
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const chip = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "audio/mpeg",
      url: "https://evil.test/wp-content/uploads/a.mp3",
      filename: "a.mp3",
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(chip)
    })
    expect(await screen.findByText(/pasted audio file was removed/i)).toBeInTheDocument()
  })
})

describe("quizzes in the editor", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  const jsonResponse = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })

  const lastStates = (fn: ReturnType<typeof vi.fn>) => fn.mock.calls.at(-1)?.[0] as Map<number, string> | undefined

  it("inserts a picked quiz as a card and saves the shortcode", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.endsWith("/v1/quizzes?status=published")) return jsonResponse([{ id: 7, slug: "campus", title: "Campus trivia", kind: "trivia", status: "published", updated_at: "", starts: 0, edit_version: 1 }])
      return jsonResponse({ error: "not found" }, 404)
    })
    const user = userEvent.setup()
    const onChange = vi.fn()
    const onQuizStatesChange = vi.fn()
    render(<TrixEditor value="<p>Body</p>" onChange={onChange} onQuizStatesChange={onQuizStatesChange} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    await user.click(screen.getByTitle(/insert a quiz/i))
    await user.click(await screen.findByRole("button", { name: /campus trivia/i }))
    await waitFor(() => {
      const last = String(onChange.mock.calls.at(-1)?.[0] ?? "")
      expect(last).toContain('<p>[quiz id="7"]</p>')
    })
    expect(document.querySelector("trix-editor .embed-card--quiz")?.textContent).toContain("Quiz: Campus trivia")
    expect(lastStates(onQuizStatesChange)?.get(7)).toBe("published")
  })

  it("resolves a loaded quiz card's title and reports its state", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input)
      if (url.endsWith("/v1/quizzes/7")) return jsonResponse({ id: 7, title: "Campus trivia", status: "draft" })
      if (url.endsWith("/v1/quizzes/8")) return jsonResponse({ error: "not found" }, 404)
      return jsonResponse({}, 500)
    })
    const onQuizStatesChange = vi.fn()
    render(<TrixEditor value='<p>[quiz id="7"]</p><p>[quiz id="8"]</p>' onChange={() => {}} onQuizStatesChange={onQuizStatesChange} />)
    await waitFor(() => expect(lastStates(onQuizStatesChange)?.get(7)).toBe("unpublished"))
    expect(lastStates(onQuizStatesChange)?.get(8)).toBe("deleted")
    const cards = Array.from(document.querySelectorAll("trix-editor .embed-card--quiz")).map((c) => c.textContent)
    expect(cards[0]).toContain("Quiz: Campus trivia (unpublished)")
    expect(cards[1]).toContain("Quiz #8 (deleted)")
  })

  it("marks a card unknown when the lookup fails", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("network"))
    const onQuizStatesChange = vi.fn()
    render(<TrixEditor value='<p>[quiz id="7"]</p>' onChange={() => {}} onQuizStatesChange={onQuizStatesChange} />)
    await waitFor(() => expect(lastStates(onQuizStatesChange)?.get(7)).toBe("unknown"))
    expect(document.querySelector("trix-editor .embed-card--quiz")?.textContent).toContain("could not check")
  })

  it("checks quiz cards on the editor document and highlights the ones it flags", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => jsonResponse({ id: 7, title: "Campus trivia", status: "draft" }))
    const apiRef: { current: TrixEditorApi | null } = { current: null }
    const onQuizStatesChange = vi.fn()
    render(<TrixEditor value='<p>[quiz id="7"]</p>' onChange={() => {}} apiRef={apiRef} onQuizStatesChange={onQuizStatesChange} />)
    await waitFor(() => expect(lastStates(onQuizStatesChange)?.get(7)).toBe("unpublished"))
    let problems: QuizCardProblems | undefined
    act(() => {
      problems = apiRef.current?.checkQuizCards(new Map([[7, "unpublished"]]))
    })
    expect(problems?.blocking).toEqual(["Quiz #7 is unpublished. Remove it or publish the quiz first."])
    await waitFor(() => expect(document.querySelector("trix-editor .embed-card--flagged")).not.toBeNull())
  })

  it("replaces a pasted quiz card with an invalid id by the invalid card", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("network"))
    render(<TrixEditor value="<p>Body</p>" onChange={() => {}} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const pasted = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/vnd.triangle.quiz",
      quizId: "abc",
      content: '<span class="hostile-marker">x</span>',
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(pasted)
    })
    await waitFor(() => expect(findInvalidEmbeds(trixHtmlToArticle(editor.value))).toHaveLength(1))
    expect(document.querySelector("trix-editor .hostile-marker")).toBeNull()
  })

  it("rebuilds a pasted quiz card from its id and keeps no stored content", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("network"))
    render(<TrixEditor value="<p>Body</p>" onChange={() => {}} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    const editor = document.querySelector("trix-editor") as TrixEditorElement
    const pasted = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/vnd.triangle.quiz",
      quizId: 7,
      quizOriginal: '<p class="media-box">[quiz id="7" /]</p>',
      content: '<span class="hostile-marker">x</span>',
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(pasted)
    })
    await waitFor(() => expect(document.querySelector("trix-editor .embed-card--quiz")?.textContent).toContain("Quiz #7"))
    expect(document.querySelector("trix-editor .hostile-marker")).toBeNull()
    expect(editor.value).not.toContain("quizOriginal")
  })

  it("does not look a quiz up again when the same document renders again", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async () => jsonResponse({ id: 7, title: "Campus trivia", status: "published" }))
    const value = '<p>[quiz id="7"]</p>'
    const { rerender } = render(<TrixEditor value={value} onChange={() => {}} onQuizStatesChange={() => {}} />)
    await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1))
    await act(async () => {
      rerender(<TrixEditor value={value} onChange={() => {}} onQuizStatesChange={() => {}} />)
    })
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })
})
