import { useCallback, useEffect, useId, useRef, useState } from "react"
import "trix"
import "trix/dist/trix.css"
import "./TrixEditor.css"
import { apiBaseUrl } from "../auth/urls"
import { copyText } from "../lib/clipboard"
import MediaPicker, { type MediaPickerItem } from "./MediaPicker"
import {
  ALIGNMENTS,
  TRIX_ALIGN_CLASS,
  articleHtmlToTrix,
  contentTypeForUrl,
  trixHtmlToArticle,
} from "./trixImageHtml"

// Leave the caption area of an image empty until it has a real caption. Trix's
// default is to fill it with the filename and file size, which reads as a
// caption the author didn't write, and -- because the slot is then never empty
// -- suppresses the "Add a caption…" placeholder that tells them it is editable.
// Non-previewable file attachments are unaffected: Trix forces the name on for
// those, and a file stub with no label would be nothing at all.
if (typeof window !== "undefined" && window.Trix) {
  window.Trix.config.attachments.preview.caption.name = false
  window.Trix.config.attachments.preview.caption.size = false

  // Keep every image in a block of its own. Trix tags previewable attachments
  // with presentation "gallery" by default, and its attachmentGalleryFilter
  // then fuses any run of two or more adjacent ones into a single
  // attachmentGallery block. That block is the unit our reordering works in, so
  // two images placed next to each other became one thing: dragging either
  // moved both, and swapping them with each other was not expressible at all.
  // Nothing here styles galleries, so switching the presentation off costs
  // nothing and leaves the filter with no run to ever match.
  window.Trix.config.attachments.preview.presentation = null

  // Let alignment survive on the attachment piece. Trix's permitted list is
  // ["caption", "presentation"] and removeProhibitedAttributes drops everything
  // else as soon as the piece is built, so `align` only ever lived on the live
  // figure as a class our own effect re-applied. That class was enough to make
  // alignment look preserved until Trix re-rendered the figure from the piece
  // -- which editing a caption does -- at which point the alignment silently
  // vanished from both the editor and the saved article.
  if (!window.Trix.AttachmentPiece.permittedAttributes.includes("align")) {
    window.Trix.AttachmentPiece.permittedAttributes.push("align")
  }
}

// The attachment toolbar is built by Trix as plain DOM, not by React, so its
// icons are hand-built SVG rather than the lucide components used elsewhere.
// These are lucide's own Copy and Check paths, so the button reads the same as
// "Copy article link" in the editor header.
const svgIcon = (paths: string[]): SVGSVGElement => {
  const ns = "http://www.w3.org/2000/svg"
  const svg = document.createElementNS(ns, "svg")
  svg.setAttribute("viewBox", "0 0 24 24")
  svg.setAttribute("fill", "none")
  svg.setAttribute("stroke", "currentColor")
  svg.setAttribute("stroke-width", "2")
  svg.setAttribute("stroke-linecap", "round")
  svg.setAttribute("stroke-linejoin", "round")
  svg.setAttribute("aria-hidden", "true")
  svg.classList.add("trix-icon")
  for (const d of paths) {
    const path = document.createElementNS(ns, "path")
    path.setAttribute("d", d)
    svg.appendChild(path)
  }
  return svg
}

const copyIcon = () => svgIcon([
  "M20 9h-9a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2v-9a2 2 0 0 0-2-2z",
  "M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1",
])

const checkIcon = () => svgIcon(["M20 6 9 17l-5-5"])

// Hosts we may embed directly. A pasted image already served from our own media
// infrastructure needs no sideload — it is the same file we would be copying.
const isOwnMediaUrl = (url: string): boolean => {
  try {
    const resolved = new URL(url, window.location.href)
    if (resolved.origin === window.location.origin) return true
    const apiOrigin = new URL(apiBaseUrl(), window.location.href).origin
    return resolved.origin === apiOrigin
  } catch {
    return false
  }
}

type TrixEditorProps = {
  value: string
  onChange: (html: string) => void
}

function TrixEditor({ value, onChange }: TrixEditorProps) {
  const toolbarId = useId()
  const editorRef = useRef<TrixEditorElement>(null)
  const wrapperRef = useRef<HTMLDivElement>(null)
  // Every HTML string we have emitted since the last load from outside, so we
  // never call loadHTML on our own output -- which would reset the document and
  // drop the caret at the top of the article mid-edit.
  //
  // This has to be a set of everything emitted, not just the most recent one.
  // The effect below is passive, so React can run it with a `value` several
  // keystrokes behind what the editor already holds; a single "last emitted"
  // string has by then moved on, the stale echo fails the comparison, and the
  // editor is reloaded from it. That is the "type fast and your text jumps to
  // the top" bug: each reload rewound the document and reset the caret to 0, so
  // the following keystrokes landed at the start of the article.
  const emittedRef = useRef<Set<string>>(new Set())
  // Bounded so a long editing session (one entry per keystroke) doesn't grow
  // without limit. Re-inserting keeps the set in least-recently-emitted order,
  // so eviction drops the entries least likely to still be in flight.
  const rememberEmitted = useCallback((html: string) => {
    const emitted = emittedRef.current
    emitted.delete(html)
    emitted.add(html)
    while (emitted.size > 100) {
      const oldest = emitted.values().next()
      if (oldest.done) break
      emitted.delete(oldest.value)
    }
  }, [])
  // Caret position captured before the picker steals focus, so the image lands
  // where the author was typing rather than at the top of the document.
  const savedRangeRef = useRef<[number, number] | null>(null)

  const [pickerOpen, setPickerOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    // An echo of our own output, however far behind. Reloading would only undo
    // edits the author has already made.
    if (emittedRef.current.has(value)) return
    // A genuine load from outside: the editor is about to hold exactly this, so
    // nothing emitted before it can still be worth honouring.
    emittedRef.current.clear()
    emittedRef.current.add(value)
    editor.editor.loadHTML(articleHtmlToTrix(value))
  }, [value])

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const handleChange = () => {
      // Emit the semantic markup we persist, not Trix's internal attachment
      // format. What we remember has to be the *converted* HTML: it is compared
      // against the incoming value prop to decide whether to reload the editor,
      // and reloading on our own output would reset the caret on every
      // keystroke.
      const html = trixHtmlToArticle(editor.value)
      rememberEmitted(html)
      onChange(html)
    }

    editor.addEventListener("trix-change", handleChange)
    return () => { editor.removeEventListener("trix-change", handleChange) }
  }, [onChange, rememberEmitted])

  // Reflect each attachment's stored alignment onto the live figure so our CSS
  // can style it. Alignment round-trips as a data-trix-attributes value (Trix
  // rebuilds attachment figures from that JSON and would discard a bare class),
  // so the class has to be re-applied after every change.
  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const applyAlignment = () => {
      for (const figure of Array.from(editor.querySelectorAll("figure[data-trix-attributes]"))) {
        let align: string | undefined
        try {
          const parsed = JSON.parse(figure.getAttribute("data-trix-attributes") ?? "{}") as { align?: string }
          align = parsed.align
        } catch {
          continue
        }
        for (const candidate of ALIGNMENTS) {
          figure.classList.toggle(TRIX_ALIGN_CLASS[candidate], align === candidate)
        }
      }
    }

    applyAlignment()
    editor.addEventListener("trix-change", applyAlignment)
    return () => { editor.removeEventListener("trix-change", applyAlignment) }
  }, [])

  // Handles every way an image enters the document. Trix fires
  // trix-attachment-add for all of them, and which branch runs depends on what
  // the attachment carries:
  //
  //   • a File — a dropped/chosen file, or an image pasted straight off the
  //     clipboard (a screenshot). Uploaded via XHR, which unlike fetch exposes
  //     progress events so Trix can draw its progress bar.
  //   • a remote URL and no File — rich text pasted from another site. Copied
  //     into our own library server-side so the article does not hotlink.
  //   • one of our own URLs — inserted from the picker; nothing to do.
  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    // A failed attachment is removed rather than left behind. Its preview is a
    // blob: URL that exists only in this tab: it looks like a working image,
    // survives no reload, and is dropped by the serializer, so leaving it in
    // place invites the author to publish an article whose image silently
    // isn't there. Removing it and saying why is the honest outcome.
    const failAttachment = (attachment: TrixAttachment, message: string) => {
      attachment.setUploadProgress(100)
      attachment.remove()
      setNotice(message)
    }

    const uploadFile = (attachment: TrixAttachment, file: File) => {
      const xhr = new XMLHttpRequest()
      xhr.open("POST", `${apiBaseUrl()}/v1/media`, true)
      // The upload endpoint is session-authenticated, and the API commonly runs
      // on a different origin than the CMS, where XHR omits cookies by default.
      xhr.withCredentials = true

      xhr.upload.onprogress = (progressEvent: ProgressEvent) => {
        if (progressEvent.lengthComputable) {
          attachment.setUploadProgress((progressEvent.loaded / progressEvent.total) * 100)
        }
      }

      xhr.onload = () => {
        if (xhr.status !== 201) {
          let detail = `upload failed (${String(xhr.status)})`
          try {
            const body = JSON.parse(xhr.responseText) as { error?: string }
            if (body.error?.trim()) detail = body.error.trim()
          } catch {
            // Non-JSON error body; the status code is all we can report.
          }
          failAttachment(attachment, `Could not add ${file.name}: ${detail}`)
          return
        }
        try {
          const { url } = JSON.parse(xhr.responseText) as { url: string }
          // href only for non-images. Trix wraps an attachment carrying an href
          // in an <a>, which for a previewable image swallows every click on the
          // figure -- including the caption field. A file stub, by contrast, has
          // nothing to edit and a download link is the whole point of it.
          attachment.setAttributes(file.type.startsWith("image/") ? { url } : { url, href: url })
          attachment.setUploadProgress(100)
        } catch {
          failAttachment(attachment, `Could not add ${file.name}: unexpected server response.`)
        }
      }

      xhr.onerror = () => {
        failAttachment(attachment, `Could not add ${file.name}: the upload did not reach the server.`)
      }

      const formData = new FormData()
      formData.append("file", attachment.file ?? file)
      xhr.send(formData)
    }

    // Pasting rich text from another site brings <img> tags pointing at that
    // site. Embedding them as-is would leave the published article depending on
    // a third party's server, so take our own copy instead. The fetch is done
    // server-side because the browser cannot read cross-origin image bytes.
    const sideloadRemote = (attachment: TrixAttachment, sourceUrl: string) => {
      attachment.setUploadProgress(5)
      fetch(`${apiBaseUrl()}/v1/media/fetch`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: sourceUrl }),
      })
        .then(async (response) => {
          if (!response.ok) {
            let detail = `import failed (${String(response.status)})`
            try {
              const body = (await response.json()) as { error?: string }
              if (body.error?.trim()) detail = body.error.trim()
            } catch {
              // Fall through to the status-code message.
            }
            throw new Error(detail)
          }
          return (await response.json()) as { url: string }
        })
        .then(({ url }) => {
          // No href: this path only ever handles pasted images. See uploadFile.
          attachment.setAttributes({ url })
          attachment.setUploadProgress(100)
        })
        .catch((err: unknown) => {
          // Unlike a failed upload there is no local copy to fall back on, so
          // the pasted image cannot be kept in any usable form.
          failAttachment(
            attachment,
            `Could not import the pasted image: ${err instanceof Error ? err.message : "import failed"}`,
          )
        })
    }

    const handleAttachmentAdd = (event: Event) => {
      const { attachment } = event as TrixAttachmentAddEvent

      if (attachment.file) {
        uploadFile(attachment, attachment.file)
        return
      }

      const url = typeof attachment.getAttribute("url") === "string" ? String(attachment.getAttribute("url")) : ""
      if (!url) return
      // Already ours (picker insert, or an image copied from another article in
      // this CMS) — re-importing would just duplicate the file.
      if (isOwnMediaUrl(url)) return
      // A blob:/data: URL has no server to fetch from; it arrives with a File
      // in every path we support, so reaching here means there is nothing to do.
      if (url.startsWith("blob:") || url.startsWith("data:")) return

      sideloadRemote(attachment, url)
    }

    editor.addEventListener("trix-attachment-add", handleAttachmentAdd)
    return () => { editor.removeEventListener("trix-attachment-add", handleAttachmentAdd) }
  }, [])

  // Insert a library image at the caret. Alt text comes from the library record,
  // so an image described once is described everywhere it is used.
  const insertFromLibrary = useCallback((item: MediaPickerItem) => {
    setPickerOpen(false)
    const editor = editorRef.current
    const Trix = window.Trix
    if (!editor || !Trix) return

    editor.focus()
    if (savedRangeRef.current) {
      editor.editor.setSelectedRange(savedRangeRef.current)
      savedRangeRef.current = null
    }

    // Deliberately no href -- see uploadFile. The saved article is a plain
    // <figure><img> either way, so the link would only ever have existed inside
    // the editor, where it fights with selecting and captioning the image.
    const attachment = new Trix.Attachment({
      url: item.url,
      contentType: item.mime_type || contentTypeForUrl(item.url),
      filename: item.alt_text || item.file_name,
      alt: item.alt_text ?? "",
      ...(item.width ? { width: item.width } : {}),
      ...(item.height ? { height: item.height } : {}),
      // Trix's previewablePattern excludes extension-less URLs, which would
      // render a library image as a file stub instead of a preview.
      previewable: true,
    })
    editor.editor.insertAttachment(attachment)

    if (!item.alt_text) {
      setNotice(`Inserted ${item.file_name}, which has no alt text. Add it in the Media library.`)
    }
  }, [])

  const openPicker = useCallback(() => {
    const editor = editorRef.current
    // Captured before the modal takes focus; restored on insert.
    savedRangeRef.current = editor ? editor.editor.getSelectedRange() : null
    setPickerOpen(true)
  }, [])

  // Image selection and drag-to-rearrange. All overlay DOM lives in the wrapper
  // *outside* Trix's contenteditable so Trix doesn't overwrite it via its
  // MutationObserver.
  //
  // There is deliberately no resize or alignment gesture here. The public site
  // sizes article images entirely in CSS (#article figure img { width: 100% }),
  // which overrides anything an author sets, so both were controls that appeared
  // to work in the editor and changed nothing on the published page. Reordering
  // is kept because it does survive. Alignment already stored on legacy
  // WordPress content is still preserved through a save -- it just can no longer
  // be set from here.
  useEffect(() => {
    const editor = editorRef.current
    const wrapper = wrapperRef.current
    if (!editor || !wrapper) return

    // Drop indicator (a thin horizontal bar shown only during an active drag).
    const dropIndicator = document.createElement("div")
    dropIndicator.className = "trix-drop-indicator"
    dropIndicator.style.display = "none"
    wrapper.appendChild(dropIndicator)

    let activeFigure: HTMLElement | null = null
    // True from the pointerdown on a figure until the pointer is released,
    // whether or not it has travelled far enough to count as a drag yet.
    let gestureActive = false
    // Per-gesture cleanup set while a drag is in progress so the outer effect's
    // teardown can abort it on unmount (prevents leaked document listeners and
    // a stuck `grabbing` cursor).
    let activeGestureCleanup: (() => void) | null = null

    const selectFigure = (figure: HTMLElement | null) => {
      if (activeFigure && activeFigure !== figure) {
        activeFigure.classList.remove("attachment--selected")
      }
      activeFigure = figure
      if (figure) figure.classList.add("attachment--selected")
    }

    // ── Drag-to-rearrange ──────────────────────────────────────────────────
    // A drop position is a *boundary* between top-level blocks, identified by
    // the index the dragged block would occupy: boundary i sits above block i,
    // and boundary blocks.length sits below the last one.
    type DropTarget = { index: number }

    const DRAG_THRESHOLD_PX = 5

    const boundaryY = (blocks: HTMLElement[], index: number): number => {
      const rect = (blocks[index === 0 ? 0 : index - 1]).getBoundingClientRect()
      return index === 0 ? rect.top : rect.bottom
    }

    // ── Auto-scroll while dragging ─────────────────────────────────────────
    // How close to the edge the pointer has to get before the view starts
    // moving, and the fastest it will go, per frame.
    const AUTO_SCROLL_EDGE_PX = 80
    const AUTO_SCROLL_MAX_PX = 22

    // Whatever actually scrolls the editor: an overflowing ancestor if the page
    // puts the article in its own pane, otherwise the window. Resolved per drag
    // rather than once, since the layout an editor sits in can change.
    const scrollContainer = (): HTMLElement | null => {
      let current = editor.parentElement
      while (current && current !== document.body) {
        const overflowY = window.getComputedStyle(current).overflowY
        if (/(auto|scroll|overlay)/.test(overflowY) && current.scrollHeight > current.clientHeight) {
          return current
        }
        current = current.parentElement
      }
      return null
    }

    // Where the dragged block would land, as a boundary index -- or null if it
    // cannot go anywhere from here.
    //
    // Only boundaries clear of the dragged block itself are candidates: the two
    // touching it leave the document exactly as it was. They used to be offered,
    // and since an image block is as tall as the image, the pointer spent the
    // whole of a short drag inside them -- the indicator appeared, tracked the
    // pointer, and then the drop did nothing. Moving an image at all meant
    // dragging clear past its own block and half of the next one, with the
    // indicator claiming otherwise the entire way. That is the bug this shape
    // exists to remove: every boundary drawn is now one that moves something.
    //
    // Which side to look at is decided by the pointer against the block's own
    // midpoint, not by nearest-boundary-overall. Nearest alone inverts at the
    // ends of the document: dragging the first image upwards has no boundary
    // above it, and the closest one anywhere is the gap *below* its neighbour,
    // so the image jumped downwards in answer to an upwards drag. Picking the
    // side first means an upwards drag either moves the image up or does
    // nothing, never the opposite.
    const findDropIndex = (clientY: number, fromIndex: number): number | null => {
      const blocks = Array.from(editor.children) as HTMLElement[]
      if (blocks.length === 0) return null

      const source = fromIndex >= 0 ? blocks[fromIndex] : null
      let first = 0
      let last = blocks.length
      if (source) {
        const rect = source.getBoundingClientRect()
        if (clientY < rect.top + rect.height / 2) last = fromIndex - 1
        else first = fromIndex + 2
      }

      let best: number | null = null
      let bestDist = Infinity
      for (let index = first; index <= last; index++) {
        const dist = Math.abs(clientY - boundaryY(blocks, index))
        if (dist < bestDist) {
          bestDist = dist
          best = index
        }
      }
      return best
    }

    const positionDropIndicator = (target: DropTarget) => {
      const wrapperRect = wrapper.getBoundingClientRect()
      const editorRect = editor.getBoundingClientRect()
      const y = boundaryY(Array.from(editor.children) as HTMLElement[], target.index)

      const editorLeft = editorRect.left - wrapperRect.left
      const editorWidth = editorRect.width
      const indicatorWidth = Math.min(editorWidth * 0.4, 240)

      dropIndicator.style.display = "block"
      dropIndicator.style.top = `${(y - wrapperRect.top).toString()}px`
      dropIndicator.style.left = `${(editorLeft + (editorWidth - indicatorWidth) / 2).toString()}px`
      dropIndicator.style.width = `${indicatorWidth.toString()}px`
    }

    // The top-level child of the editor that contains this figure. Trix renders
    // one element per document block, so these are the units a move reorders.
    const blockContaining = (node: HTMLElement): HTMLElement | null => {
      let current: HTMLElement = node
      while (current.parentElement && current.parentElement !== editor) current = current.parentElement
      return current.parentElement === editor ? current : null
    }

    // Re-resolve a figure by id. The element captured when a gesture started may
    // already be detached: selecting an attachment -- which Trix does on the very
    // mousedown that starts a drag -- re-renders it, so an identical figure has
    // taken its place. Anything that asks "which block is this image in?" has to
    // go through here or it gets the answer for a corpse.
    const liveFigure = (figure: HTMLElement): HTMLElement | null => {
      if (figure.isConnected) return figure
      const trixId = figure.getAttribute("data-trix-id")
      return trixId ? editor.querySelector<HTMLElement>(`[data-trix-id="${trixId}"]`) : null
    }

    // Index of the top-level block holding this figure, or -1.
    const blockIndexOf = (figure: HTMLElement): number => {
      const live = liveFigure(figure)
      const block = live ? blockContaining(live) : null
      return block ? (Array.from(editor.children) as HTMLElement[]).indexOf(block) : -1
    }

    // Put an attachment into Trix's "being edited" state -- the state that
    // shows its caption field and its toolbar. Trix only enters it from a
    // mousedown of its own, which is no help either when that mousedown's
    // selection gets reset (see onUp) or after a move has replaced the element.
    const editAttachmentForFigure = (figure: Element | null | undefined) => {
      const trixId = figure?.getAttribute("data-trix-id")
      if (!trixId) return
      const attachment = editor.editor
        .getDocument()
        .getAttachments()
        .find((a) => String(a.id) === trixId)
      if (attachment) editor.editor.composition.editAttachment(attachment)
    }

    // Move the figure's whole block to sit before or after the target block.
    //
    // This reorders Trix's serialized HTML and reloads it, rather than splicing
    // the live document through Trix's mutation APIs. The obvious approach --
    // remove the attachment, then insert it at a captured DOM Range -- cannot
    // work: the Attachment objects on editor.getDocument() are plain models
    // with no remove() of their own (only the ManagedAttachment passed to
    // trix-attachment-add has one), and any removal re-renders the blocks the
    // captured Range was anchored to. Reordering the serialization sidesteps
    // both problems and is order-of-blocks-in, order-of-blocks-out.
    //
    // A move is one undo step that restores the whole document rather than a
    // fine-grained edit, which for a whole-block move is what the author would
    // expect to get back anyway.
    const commitMove = (figure: HTMLElement, target: DropTarget) => {
      const liveBlocks = Array.from(editor.children) as HTMLElement[]
      const from = blockIndexOf(figure)
      if (from < 0) return
      if (target.index < 0 || target.index > liveBlocks.length) return

      const serialized = Array.from(
        new DOMParser().parseFromString(editor.value, "text/html").body.children,
      )
      // The serialized blocks line up with the rendered ones by index. If that
      // ever stops holding, bail rather than reorder the wrong block.
      if (serialized.length !== liveBlocks.length) return

      // Where the block lands once it has been lifted out of the list.
      let insertAt = target.index
      if (from < insertAt) insertAt -= 1
      if (insertAt === from) return

      const [moved] = serialized.splice(from, 1)
      serialized.splice(insertAt, 0, moved)

      // Take the attachment editor down before swapping the document out from
      // under it. Its controller holds a reference to the figure it was
      // installed on; reloading leaves it pointing at a detached element and
      // Trix's own click handling then throws on the next click on any image
      // (getRangeOfAttachment on an attachment the document no longer has).
      editor.editor.composition.stopEditingAttachment()

      // Swap the document in under a recorded undo entry, rather than through
      // loadHTML. loadHTML routes to Editor#loadSnapshot, which replaces the
      // whole UndoManager -- so a move was not merely un-undoable, it silently
      // threw away every undo step the author had built up before it.
      // recordUndoEntry snapshots the current document and selection onto the
      // stack, and Composition#setDocument then mutates without disturbing it.
      const Trix = window.Trix
      if (!Trix) return
      const movedDocument = Trix.HTMLParser.parse(serialized.map((block) => block.outerHTML).join(""), {
        referenceElement: editor,
      }).getDocument()

      editor.editor.recordUndoEntry("Move Image")
      editor.editor.composition.setDocument(movedDocument)

      // Re-select the image at its new home. Without this every nudge costs the
      // author the selection -- and with it the toolbar they are clicking --
      // so moving an image three blocks would mean three round trips to it.
      //
      // Two frames, not one. One frame is enough for the element to exist, but
      // not for Trix to have finished rendering the reloaded document, and
      // re-installing the attachment editor into that half-settled state leaves
      // its element-to-attachment mapping stale: the next DOM mutation anywhere
      // in the editor makes Trix re-parse into a document whose attachments no
      // longer match the data-trix-id attributes still on the figures, and from
      // then on every click on an image throws inside Trix. The alignment
      // effect above mutates figure classes on each change, so this is a
      // reachable state, not a theoretical one.
      requestAnimationFrame(() => { requestAnimationFrame(() => {
        if (!editor.isConnected) return
        editAttachmentForFigure(editor.children[insertAt]?.querySelector("[data-trix-id]"))
      }) })
    }

    // Note the absence of preventDefault on the pointerdown itself. Suppressing
    // the default is what a drag needs (it stops the browser turning the
    // gesture into a text selection), but on a plain click it also blocks the
    // native focus/selection that Trix uses to select the attachment and raise
    // its caption field and toolbar. So the default is left alone until the
    // pointer has actually travelled far enough to be a drag.
    const beginDrag = (figure: HTMLElement, downEvent: PointerEvent) => {
      const pointerId = downEvent.pointerId
      const startX = downEvent.clientX
      const startY = downEvent.clientY
      let dragging = false
      let captured = false
      let dropTarget: DropTarget | null = null
      // The pointer's last known position, which auto-scroll keeps re-reading:
      // while the view is moving under a stationary mouse there are no further
      // pointermove events, but the block under that unchanged position changes
      // every frame.
      let lastClientY = downEvent.clientY
      let scroller: HTMLElement | null = null
      let autoScrollFrame: number | null = null

      // Recomputed on every move: Trix re-renders the figure during the drag,
      // and the indicator must exclude the boundaries around wherever the
      // block is *now*, or it offers no-op drops again.
      const updateDropTarget = () => {
        const index = findDropIndex(lastClientY, blockIndexOf(figure))
        if (index === null) {
          dropIndicator.style.display = "none"
          dropTarget = null
          return
        }
        dropTarget = { index }
        positionDropIndicator(dropTarget)
      }

      // Scroll the view when the pointer is held near its top or bottom edge,
      // speeding up the closer to the edge it gets. Without this, a drop target
      // that starts off screen is unreachable: the gesture is driven by the
      // pointer alone, and a long article is taller than the window, so an image
      // could only ever be moved as far as the visible page.
      const autoScrollStep = () => {
        autoScrollFrame = null
        if (!dragging) return

        const bounds = scroller
          ? scroller.getBoundingClientRect()
          : { top: 0, bottom: window.innerHeight }
        let delta = 0
        if (lastClientY < bounds.top + AUTO_SCROLL_EDGE_PX) {
          delta = -(bounds.top + AUTO_SCROLL_EDGE_PX - lastClientY)
        } else if (lastClientY > bounds.bottom - AUTO_SCROLL_EDGE_PX) {
          delta = lastClientY - (bounds.bottom - AUTO_SCROLL_EDGE_PX)
        }

        if (delta !== 0) {
          const speed = Math.min(AUTO_SCROLL_MAX_PX, Math.abs(delta) / AUTO_SCROLL_EDGE_PX * AUTO_SCROLL_MAX_PX)
          const by = Math.sign(delta) * Math.max(2, speed)
          if (scroller) scroller.scrollTop += by
          else window.scrollBy(0, by)
          // The document just moved under the pointer, so the boundary the
          // pointer now sits at is a different one.
          updateDropTarget()
        }

        // Keep polling even when standing still, so pushing back into the edge
        // zone resumes scrolling without needing a fresh mousemove.
        autoScrollFrame = requestAnimationFrame(autoScrollStep)
      }

      const enterDragMode = () => {
        dragging = true

        // Take ownership of the pointer stream for the rest of the gesture.
        // This is the whole reason the gesture is on pointer events: with plain
        // mouse events, anything that starts a native drag session mid-gesture
        // (see the dragstart guard below) makes the browser stop delivering
        // mousemove and -- fatally -- mouseup, so the release was never seen and
        // nothing was ever committed. Capture is retargeted to the wrapper
        // rather than the figure because Trix re-renders the figure during a
        // drag; capturing on an element that then leaves the document drops the
        // capture with it. Capture is taken here and not on pointerdown so that
        // a plain click still reaches Trix untouched -- capturing at pointerdown
        // retargets the compatibility mousedown too, which is the event Trix
        // selects the attachment from.
        try {
          wrapper.setPointerCapture(pointerId)
          captured = true
        } catch {
          // The pointer is already gone (released between the move and here).
          // Nothing to hold; the gesture ends at the next up or cancel.
        }

        liveFigure(figure)?.classList.add("attachment--dragging")
        document.body.style.cursor = "grabbing"
        // Drop whatever text selection the un-prevented pointerdown started, so
        // the drag doesn't paint a selection highlight across the article -- and
        // so there is no selection left for the browser to want to drag.
        window.getSelection()?.removeAllRanges()
        scroller = scrollContainer()
        autoScrollFrame = requestAnimationFrame(autoScrollStep)
      }

      const onMove = (moveEvent: PointerEvent) => {
        if (moveEvent.pointerId !== pointerId) return
        lastClientY = moveEvent.clientY
        if (!dragging) {
          const dx = Math.abs(moveEvent.clientX - startX)
          const dy = Math.abs(moveEvent.clientY - startY)
          if (dx < DRAG_THRESHOLD_PX && dy < DRAG_THRESHOLD_PX) return
          enterDragMode()
        }
        // Now that this is a drag, keep the browser from extending a text
        // selection under the pointer.
        moveEvent.preventDefault()
        updateDropTarget()
      }

      // Idempotent state-restorer, invoked from onUp on normal release, from
      // onCancel if the browser takes the pointer away, AND from the outer
      // effect's cleanup if the component unmounts mid-drag.
      const cleanup = () => {
        gestureActive = false
        dragging = false
        if (autoScrollFrame !== null) {
          cancelAnimationFrame(autoScrollFrame)
          autoScrollFrame = null
        }
        document.removeEventListener("pointermove", onMove)
        document.removeEventListener("pointerup", onUp)
        document.removeEventListener("pointercancel", onCancel)
        if (captured) {
          captured = false
          if (wrapper.hasPointerCapture(pointerId)) wrapper.releasePointerCapture(pointerId)
        }
        document.body.style.cursor = ""
        liveFigure(figure)?.classList.remove("attachment--dragging")
        dropIndicator.style.display = "none"
      }

      // The pointer was taken away mid-gesture -- a touch turning into a scroll,
      // the window losing the device. Abandon the move rather than committing to
      // wherever the indicator happened to be.
      const onCancel = (cancelEvent: PointerEvent) => {
        if (cancelEvent.pointerId !== pointerId) return
        activeGestureCleanup = null
        cleanup()
      }

      const onUp = (upEvent: PointerEvent) => {
        if (upEvent.pointerId !== pointerId) return
        activeGestureCleanup = null
        // Read before cleanup, which clears `dragging` to stop the auto-scroll
        // loop -- reading after it would make every drop look like a click.
        const wasDragging = dragging
        const releasedOn = dropTarget
        cleanup()
        if (wasDragging && releasedOn) {
          commitMove(figure, releasedOn)
        } else if (!wasDragging) {
          selectFigure(figure)
          // Re-assert Trix's own attachment selection. Trix makes it on
          // mousedown, but when the click is what focuses the editor in the
          // first place, the focus that follows resets the selection and takes
          // the attachment toolbar back down with it -- so the first click on
          // an image in a freshly loaded editor appeared to do nothing, and it
          // took a second click to get at Remove or the move buttons. Trix
          // ignores this when the attachment is already the one being edited.
          editAttachmentForFigure(figure)
        }
      }

      gestureActive = true
      // Listening on the document rather than on the capture target: captured
      // events are retargeted to the wrapper but still bubble from there, so one
      // set of listeners covers both halves of the gesture (before the threshold,
      // when there is no capture, and after it, when there is).
      document.addEventListener("pointermove", onMove)
      document.addEventListener("pointerup", onUp)
      document.addEventListener("pointercancel", onCancel)
      activeGestureCleanup = cleanup
    }

    // Trix's own attachment chrome: the caption (static <figcaption>, and the
    // <textarea> that replaces it while editing) and the floating toolbar that
    // carries Remove. These have to keep their native mousedown, because that
    // is the event Trix uses to select the attachment and focus the caption
    // field. Running beginDrag over them instead -- it calls preventDefault,
    // and its mouseup pulls focus back to the editor body -- is what made the
    // caption impossible to click into.
    const TRIX_ATTACHMENT_CHROME = "figcaption, .attachment__caption, .attachment__toolbar"

    // ── Selection / drag entry point (capture phase so Trix can't preempt) ──
    const onEditorPointerDown = (event: PointerEvent) => {
      // Mouse and pen only. Touch is deliberately left to the browser so that a
      // swipe starting on an image still scrolls the article; the move buttons
      // on the attachment toolbar are how an image gets reordered without a
      // pointing device.
      if (event.pointerType === "touch") return
      if (!event.isPrimary || event.button !== 0) return
      const target = event.target as HTMLElement
      const figure = target.closest(".attachment--preview") as HTMLElement | null
      if (!figure) return
      if (target.closest(TRIX_ATTACHMENT_CHROME)) return

      // Take the image out of the browser's native drag-and-drop before it can
      // consider starting a session of its own: an <img> is draggable by default,
      // and once a native drag begins the pointer stream is the browser's, not
      // ours. Re-applied on every gesture because Trix rebuilds attachment
      // figures from its own model and drops attributes it did not put there.
      for (const image of figure.querySelectorAll("img")) image.draggable = false

      beginDrag(figure, event)
    }
    const onDocumentPointerDown = (event: PointerEvent) => {
      const target = event.target as HTMLElement
      // Don't deselect when the click lands on another figure; that has its own
      // selection semantics.
      if (target.closest(".attachment--preview")) return
      selectFigure(null)
    }

    // ── Move buttons on the attachment toolbar ─────────────────────────────
    // Dragging is workable in a short article and miserable in a long one,
    // where the drop target is off screen and the page has to auto-scroll.
    // These step the image over one block at a time, need no aim, and are the
    // only way to move an image without a mouse. They ride along on the toolbar
    // Trix already shows over a selected attachment, next to Remove.

    // One step in `direction` as a boundary index: up is the boundary above the
    // previous block, down is the boundary below the next one. Null at the ends,
    // where there is nothing to swap with.
    const stepTarget = (figure: HTMLElement, direction: "up" | "down"): DropTarget | null => {
      const from = blockIndexOf(figure)
      if (from < 0) return null
      const index = direction === "up" ? from - 1 : from + 2
      return index >= 0 && index <= editor.children.length ? { index } : null
    }

    const onBeforeToolbar = (event: Event) => {
      const { toolbar, attachment } = event as TrixAttachmentToolbarEvent
      const figure = editor.querySelector<HTMLElement>(`[data-trix-id="${String(attachment.id)}"]`)
      // Only previewable attachments are positioned as their own block; a file
      // stub sits inline in a paragraph, where "move up a block" is meaningless.
      if (!figure?.classList.contains("attachment--preview")) return

      const group = document.createElement("span")
      group.className = "trix-button-group trix-button-group--move"

      for (const direction of ["up", "down"] as const) {
        const button = document.createElement("button")
        button.type = "button"
        button.className = `trix-button trix-button--move trix-button--move-${direction}`
        button.title = direction === "up" ? "Move image up" : "Move image down"
        button.textContent = direction === "up" ? "↑" : "↓"
        // Nothing to swap with at the top or bottom of the document.
        button.disabled = !stepTarget(figure, direction)
        // Suppress mousedown so pressing the button doesn't collapse the
        // attachment selection (and tear down this very toolbar) before the
        // click lands.
        button.addEventListener("mousedown", (mouseEvent) => { mouseEvent.preventDefault() })
        button.addEventListener("click", (clickEvent) => {
          clickEvent.preventDefault()
          const target = stepTarget(figure, direction)
          if (target) commitMove(figure, target)
        })
        group.appendChild(button)
      }

      // Copy the image's URL, matching the "Copy article link" button in the
      // editor header: same clipboard helper (which falls back to execCommand,
      // since the CMS is served over plain HTTP where navigator.clipboard does
      // not exist) and the same swap-to-a-tick confirmation.
      const url = typeof attachment.getAttribute("url") === "string" ? String(attachment.getAttribute("url")) : ""
      if (url) {
        const copyGroup = document.createElement("span")
        copyGroup.className = "trix-button-group trix-button-group--copy"

        const copyButton = document.createElement("button")
        copyButton.type = "button"
        copyButton.className = "trix-button trix-button--copy-url"
        copyButton.title = `Copy image URL (${url})`
        copyButton.appendChild(copyIcon())

        let resetIcon: number | undefined
        copyButton.addEventListener("mousedown", (mouseEvent) => { mouseEvent.preventDefault() })
        copyButton.addEventListener("click", (clickEvent) => {
          clickEvent.preventDefault()
          void copyText(url).then((copied) => {
            if (!copied) {
              setNotice("Could not copy the image URL. Your browser blocked clipboard access.")
              return
            }
            copyButton.replaceChildren(checkIcon())
            copyButton.classList.add("trix-button--copied")
            window.clearTimeout(resetIcon)
            resetIcon = window.setTimeout(() => {
              // The toolbar is torn down whenever the image is deselected, so
              // by the time this fires the button may be long gone.
              if (!copyButton.isConnected) return
              copyButton.replaceChildren(copyIcon())
              copyButton.classList.remove("trix-button--copied")
            }, 1500)
          })
        })

        copyGroup.appendChild(copyButton)
        toolbar.querySelector(".trix-button-row")?.appendChild(copyGroup)
      }

      toolbar.querySelector(".trix-button-row")?.appendChild(group)
    }

    // With the pointerdown default no longer suppressed, the browser is free to
    // start its own native drag, which would race our rearrange gesture and can
    // drop the image into another window entirely. Once a native drag begins
    // the browser stops sending pointer events, so our gesture goes dead and the
    // image simply stays where it was.
    //
    // Keying this off the event target alone is not enough. Trix selects the
    // attachment on the same mousedown that starts the gesture, so what the
    // browser goes on to drag is the *selection*, and the dragstart it fires
    // for that is targeted at whatever node the selection is anchored in --
    // routinely the editor or a text block rather than anything inside the
    // figure. So suppress unconditionally while our own gesture is in flight,
    // and keep the target test for the rest of the time (a native image drag
    // out of the article is never something we want). Capture phase on the
    // document, because Trix has dragstart handlers of its own on the editor.
    //
    // This guard alone is not enough, and used not to be the only problem. In
    // Firefox on Windows and macOS a native drag session was starting anyway and
    // taking the event stream with it: the gesture began, the drop indicator
    // appeared and tracked the pointer, and the release was then never delivered,
    // so nothing was ever committed -- an image that could be picked up and never
    // put down. It went unnoticed for a while because Firefox on Linux keeps
    // delivering input during a drag session, so the same build worked there.
    // It is defended in three places now, and this is the last of them: the
    // images are marked non-draggable when a gesture starts (onEditorPointerDown),
    // the pointer is captured once the gesture is really a drag (enterDragMode),
    // and any dragstart that still gets raised is cancelled here.
    const onDragStart = (event: DragEvent) => {
      if (gestureActive || (event.target as HTMLElement | null)?.closest(".attachment--preview")) {
        event.preventDefault()
      }
    }

    document.addEventListener("dragstart", onDragStart, true)
    editor.addEventListener("trix-attachment-before-toolbar", onBeforeToolbar)
    editor.addEventListener("pointerdown", onEditorPointerDown, true)
    document.addEventListener("pointerdown", onDocumentPointerDown)

    return () => {
      // Abort any in-progress drag so we don't leave document-level
      // listeners or a stuck `grabbing` cursor behind on unmount.
      if (activeGestureCleanup) activeGestureCleanup()
      document.removeEventListener("dragstart", onDragStart, true)
      editor.removeEventListener("trix-attachment-before-toolbar", onBeforeToolbar)
      editor.removeEventListener("pointerdown", onEditorPointerDown, true)
      document.removeEventListener("pointerdown", onDocumentPointerDown)
      dropIndicator.remove()
    }
  }, [])

  // Open links in a new tab when clicked inside the editor. Trix leaves clicks
  // to the browser's contenteditable, which only moves the caret. Readers
  // expect a click to navigate.
  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const handleClick = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null
      const link = target?.closest("a")
      if (!link || !editor.contains(link)) return
      // An image preview is something you edit, not something you follow. Trix
      // wraps an attachment that has an href in an <a> spanning the whole
      // figure, so navigating here would make the image and its caption field
      // unclickable. Attachments we create no longer set href, but content
      // saved before that -- or pasted as <a><img></a> -- still can.
      if (link.closest(".attachment--preview")) return
      const href = link.getAttribute("href")
      if (!href) return
      event.preventDefault()
      // Allowlist schemes before navigating. An author can type any href into
      // the link dialog, so reject javascript:/data:/vbscript: etc. to avoid
      // executing attacker-controlled script on click.
      let url: URL
      try {
        url = new URL(href, window.location.href)
      } catch {
        return
      }
      if (!["http:", "https:", "mailto:"].includes(url.protocol)) return
      window.open(url.toString(), "_blank", "noopener,noreferrer")
    }

    editor.addEventListener("click", handleClick)
    return () => { editor.removeEventListener("click", handleClick) }
  }, [])

  return (
    <div ref={wrapperRef} className="trix-editor-wrapper">
      <svg style={{ display: "none" }} aria-hidden="true">
        <symbol id={`${toolbarId}-undo`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M7 13L3 9M3 9L7 5M3 9H16C18.7614 9 21 11.2386 21 14C21 16.7614 18.7614 19 16 19H11" />
        </symbol>
        <symbol id={`${toolbarId}-redo`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M17 13L21 9M21 9L17 5M21 9H8C5.23858 9 3 11.2386 3 14C3 16.7614 5.23858 19 8 19H13" />
        </symbol>
        <symbol id={`${toolbarId}-bold`} viewBox="0 0 24 24">
          <path d="M8 12H12.5M8 12V5H12.5C14.433 5 16 6.567 16 8.5C16 10.433 14.433 12 12.5 12M8 12V19H13.5C15.433 19 17 17.433 17 15.5C17 13.567 15.433 12 13.5 12H12.5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </symbol>
        <symbol id={`${toolbarId}-italic`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="19" y1="4" x2="10" y2="4" /><line x1="14" y1="20" x2="5" y2="20" /><line x1="15" y1="4" x2="9" y2="20" />
        </symbol>
        <symbol id={`${toolbarId}-strike`} viewBox="0 0 24 24">
          <path d="M12.0005 12.0001C12.8959 12.0001 13.7749 12.1925 14.5457 12.5571C14.8939 12.7218 15.2146 12.9192 15.5009 13.1437C15.8484 13.4162 16.1457 13.729 16.3822 14.0732C16.8136 14.7009 17.0263 15.4096 16.9982 16.1256C16.97 16.8416 16.702 17.5385 16.2222 18.1433C15.7424 18.7481 15.0684 19.2386 14.2705 19.5638C13.4727 19.889 12.5802 20.0373 11.6865 19.9923C10.7928 19.9473 9.93104 19.7108 9.19043 19.3082C8.44982 18.9055 7.85782 18.3514 7.47656 17.7032M12.0005 12.0001H4M12.0005 12.0001H20M16.5243 6.29718C16.143 5.649 15.5512 5.09462 14.8105 4.69197C14.0699 4.28932 13.2076 4.05287 12.314 4.00789C11.4203 3.96291 10.5278 4.11091 9.72998 4.43613C8.93213 4.76135 8.25812 5.25205 7.77832 5.85689C7.29852 6.46173 7.03057 7.15885 7.00244 7.87485C6.9942 8.08463 7.00669 8.29345 7.03924 8.50014" stroke="currentColor" fill="none" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </symbol>
        <symbol id={`${toolbarId}-link`} viewBox="0 0 24 24">
          <path fillRule="evenodd" clipRule="evenodd" d="M10.975 14.51a1.05 1.05 0 0 0 0-1.485 2.95 2.95 0 0 1 0-4.172l3.536-3.535a2.95 2.95 0 1 1 4.172 4.172l-1.093 1.092a1.05 1.05 0 0 0 1.485 1.485l1.093-1.092a5.05 5.05 0 0 0-7.142-7.142L9.49 7.368a5.05 5.05 0 0 0 0 7.142c.41.41 1.075.41 1.485 0zm2.05-5.02a1.05 1.05 0 0 0 0 1.485 2.95 2.95 0 0 1 0 4.172l-3.5 3.5a2.95 2.95 0 1 1-4.171-4.172l1.025-1.025a1.05 1.05 0 0 0-1.485-1.485L3.87 12.99a5.05 5.05 0 0 0 7.142 7.142l3.5-3.5a5.05 5.05 0 0 0 0-7.142 1.05 1.05 0 0 0-1.485 0z" fill="currentColor" />
        </symbol>
        <symbol id={`${toolbarId}-heading`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M7 5V12M7 12V19M7 12H17M17 5V12M17 12V19" />
        </symbol>
        <symbol id={`${toolbarId}-bullet`} viewBox="0 0 24 24">
          <path fill="currentColor" d="M4 6c0-.55.45-1 1-1s1 .45 1 1-.45 1-1 1-1-.45-1-1zm5-1c-.55 0-1 .45-1 1s.45 1 1 1h10c.55 0 1-.45 1-1s-.45-1-1-1H9zm0 7c-.55 0-1 .45-1 1s.45 1 1 1h10c.55 0 1-.45 1-1s-.45-1-1-1H9zm0 7c-.55 0-1 .45-1 1s.45 1 1 1h10c.55 0 1-.45 1-1s-.45-1-1-1H9zm-5-7c0-.55.45-1 1-1s1 .45 1 1-.45 1-1 1-1-.45-1-1zm0 7c0-.55.45-1 1-1s1 .45 1 1-.45 1-1 1-1-.45-1-1z" />
        </symbol>
        <symbol id={`${toolbarId}-number`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10 17H20M4 15.6853V15.5C4 14.6716 4.67157 14 5.5 14H5.54054C6.34658 14 7.00021 14.6534 7.00021 15.4595C7.00021 15.8103 6.8862 16.1519 6.67568 16.4326L4 20.0002L7 20M10 12H20M10 7H20M4 5L6 4V10" />
        </symbol>
        <symbol id={`${toolbarId}-quote`} viewBox="0 0 24 24" fill="currentColor">
          <path d="M7.17 6C4.87 6 3 7.87 3 10.17v3.41C3 14.92 4.08 16 5.42 16h3.16c1.34 0 2.42-1.08 2.42-2.42v-3.16C11 8.87 10.13 8 9.07 8H8c.13-.97.93-1.72 1.92-1.72H10V6H7.17zm9 0C13.87 6 12 7.87 12 10.17v3.41c0 1.34 1.08 2.42 2.42 2.42h3.16c1.34 0 2.42-1.08 2.42-2.42v-3.16c0-1.55-.87-2.42-1.93-2.42H17c.13-.97.93-1.72 1.92-1.72H19V6h-2.83z" />
        </symbol>
        <symbol id={`${toolbarId}-code`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M16 18l6-6-6-6M8 6l-6 6 6 6" />
        </symbol>
        <symbol id={`${toolbarId}-decrease`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 6H11M21 12H13M21 18H11M7 8l-4 4 4 4" />
        </symbol>
        <symbol id={`${toolbarId}-increase`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 6H11M21 12H13M21 18H11M3 8l4 4-4 4" />
        </symbol>
        <symbol id={`${toolbarId}-attach`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
        </symbol>
        <symbol id={`${toolbarId}-image`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2" /><circle cx="8.5" cy="8.5" r="1.5" /><path d="M21 15l-5-5L5 21" />
        </symbol>
      </svg>

      <trix-toolbar id={toolbarId}>
        <div className="trix-button-row">
          <span className="trix-button-group trix-button-group--text-tools">
            <button type="button" className="trix-button trix-button--icon-bold" data-trix-attribute="bold" data-trix-key="b" title="Bold" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-bold`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-italic" data-trix-attribute="italic" data-trix-key="i" title="Italic" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-italic`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-strike" data-trix-attribute="strike" title="Strikethrough" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-strike`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-link" data-trix-action="link" data-trix-key="k" title="Link" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-link`} /></svg>
            </button>
          </span>
          <span className="trix-button-group trix-button-group--block-tools">
            <button type="button" className="trix-button trix-button--icon-heading-1" data-trix-attribute="heading1" title="Heading" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-heading`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-quote" data-trix-attribute="quote" title="Quote" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-quote`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-code" data-trix-attribute="code" title="Code" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-code`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-bullet-list" data-trix-attribute="bullet" title="Bullets" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-bullet`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-number-list" data-trix-attribute="number" title="Numbers" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-number`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-decrease-nesting-level" data-trix-action="decreaseNestingLevel" title="Decrease indent" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-decrease`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-increase-nesting-level" data-trix-action="increaseNestingLevel" title="Increase indent" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-increase`} /></svg>
            </button>
          </span>
          <span className="trix-button-group trix-button-group--file-tools">
            {/* Not a data-trix-action button: this opens our own picker rather
                than invoking a built-in Trix action. */}
            <button type="button" className="trix-button trix-button--icon-image" onClick={openPicker} title="Insert image from library" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-image`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-attach" data-trix-action="attachFiles" title="Attach files" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-attach`} /></svg>
            </button>
          </span>
          <span className="trix-button-group trix-button-group--history-tools">
            <button type="button" className="trix-button trix-button--icon-undo" data-trix-action="undo" data-trix-key="z" title="Undo" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-undo`} /></svg>
            </button>
            <button type="button" className="trix-button trix-button--icon-redo" data-trix-action="redo" data-trix-key="shift+z" title="Redo" tabIndex={-1}>
              <svg className="trix-icon" aria-hidden="true"><use href={`#${toolbarId}-redo`} /></svg>
            </button>
          </span>
        </div>

        <div className="trix-dialogs" data-trix-dialogs>
          <div className="trix-dialog trix-dialog--link" data-trix-dialog="link" data-trix-dialog-attribute="href">
            <div className="trix-dialog__link-fields">
              <input type="url" name="href" className="trix-input trix-input--dialog" placeholder="Enter a URL…" aria-label="URL" required data-trix-input />
              <div className="trix-button-group">
                <input type="button" className="trix-button trix-button--dialog" value="Link" data-trix-method="setAttribute" />
                <input type="button" className="trix-button trix-button--dialog" value="Unlink" data-trix-method="removeAttribute" />
              </div>
            </div>
          </div>
        </div>
      </trix-toolbar>

      <trix-editor
        ref={editorRef as React.RefObject<TrixEditorElement>}
        toolbar={toolbarId}
        className="trix-content"
      />

      {notice && (
        <div className="trix-editor-notice" role="status">
          <span>{notice}</span>
          <button aria-label="Dismiss" onClick={() => setNotice(null)} type="button">×</button>
        </div>
      )}

      {pickerOpen && (
        <MediaPicker
          onClose={() => {
            setPickerOpen(false)
            savedRangeRef.current = null
          }}
          onSelect={insertFromLibrary}
        />
      )}
    </div>
  )
}

export default TrixEditor
