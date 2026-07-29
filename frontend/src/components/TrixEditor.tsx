import { useEffect, useId, useRef } from "react"
import "trix"
import "trix/dist/trix.css"
import "./TrixEditor.css"
import { apiBaseUrl } from "../auth/urls"

// Show the filename in the auto-generated caption under attachments, but hide
// the file size this matches the upstream Trix demo's defaults and gives users an
// editable caption field below each image.
if (typeof window !== "undefined" && window.Trix) {
  window.Trix.config.attachments.preview.caption.name = true
  window.Trix.config.attachments.preview.caption.size = false
}

const IMAGE_CONTENT_TYPES: Record<string, string> = {
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  png: "image/png",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  svg: "image/svg+xml",
}

const contentTypeForUrl = (url: string): string => {
  const ext = url.split(/[?#]/)[0].split(".").pop()?.toLowerCase() ?? ""
  return IMAGE_CONTENT_TYPES[ext] ?? "image/jpeg"
}

// Trix only restores an image's caption when it's carried in the attachment's
// data attributes. Article HTML imported from WordPress instead puts the
// caption in a plain caption element (block editor: <figure><figcaption>;
// classic editor: <div class="wp-caption">…<* class="wp-caption-text">), which
// Trix's parser drops onto the next line as body text — the caption "isn't
// picked up by the editor". Rewrite those into Trix's native attachment format
// so loadHTML keeps the caption bound to the image. Containers already
// round-tripped through Trix (they carry data-trix-attachment) are skipped, so
// this is idempotent.
const restoreFigureCaptions = (html: string): string => {
  if (typeof window === "undefined" || !window.DOMParser) return html
  if (!html.includes("<figure") && !html.includes("wp-caption")) return html

  const doc = new DOMParser().parseFromString(html, "text/html")
  let changed = false

  for (const container of Array.from(doc.querySelectorAll("figure, .wp-caption"))) {
    if (container.hasAttribute("data-trix-attachment")) continue
    // Only the simple single-image case is safe to rebuild. Galleries and
    // nested captioned figures hold more than one image (or another caption
    // container); reconstructing them from a single <img> would silently drop
    // the rest, so leave those untouched.
    if (container.querySelectorAll("img").length !== 1) continue
    if (container.querySelector("figure, .wp-caption")) continue

    const img = container.querySelector("img")
    const src = img?.getAttribute("src")?.trim()
    const caption = container.querySelector("figcaption, .wp-caption-text")?.textContent?.trim()
    if (!img || !src || !caption) continue

    const basename = src.split("/").pop()?.split(/[?#]/)[0]
    const replacement = doc.createElement("figure")
    replacement.setAttribute("data-trix-attachment", JSON.stringify({
      contentType: contentTypeForUrl(src),
      url: src,
      filename: img.getAttribute("alt")?.trim() || basename || "image",
      // Force inline preview: Trix's previewablePattern excludes svg/avif and
      // any extension-less CDN URL, which would otherwise render as a file stub.
      previewable: true,
    }))
    replacement.setAttribute("data-trix-attributes", JSON.stringify({ presentation: "gallery", caption }))
    const newImg = doc.createElement("img")
    newImg.setAttribute("src", src)
    replacement.appendChild(newImg)
    container.replaceWith(replacement)
    changed = true
  }

  return changed ? doc.body.innerHTML : html
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

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    if (value !== lastEmittedRef.current) {
      editor.editor.loadHTML(restoreFigureCaptions(value))
      lastEmittedRef.current = value
    }
  }, [value])

  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const handleChange = () => {
      const html = editor.value
      lastEmittedRef.current = html
      onChange(html)
    }

    editor.addEventListener("trix-change", handleChange)
    return () => { editor.removeEventListener("trix-change", handleChange) }
  }, [onChange])

  // Uses XHR instead of fetch since fetch doesn't expose upload progress events,
  // which Trix needs to render its built-in progress bar.
  useEffect(() => {
    const editor = editorRef.current
    if (!editor) return

    const handleAttachmentAdd = (event: Event) => {
      const { attachment } = event as TrixAttachmentAddEvent
      // trix-attachment-add also fires for programmatic URL embeds (no .file)
      if (!attachment.file) return

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

      // On failure the attachment stays in the editor on its local blob-URL
      // preview, so the image can still be moved, captioned, and rearranged.
      // That preview does not survive a reload — the saved HTML needs the real
      // server URL — so a failed upload has to be retried.
      xhr.onload = () => {
        if (xhr.status === 201) {
          try {
            const { url } = JSON.parse(xhr.responseText) as { url: string }
            attachment.setAttributes({ url, href: url })
          } catch {
            // 201 with an unexpected body: nothing to attach, keep the preview.
          }
        }
        attachment.setUploadProgress(100)
      }

      xhr.onerror = () => {
        attachment.setUploadProgress(100)
      }

      const formData = new FormData()
      formData.append("file", attachment.file)
      xhr.send(formData)
    }

    editor.addEventListener("trix-attachment-add", handleAttachmentAdd)
    return () => { editor.removeEventListener("trix-attachment-add", handleAttachmentAdd) }
  }, [])

  // Image manipulation overlay: selection, four-corner resize, and
   // drag-to-rearrange (vertical drop + left/center/right alignment snap).
   // All overlay DOM lives in the wrapper *outside* Trix's contenteditable so
   // Trix doesn't overwrite our handles via its MutationObserver.
  useEffect(() => {
    const editor = editorRef.current
    const wrapper = wrapperRef.current
    if (!editor || !wrapper) return

    const overlay = document.createElement("div")
    overlay.className = "trix-resize-overlay"
    overlay.style.display = "none"

    const corners = ["nw", "ne", "sw", "se"] as const
    type Corner = (typeof corners)[number]
    const handles: Record<Corner, HTMLDivElement> = {} as Record<Corner, HTMLDivElement>
    for (const corner of corners) {
      const handle = document.createElement("div")
      handle.className = `trix-resize-handle trix-resize-handle--${corner}`
      handle.dataset.corner = corner
      overlay.appendChild(handle)
      handles[corner] = handle
    }
    wrapper.appendChild(overlay)

    // Drop indicator (a thin horizontal bar shown only during an active drag).
    const dropIndicator = document.createElement("div")
    dropIndicator.className = "trix-drop-indicator"
    dropIndicator.style.display = "none"
    wrapper.appendChild(dropIndicator)

    let activeFigure: HTMLElement | null = null
    // Per-gesture cleanup set when a drag or resize is in progress so the
    // outer effect's teardown can abort it on unmount (prevents leaked
    // document listeners and a stuck `grabbing` cursor).
    let activeGestureCleanup: (() => void) | null = null

    const positionOverlay = () => {
      if (!activeFigure) {
        overlay.style.display = "none"
        return
      }
      const img = activeFigure.querySelector("img")
      const target = img ?? activeFigure
      const wrapperRect = wrapper.getBoundingClientRect()
      const rect = target.getBoundingClientRect()
      overlay.style.display = "block"
      overlay.style.top = `${(rect.top - wrapperRect.top).toString()}px`
      overlay.style.left = `${(rect.left - wrapperRect.left).toString()}px`
      overlay.style.width = `${rect.width.toString()}px`
      overlay.style.height = `${rect.height.toString()}px`
    }

    const selectFigure = (figure: HTMLElement | null) => {
      if (activeFigure && activeFigure !== figure) {
        activeFigure.classList.remove("attachment--selected")
      }
      activeFigure = figure
      if (figure) figure.classList.add("attachment--selected")
      positionOverlay()
    }

    // ── Resize (4 corners, aspect-ratio locked) ────────────────────────────
    const beginResize = (corner: Corner) => (downEvent: MouseEvent) => {
      if (!activeFigure) return
      const img = activeFigure.querySelector("img")
      if (!img) return
      downEvent.preventDefault()
      downEvent.stopPropagation()

      const startRect = img.getBoundingClientRect()
      const startWidth = startRect.width
      const startHeight = startRect.height
      const aspect = startHeight / startWidth
      const startX = downEvent.clientX
      const xDirection = corner === "ne" || corner === "se" ? 1 : -1

      const onMove = (moveEvent: MouseEvent) => {
        const dx = (moveEvent.clientX - startX) * xDirection
        const newWidth = Math.max(80, Math.round(startWidth + dx))
        const newHeight = Math.max(40, Math.round(newWidth * aspect))
        img.setAttribute("width", String(newWidth))
        img.setAttribute("height", String(newHeight))
        img.style.width = `${newWidth.toString()}px`
        img.style.height = `${newHeight.toString()}px`
        positionOverlay()
      }
      const cleanup = () => {
        document.removeEventListener("mousemove", onMove)
        document.removeEventListener("mouseup", onUp)
      }
      const onUp = () => {
        activeGestureCleanup = null
        cleanup()
        editor.dispatchEvent(new Event("input", { bubbles: true }))
      }
      document.addEventListener("mousemove", onMove)
      document.addEventListener("mouseup", onUp)
      activeGestureCleanup = cleanup
    }

    for (const corner of corners) {
      handles[corner].addEventListener("mousedown", beginResize(corner))
    }

    // ── Drag-to-rearrange ──────────────────────────────────────────────────
    type Alignment = "left" | "center" | "right"
    type DropTarget = { block: HTMLElement; insertBefore: boolean; alignment: Alignment }

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

    const alignmentFromX = (clientX: number): Alignment => {
      const rect = editor.getBoundingClientRect()
      const ratio = (clientX - rect.left) / rect.width
      if (ratio < 0.3) return "left"
      if (ratio > 0.7) return "right"
      return "center"
    }

    const positionDropIndicator = (target: DropTarget) => {
      const wrapperRect = wrapper.getBoundingClientRect()
      const editorRect = editor.getBoundingClientRect()
      const blockRect = target.block.getBoundingClientRect()
      const y = target.insertBefore ? blockRect.top : blockRect.bottom

      const editorLeft = editorRect.left - wrapperRect.left
      const editorWidth = editorRect.width
      const indicatorWidth = Math.min(editorWidth * 0.4, 240)
      let left = editorLeft + (editorWidth - indicatorWidth) / 2
      if (target.alignment === "left") left = editorLeft + 24
      if (target.alignment === "right") left = editorLeft + editorWidth - indicatorWidth - 24

      dropIndicator.style.display = "block"
      dropIndicator.style.top = `${(y - wrapperRect.top).toString()}px`
      dropIndicator.style.left = `${left.toString()}px`
      dropIndicator.style.width = `${indicatorWidth.toString()}px`
      dropIndicator.dataset.alignment = target.alignment
    }

    // Move the attachment via Trix's editor API. Strategy:
    //   1. Build the clone HTML with alignment baked in. Strip the stale
    //      data-trix-id (and matching content-type's sgid hint) so Trix mints
    //      a fresh attachment on insert rather than colliding with the live
    //      attachment we're about to remove.
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
      // No-op if the user dropped right back onto the source's own block.
      // findBlockAt no longer skips it so the drop indicator works for
      // single-block editors; this is where we treat the same-block drop as
      // "nothing to do" and still apply only the alignment change.
      const droppedOnSelf = target.block.contains(figure)
      if (droppedOnSelf) {
        figure.classList.remove("attachment--align-left", "attachment--align-center", "attachment--align-right")
        figure.classList.add(`attachment--align-${target.alignment}`)
        figure.style.textAlign = target.alignment
        editor.dispatchEvent(new Event("input", { bubbles: true }))
        return
      }

      const clone = figure.cloneNode(true) as HTMLElement
      clone.classList.remove("attachment--align-left", "attachment--align-center", "attachment--align-right")
      clone.classList.add(`attachment--align-${target.alignment}`)
      clone.style.textAlign = target.alignment
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
        overlay.style.display = "none"
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
        dropTarget = {
          block,
          insertBefore,
          alignment: alignmentFromX(moveEvent.clientX),
        }
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
      if (target.closest(".trix-resize-handle")) return
      const figure = target.closest(".attachment--preview") as HTMLElement | null
      if (!figure) return
      beginDrag(figure, event)
    }
    const onDocumentMouseDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement
      // Don't deselect when the user is clicking a resize handle or another
      // figure; those have their own selection semantics.
      if (target.closest(".trix-resize-handle")) return
      if (target.closest(".attachment--preview")) return
      selectFigure(null)
    }

    const onReflow = () => positionOverlay()
    editor.addEventListener("mousedown", onEditorMouseDown, true)
    document.addEventListener("mousedown", onDocumentMouseDown)
    editor.addEventListener("trix-change", onReflow)
    window.addEventListener("resize", onReflow)
    window.addEventListener("scroll", onReflow, true)

    return () => {
      // Abort any in-progress drag/resize so we don't leave document-level
      // listeners or a stuck `grabbing` cursor behind on unmount.
      if (activeGestureCleanup) activeGestureCleanup()
      editor.removeEventListener("mousedown", onEditorMouseDown, true)
      document.removeEventListener("mousedown", onDocumentMouseDown)
      editor.removeEventListener("trix-change", onReflow)
      window.removeEventListener("resize", onReflow)
      window.removeEventListener("scroll", onReflow, true)
      overlay.remove()
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
    </div>
  )
}

export default TrixEditor
