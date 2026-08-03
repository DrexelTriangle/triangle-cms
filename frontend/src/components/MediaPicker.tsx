import { useCallback, useEffect, useRef, useState } from "react"
import { ImageOff, Search, Upload, X } from "lucide-react"
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
  const [search, setSearch] = useState("")
  const [isLoading, setIsLoading] = useState(true)
  const [isUploading, setIsUploading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [urlInput, setUrlInput] = useState(initialUrl)

  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput.trim()), 300)
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

    const load = async () => {
      setIsLoading(true)
      setError(null)
      try {
        const params = new URLSearchParams({ limit: String(PAGE_SIZE) })
        if (search) params.set("search", search)
        const response = await apiFetch(`/v1/media/gallery?${params.toString()}`, {
          signal: controller.signal,
        })
        if (!response.ok) throw new Error(await errorMessage(response, `Request failed (${response.status})`))
        const payload = (await response.json()) as GalleryResponse
        if (controller.signal.aborted) return
        setItems(payload.media ?? [])
      } catch (err) {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : "Unable to load media.")
      } finally {
        if (!controller.signal.aborted) setIsLoading(false)
      }
    }

    void load()
    return () => controller.abort()
  }, [apiFetch, search])

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

        <div className="min-h-0 flex-1 overflow-y-auto">
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
                <button
                  className="group flex flex-col overflow-hidden rounded-lg border border-border bg-card text-left transition-colors hover:border-primary"
                  key={item.id}
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
                  {!item.alt_text && (
                    // Surfaced here because this is the moment the choice is
                    // made: an image with no alt text will publish without any.
                    <span className="px-2 pb-1.5 text-[11px] text-muted-foreground">No alt text</span>
                  )}
                </button>
              ))}
            </div>
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

export default MediaPicker
