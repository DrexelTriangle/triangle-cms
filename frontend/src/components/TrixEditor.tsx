import { useCallback, useEffect, useId, useRef, useState } from "react"
import "trix"
import "trix/dist/trix.css"
import "./TrixEditor.css"
import { apiBaseUrl } from "../auth/urls"
import MediaPicker, { type MediaPickerItem } from "./MediaPicker"
import {
  ALIGNMENTS,
  TRIX_ALIGN_CLASS,
  articleHtmlToTrix,
  contentTypeForUrl,
  trixHtmlToArticle,
} from "./trixImageHtml"

// Show the filename in the auto-generated caption under attachments, but hide
// the file size this matches the upstream Trix demo's defaults and gives users an
// editable caption field below each image.
if (typeof window !== "undefined" && window.Trix) {
  window.Trix.config.attachments.preview.caption.name = true
  window.Trix.config.attachments.preview.caption.size = false
}

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
  // Tracks the last HTML we emitted so we don't call loadHTML on our own onChange updates,
  // which would reset the cursor mid-edit.
  const lastEmittedRef = useRef<string>("")
  // Caret position captured before the picker steals focus, so the image lands
  // where the author was typing rather than at the top of the document.
  const savedRangeRef = useRef<[number, number] | null>(null)

  const [pickerOpen, setPickerOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    if (value !== lastEmittedRef.current) {
      editor.editor.loadHTML(articleHtmlToTrix(value))
      lastEmittedRef.current = value
    }
  }, [value])

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const handleChange = () => {
      // Emit the semantic markup we persist, not Trix's internal attachment
      // format. lastEmittedRef has to hold the *converted* HTML: it is compared
      // against the incoming value prop to decide whether to reload the editor,
      // and reloading on our own output would reset the caret on every
      // keystroke.
      const html = trixHtmlToArticle(editor.value)
      lastEmittedRef.current = html
      onChange(html)
    }

    editor.addEventListener("trix-change", handleChange)
    return () => { editor.removeEventListener("trix-change", handleChange) }
  }, [onChange])

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
          attachment.setAttributes({ url, href: url })
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
          attachment.setAttributes({ url, href: url })
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

    const attachment = new Trix.Attachment({
      url: item.url,
      href: item.url,
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
    type DropTarget = { block: HTMLElement; insertBefore: boolean }

    const DRAG_THRESHOLD_PX = 5

    // Find which top-level block the cursor is over (or nearest by Y). We
    // intentionally include the dragged figure's own block as a candidate so
    // the drop indicator still appears when an editor contains only one
    // block. The actual no-op check (drop ≈ no move) happens at commit time.
    const findBlockAt = (clientY: number): HTMLElement | null => {
      const blocks = Array.from(editor.children) as HTMLElement[]
      let nearest: HTMLElement | null = null
      let nearestDist = Infinity
      for (const block of blocks) {
        const rect = block.getBoundingClientRect()
        if (clientY >= rect.top && clientY <= rect.bottom) return block
        const dist = clientY < rect.top ? rect.top - clientY : clientY - rect.bottom
        if (dist < nearestDist) {
          nearestDist = dist
          nearest = block
        }
      }
      return nearest
    }

    const positionDropIndicator = (target: DropTarget) => {
      const wrapperRect = wrapper.getBoundingClientRect()
      const editorRect = editor.getBoundingClientRect()
      const blockRect = target.block.getBoundingClientRect()
      const y = target.insertBefore ? blockRect.top : blockRect.bottom

      const editorLeft = editorRect.left - wrapperRect.left
      const editorWidth = editorRect.width
      const indicatorWidth = Math.min(editorWidth * 0.4, 240)

      dropIndicator.style.display = "block"
      dropIndicator.style.top = `${(y - wrapperRect.top).toString()}px`
      dropIndicator.style.left = `${(editorLeft + (editorWidth - indicatorWidth) / 2).toString()}px`
      dropIndicator.style.width = `${indicatorWidth.toString()}px`
    }

    // Move the attachment via Trix's editor API. Strategy:
    //   1. Clone the figure, stripping the stale data-trix-id so Trix mints a
    //      fresh attachment on insert rather than colliding with the live
    //      attachment we're about to remove. data-trix-attributes is left
    //      untouched, which is what carries any pre-existing alignment through
    //      the move intact.
    //   2. Capture a DOM Range anchored on the target block BEFORE removing
    //      the source. Trix's remove() can collapse or detach adjacent empty
    //      blocks, which would invalidate setStartBefore/After afterwards.
    //   3. Focus the editor BEFORE applying the selection. Focusing a
    //      contenteditable that isn't currently focused resets the caret to
    //      its last internal position, which would blow away our range.
    //   4. Then remove the attachment, apply the captured range, and insert.
    const commitMove = (figure: HTMLElement, target: DropTarget) => {
      const trixId = figure.getAttribute("data-trix-id")
      if (!trixId) return
      const attachment = editor.editor.getDocument().getAttachments().find((a) => String(a.id) === trixId)
      if (!attachment) return
      if (!target.block.isConnected) return
      // Dropping onto the figure's own block moves nothing. With alignment gone
      // there is no longer any secondary effect to apply, so this is a no-op --
      // and skipping it avoids a needless remove/reinsert of the attachment.
      if (target.block.contains(figure)) return

      const clone = figure.cloneNode(true) as HTMLElement
      clone.removeAttribute("data-trix-id")
      const html = clone.outerHTML

      const range = document.createRange()
      if (target.insertBefore) range.setStartBefore(target.block)
      else range.setStartAfter(target.block)
      range.collapse(true)

      editor.focus()
      attachment.remove()

      if (target.block.isConnected) {
        const selection = window.getSelection()
        selection?.removeAllRanges()
        selection?.addRange(range)
      }

      editor.editor.insertHTML(html)
      editor.dispatchEvent(new Event("input", { bubbles: true }))
    }

    const beginDrag = (figure: HTMLElement, downEvent: MouseEvent) => {
      downEvent.preventDefault()
      const startX = downEvent.clientX
      const startY = downEvent.clientY
      let dragging = false
      let dropTarget: DropTarget | null = null

      const enterDragMode = () => {
        dragging = true
        figure.classList.add("attachment--dragging")
        document.body.style.cursor = "grabbing"
      }

      const onMove = (moveEvent: MouseEvent) => {
        if (!dragging) {
          const dx = Math.abs(moveEvent.clientX - startX)
          const dy = Math.abs(moveEvent.clientY - startY)
          if (dx < DRAG_THRESHOLD_PX && dy < DRAG_THRESHOLD_PX) return
          enterDragMode()
        }

        const block = findBlockAt(moveEvent.clientY)
        if (!block) {
          dropIndicator.style.display = "none"
          dropTarget = null
          return
        }
        const blockRect = block.getBoundingClientRect()
        const insertBefore = moveEvent.clientY < blockRect.top + blockRect.height / 2
        dropTarget = { block, insertBefore }
        positionDropIndicator(dropTarget)
      }

      // Idempotent state-restorer, invoked from onUp on normal release AND
      // from the outer effect's cleanup if the component unmounts mid-drag.
      const cleanup = () => {
        document.removeEventListener("mousemove", onMove)
        document.removeEventListener("mouseup", onUp)
        document.body.style.cursor = ""
        if (figure.isConnected) figure.classList.remove("attachment--dragging")
        dropIndicator.style.display = "none"
      }

      const onUp = () => {
        activeGestureCleanup = null
        cleanup()
        if (dragging && dropTarget) {
          commitMove(figure, dropTarget)
        } else if (!dragging) {
          // Treat as a plain click: select the figure and restore focus so
          // subsequent keyboard input still lands in the editor (our
          // preventDefault on mousedown blocked the native focus path).
          selectFigure(figure)
          editor.focus()
        }
      }

      document.addEventListener("mousemove", onMove)
      document.addEventListener("mouseup", onUp)
      activeGestureCleanup = cleanup
    }

    // ── Selection / drag entry point (capture phase so Trix can't preempt) ──
    const onEditorMouseDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement
      const figure = target.closest(".attachment--preview") as HTMLElement | null
      if (!figure) return
      beginDrag(figure, event)
    }
    const onDocumentMouseDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement
      // Don't deselect when the click lands on another figure; that has its own
      // selection semantics.
      if (target.closest(".attachment--preview")) return
      selectFigure(null)
    }

    editor.addEventListener("mousedown", onEditorMouseDown, true)
    document.addEventListener("mousedown", onDocumentMouseDown)

    return () => {
      // Abort any in-progress drag so we don't leave document-level
      // listeners or a stuck `grabbing` cursor behind on unmount.
      if (activeGestureCleanup) activeGestureCleanup()
      editor.removeEventListener("mousedown", onEditorMouseDown, true)
      document.removeEventListener("mousedown", onDocumentMouseDown)
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
