import { useCallback, useEffect, useRef, useState } from "react"
import { ImageOff, Pencil, Search, Upload, X } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

export type MediaPickerItem = {
  id: number
  url: string
  file_name: string
  mime_type?: string
  width?: number
  height?: number
  alt_text?: string
}

type GalleryResponse = {
  media?: MediaPickerItem[]
}

type MediaPickerProps = {
  onSelect: (item: MediaPickerItem) => void
  onClose: () => void
  title?: string
  // When set, the picker also accepts a bare image URL. Only the featured-image
  // field wants this: an article's photo_url is served verbatim, so it can point
  // at an image that was never in our library. Body attachments have no such
  // escape hatch by design -- they get sideloaded so articles never hotlink.
  onUseUrl?: (url: string) => void
  initialUrl?: string
}

const PAGE_SIZE = 60

// How far ahead of the sentinel to start fetching, so the next page is usually
// already in place by the time the author scrolls to the bottom of the grid.
const LOAD_MORE_MARGIN = "400px"

async function errorMessage(response: Response, fallback: string) {
  try {
    const body = (await response.json()) as { error?: string }
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

/**
 * Modal for choosing an image from the media library, plus an upload shortcut
 * for the common case where the image is not in the library yet.
 *
 * Backed by /v1/media/gallery, which returns the trimmed picker shape --
 * notably including alt_text, so a chosen image arrives with its description
 * already written rather than needing it retyped per article.
 */
function MediaPicker({ onSelect, onClose, title = "Insert image", onUseUrl, initialUrl = "" }: MediaPickerProps) {
  const apiFetch = useApiFetch()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [items, setItems] = useState<MediaPickerItem[]>([])
  const [searchInput, setSearchInput] = useState("")
  // The search and the offset travel together so a new search always starts
  // from the top: changing them separately would briefly request page 2 of the
  // old results under the new query.
  const [request, setRequest] = useState({ search: "", offset: 0 })
  const [hasMore, setHasMore] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [isUploading, setIsUploading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [urlInput, setUrlInput] = useState(initialUrl)
  const scrollRef = useRef<HTMLDivElement>(null)
  // A state-held node rather than a ref: the sentinel mounts and unmounts with
  // the grid, and the observer effect has to re-run when it does.
  const [sentinel, setSentinel] = useState<HTMLDivElement | null>(null)

  const search = request.search

  useEffect(() => {
    const timer = setTimeout(() => {
      const next = searchInput.trim()
      setRequest((prev) => (prev.search === next ? prev : { search: next, offset: 0 }))
    }, 300)
    return () => clearTimeout(timer)
  }, [searchInput])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [onClose])

  useEffect(() => {
    const controller = new AbortController()
    const isFirstPage = request.offset === 0

    const load = async () => {
      if (isFirstPage) setIsLoading(true)
      else setIsLoadingMore(true)
      setError(null)
      try {
        const params = new URLSearchParams({
          limit: String(PAGE_SIZE),
          offset: String(request.offset),
        })
        if (request.search) params.set("search", request.search)
        const response = await apiFetch(`/v1/media/gallery?${params.toString()}`, {
          signal: controller.signal,
        })
        if (!response.ok) throw new Error(await errorMessage(response, `Request failed (${response.status})`))
        const payload = (await response.json()) as GalleryResponse
        if (controller.signal.aborted) return
        const page = payload.media ?? []
        setItems((prev) => (isFirstPage ? page : [...prev, ...page]))
        // A short page means the library is exhausted; a full one only means
        // there might be more, which the next scroll will find out.
        setHasMore(page.length === PAGE_SIZE)
      } catch (err) {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : "Unable to load media.")
        // Otherwise the sentinel is still on screen and would immediately retry
        // the failing request in a loop.
        setHasMore(false)
      } finally {
        if (!controller.signal.aborted) {
          setIsLoading(false)
          setIsLoadingMore(false)
        }
      }
    }

    void load()
    return () => controller.abort()
  }, [apiFetch, request])

  useEffect(() => {
    if (!sentinel || !hasMore || isLoading || isLoadingMore) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        setRequest((prev) => (prev.offset === items.length ? prev : { ...prev, offset: items.length }))
      },
      { root: scrollRef.current, rootMargin: LOAD_MORE_MARGIN },
    )
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [sentinel, hasMore, isLoading, isLoadingMore, items.length])

  // Alt text saved from a tile belongs to the library record, so the grid has to
  // show the new value straight away -- otherwise the "No alt text" warning
  // stays up on an image that now has some.
  const updateItem = useCallback((id: number, altText: string) => {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, alt_text: altText } : item)))
  }, [])

  const handleUpload = useCallback(
    async (files: FileList | null) => {
      const file = files?.[0]
      if (!file) return
      setError(null)
      setIsUploading(true)
      try {
        const body = new FormData()
        body.append("file", file)
        const response = await apiFetch("/v1/media", { method: "POST", body })
        if (!response.ok) {
          setError(await errorMessage(response, `Upload failed (${response.status})`))
          return
        }
        const created = (await response.json()) as {
          id: number
          path: string
          url: string
          content_type?: string
          width?: number
          height?: number
        }
        // Straight into the document: the author picked a file in order to use
        // it, so making them find it in the grid afterwards is a wasted step.
        onSelect({
          id: created.id,
          url: created.url,
          file_name: created.path.split("/").pop() ?? created.path,
          mime_type: created.content_type,
          width: created.width,
          height: created.height,
        })
      } catch (err) {
        setError(err instanceof Error ? err.message : "Upload failed.")
      } finally {
        setIsUploading(false)
        if (fileInputRef.current) fileInputRef.current.value = ""
      }
    },
    [apiFetch, onSelect],
  )

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        aria-label={title}
        aria-modal="true"
        className="flex max-h-[85vh] w-full max-w-4xl flex-col gap-4 rounded-xl border border-border bg-background p-6"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
          <button
            aria-label="Close"
            className="p-1 text-muted-foreground hover:text-foreground"
            onClick={onClose}
            type="button"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-[240px] flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <input
              aria-label="Search media"
              autoFocus
              className="w-full rounded-lg border border-border bg-background py-2 pl-9 pr-4 text-sm text-foreground placeholder:text-muted-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder="Search by file name, alt text, or caption..."
              type="search"
              value={searchInput}
            />
          </div>
          <input
            accept="image/jpeg,image/png,image/gif,image/webp"
            className="hidden"
            onChange={(e) => void handleUpload(e.target.files)}
            ref={fileInputRef}
            type="file"
          />
          <button
            className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
            disabled={isUploading}
            onClick={() => fileInputRef.current?.click()}
            type="button"
          >
            <Upload className="h-4 w-4" aria-hidden="true" />
            {isUploading ? "Uploading..." : "Upload"}
          </button>
        </div>

        {error && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2 text-sm text-destructive">
            {error}
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto" ref={scrollRef}>
          {isLoading ? (
            <p className="py-12 text-center text-sm text-muted-foreground">Loading media...</p>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-12 text-muted-foreground">
              <ImageOff className="h-10 w-10" />
              <p className="text-sm">{search ? `No results for "${search}"` : "No media items yet."}</p>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
              {items.map((item) => (
                <div
                  className="group flex flex-col overflow-hidden rounded-lg border border-border bg-card transition-colors focus-within:border-primary hover:border-primary"
                  key={item.id}
                >
                  <button
                    className="flex flex-col text-left"
                    onClick={() => onSelect(item)}
                    title={item.file_name}
                    type="button"
                  >
                    <span className="relative block aspect-square overflow-hidden bg-muted">
                      <img
                        alt={item.alt_text || item.file_name}
                        className="h-full w-full object-cover transition-transform duration-200 group-hover:scale-105"
                        loading="lazy"
                        referrerPolicy="no-referrer"
                        src={item.url}
                      />
                    </span>
                    <span className="truncate px-2 py-1.5 text-xs text-foreground">{item.file_name}</span>
                  </button>
                  {/* Alt text is edited here, next to the image, because this is
                      the moment the choice is made: an image with no alt text
                      publishes without any, and sending the author to the Media
                      library to fix that loses the article they were writing. */}
                  <AltTextField item={item} onSaved={updateItem} />
                </div>
              ))}
            </div>
          )}
          {!isLoading && items.length > 0 && (
            <>
              {hasMore && <div aria-hidden="true" ref={setSentinel} className="h-px" />}
              {(isLoadingMore || (!hasMore && !error)) && (
                <p aria-live="polite" className="py-4 text-center text-sm text-muted-foreground">
                  {isLoadingMore ? "Loading more..." : "End of media library"}
                </p>
              )}
            </>
          )}
        </div>

        {onUseUrl && (
          <div className="flex gap-2 border-t border-border pt-4">
            <input
              aria-label="Image URL"
              className="flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
              onChange={(e) => setUrlInput(e.target.value)}
              placeholder="Or paste image URL"
              type="url"
              value={urlInput}
            />
            <button
              className="whitespace-nowrap rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
              disabled={!urlInput.trim()}
              onClick={() => onUseUrl(urlInput.trim())}
              type="button"
            >
              Use URL
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

type AltTextFieldProps = {
  item: MediaPickerItem
  onSaved: (id: number, altText: string) => void
}

/**
 * Inline alt-text editor for one tile.
 *
 * It writes to the library record (PATCH /v1/media/{id}), not to this article,
 * so an image described once is described everywhere it is used -- the same
 * contract the picker already relies on when it hands alt_text to the editor.
 *
 * Its own error state rather than the picker's banner: a failed save belongs
 * next to the field that failed, and it must not clear the text the author just
 * typed.
 */
function AltTextField({ item, onSaved }: AltTextFieldProps) {
  const apiFetch = useApiFetch()
  const [isEditing, setIsEditing] = useState(false)
  const [draft, setDraft] = useState(item.alt_text ?? "")
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const altText = item.alt_text ?? ""

  const save = async () => {
    const next = draft.trim()
    if (next === altText) {
      setIsEditing(false)
      return
    }
    setIsSaving(true)
    setError(null)
    try {
      const response = await apiFetch(`/v1/media/${String(item.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ alt_text: next }),
      })
      if (!response.ok) {
        setError(await errorMessage(response, `Save failed (${response.status})`))
        return
      }
      onSaved(item.id, next)
      setIsEditing(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to save alt text.")
    } finally {
      setIsSaving(false)
    }
  }

  if (!isEditing) {
    return (
      <button
        className={`flex items-center gap-1 px-2 pb-1.5 text-left text-[11px] hover:text-foreground ${
          altText ? "text-muted-foreground" : "text-destructive"
        }`}
        onClick={() => {
          setDraft(altText)
          setError(null)
          setIsEditing(true)
        }}
        title={altText || "Add alt text"}
        type="button"
      >
        <Pencil className="h-3 w-3 shrink-0" aria-hidden="true" />
        <span className="truncate">{altText || "No alt text"}</span>
      </button>
    )
  }

  return (
    <div className="flex flex-col gap-1 px-2 pb-2">
      <input
        aria-label={`Alt text for ${item.file_name}`}
        autoFocus
        className="w-full rounded border border-border bg-background px-1.5 py-1 text-[11px] text-foreground focus:border-primary focus:outline-none"
        disabled={isSaving}
        onChange={(e) => setDraft(e.target.value)}
        // Enter saves and Escape abandons, so the whole edit is one gesture
        // without reaching for the buttons.
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            void save()
          }
          // Stopped from bubbling: the picker closes the whole modal on Escape,
          // which would throw away the article's insertion point too.
          if (e.key === "Escape") {
            e.preventDefault()
            e.stopPropagation()
            setIsEditing(false)
          }
        }}
        placeholder="Describe the image"
        value={draft}
      />
      {error && <p className="text-[11px] text-destructive">{error}</p>}
      <div className="flex gap-1">
        <button
          className="rounded bg-primary px-2 py-0.5 text-[11px] font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={isSaving}
          onClick={() => void save()}
          type="button"
        >
          {isSaving ? "Saving..." : "Save"}
        </button>
        <button
          className="rounded px-2 py-0.5 text-[11px] text-muted-foreground hover:text-foreground"
          disabled={isSaving}
          onClick={() => setIsEditing(false)}
          type="button"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

export default MediaPicker
