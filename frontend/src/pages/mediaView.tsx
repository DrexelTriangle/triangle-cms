import { useCallback, useEffect, useRef, useState } from "react"
import { Copy, Check, ImageOff, Images, Search, Trash2, Upload, X } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"
import { useCurrentUserRole } from "../hooks/useCurrentUserRole"

type MediaItem = {
  id: number
  path: string
  url: string
  file_name: string
  mime_type?: string
  size_bytes?: number
  width?: number
  height?: number
  alt_text?: string
  caption?: string
  in_gallery?: boolean
  created_at?: string
}

type MediaResponse = {
  media?: MediaItem[]
  pagination?: {
    offset?: number
    has_more?: boolean
    total_count?: number
  }
}

const PAGE_SIZE = 60

function formatBytes(bytes?: number) {
  if (!bytes || bytes <= 0) return ""
  const units = ["B", "KB", "MB", "GB"]
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

async function errorMessage(response: Response, fallback: string) {
  try {
    const body = (await response.json()) as { error?: string }
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

function MediaView() {
  const apiFetch = useApiFetch()
  const { isAdmin } = useCurrentUserRole()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [mediaItems, setMediaItems] = useState<MediaItem[]>([])
  const [totalCount, setTotalCount] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const [searchInput, setSearchInput] = useState("")
  const [search, setSearch] = useState("")
  const [galleryOnly, setGalleryOnly] = useState(false)
  const [selected, setSelected] = useState<MediaItem | null>(null)
  const [uploadStatus, setUploadStatus] = useState<string | null>(null)

  // Debounce so typing doesn't fire a request per keystroke; the filter itself
  // is applied server-side because the library is far too large to filter here.
  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput.trim()), 300)
    return () => clearTimeout(timer)
  }, [searchInput])

  const fetchPage = useCallback(
    async (offset: number, signal?: AbortSignal) => {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String(offset),
      })
      if (search) params.set("search", search)
      if (galleryOnly) params.set("in_gallery", "true")

      const response = await apiFetch(`/v1/media?${params.toString()}`, { signal })
      if (!response.ok) throw new Error(await errorMessage(response, `Request failed (${response.status})`))
      return (await response.json()) as MediaResponse
    },
    [apiFetch, search, galleryOnly],
  )

  useEffect(() => {
    const controller = new AbortController()

    const load = async () => {
      setIsLoading(true)
      setError(null)
      try {
        const payload = await fetchPage(0, controller.signal)
        setMediaItems(payload.media ?? [])
        setTotalCount(payload.pagination?.total_count ?? 0)
        setHasMore(Boolean(payload.pagination?.has_more))
      } catch (err) {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : "Unable to load media.")
      } finally {
        if (!controller.signal.aborted) setIsLoading(false)
      }
    }

    void load()
    return () => controller.abort()
  }, [fetchPage])

  const loadMore = async () => {
    setIsLoadingMore(true)
    try {
      const payload = await fetchPage(mediaItems.length)
      setMediaItems((current) => [...current, ...(payload.media ?? [])])
      setTotalCount(payload.pagination?.total_count ?? 0)
      setHasMore(Boolean(payload.pagination?.has_more))
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load more media.")
    } finally {
      setIsLoadingMore(false)
    }
  }

  const handleUpload = async (files: FileList | null) => {
    if (!files || files.length === 0) return
    setError(null)
    setNotice(null)

    const uploaded: MediaItem[] = []
    const failures: string[] = []

    for (const [index, file] of Array.from(files).entries()) {
      setUploadStatus(`Uploading ${String(index + 1)} of ${String(files.length)}...`)
      const body = new FormData()
      body.append("file", file)
      try {
        const response = await apiFetch("/v1/media", { method: "POST", body })
        if (!response.ok) {
          failures.push(`${file.name}: ${await errorMessage(response, `failed (${response.status})`)}`)
          continue
        }
        const created = (await response.json()) as { id: number; path: string; url: string; content_type?: string; size?: number; width?: number; height?: number }
        uploaded.push({
          id: created.id,
          path: created.path,
          url: created.url,
          file_name: created.path.split("/").pop() ?? created.path,
          mime_type: created.content_type,
          size_bytes: created.size,
          width: created.width,
          height: created.height,
        })
      } catch (err) {
        failures.push(`${file.name}: ${err instanceof Error ? err.message : "upload failed"}`)
      }
    }

    setUploadStatus(null)
    if (fileInputRef.current) fileInputRef.current.value = ""
    // Newest first matches the default ordering, so prepend rather than refetch.
    if (uploaded.length > 0) {
      setMediaItems((current) => [...uploaded, ...current])
      setTotalCount((current) => current + uploaded.length)
      setNotice(`Uploaded ${String(uploaded.length)} file${uploaded.length === 1 ? "" : "s"}.`)
    }
    if (failures.length > 0) setError(failures.join(" · "))
  }

  const handleDelete = async (item: MediaItem) => {
    if (!window.confirm(`Delete ${item.file_name}? This removes the file from the media server.`)) return
    setError(null)
    setNotice(null)

    try {
      const response = await apiFetch(`/v1/media/${String(item.id)}`, { method: "DELETE" })
      if (!response.ok) {
        setError(await errorMessage(response, `Delete failed (${response.status})`))
        return
      }
      setMediaItems((current) => current.filter((candidate) => candidate.id !== item.id))
      setTotalCount((current) => Math.max(0, current - 1))
      setSelected((current) => (current?.id === item.id ? null : current))
      setNotice(`Deleted ${item.file_name}.`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to delete media.")
    }
  }

  const handleSaveDetails = async (item: MediaItem, altText: string, caption: string, inGallery: boolean) => {
    setError(null)
    setNotice(null)

    try {
      const response = await apiFetch(`/v1/media/${String(item.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ alt_text: altText, caption, in_gallery: inGallery }),
      })
      if (!response.ok) {
        setError(await errorMessage(response, `Save failed (${response.status})`))
        return
      }
      const updated = (await response.json()) as MediaItem
      // Taking an image out of the gallery while filtered to it drops the tile,
      // rather than leaving a row on screen that no longer matches the filter.
      const droppedByFilter = galleryOnly && !updated.in_gallery
      setMediaItems((current) =>
        droppedByFilter
          ? current.filter((candidate) => candidate.id !== updated.id)
          : current.map((candidate) => (candidate.id === updated.id ? updated : candidate)),
      )
      if (droppedByFilter) setTotalCount((current) => Math.max(0, current - 1))
      setSelected(droppedByFilter ? null : updated)
      setNotice("Details saved.")
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to save details.")
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Media</h1>
          {!isLoading && !error && (
            <p className="text-sm text-muted-foreground">
              {totalCount} {galleryOnly ? "gallery image" : "item"}
              {totalCount === 1 ? "" : "s"}
              {search ? ` matching "${search}"` : ""}
            </p>
          )}
        </div>

        {isAdmin && (
          <div className="flex items-center gap-2">
            <input
              accept="image/jpeg,image/png,image/gif,image/webp"
              className="hidden"
              multiple
              onChange={(e) => void handleUpload(e.target.files)}
              ref={fileInputRef}
              type="file"
            />
            <button
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50 transition-colors"
              disabled={uploadStatus !== null}
              onClick={() => fileInputRef.current?.click()}
              type="button"
            >
              <Upload className="w-4 h-4" aria-hidden="true" />
              {uploadStatus ?? "Upload"}
            </button>
          </div>
        )}
      </div>

      {/* Search, and the curated-gallery filter */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[16rem]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
          <input
            aria-label="Search media"
            className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search by file name, alt text, or caption..."
            type="search"
            value={searchInput}
          />
        </div>
        <button
          aria-pressed={galleryOnly}
          className={`inline-flex items-center gap-2 px-3 py-2 rounded-lg border text-sm transition-colors ${
            galleryOnly
              ? "border-primary bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:bg-muted"
          }`}
          onClick={() => setGalleryOnly((current) => !current)}
          title="Show only the images published on the public photo gallery"
          type="button"
        >
          <Images className="w-4 h-4" aria-hidden="true" />
          Photo gallery
        </button>
      </div>

      {notice && (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-muted px-4 py-2 text-sm text-foreground">
          <span>{notice}</span>
          <button aria-label="Dismiss" onClick={() => setNotice(null)} type="button">
            <X className="w-4 h-4" />
          </button>
        </div>
      )}
      {error && (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2 text-sm text-destructive">
          <span>{error}</span>
          <button aria-label="Dismiss" onClick={() => setError(null)} type="button">
            <X className="w-4 h-4" />
          </button>
        </div>
      )}

      {/* Grid */}
      {isLoading ? (
        <div className="text-center text-muted-foreground py-16">Loading media...</div>
      ) : mediaItems.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted-foreground">
          <ImageOff className="w-10 h-10" />
          <p className="text-sm">
            {search
              ? `No results for "${search}"`
              : galleryOnly
                ? "Nothing is on the public photo gallery yet. Open an image and tick “Show on the photo gallery”."
                : "No media items yet."}
          </p>
          {!search && !galleryOnly && isAdmin && (
            <p className="text-xs">
              Upload a file, or run Reindex Media in Settings to pull in already-migrated images.
            </p>
          )}
        </div>
      ) : (
        <>
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
            {mediaItems.map((item) => (
              <div
                key={item.id}
                className="group relative flex flex-col gap-1.5 rounded-xl border border-border bg-card overflow-hidden hover:border-primary transition-colors"
              >
                <button
                  className="relative aspect-square bg-muted overflow-hidden text-left"
                  onClick={() => setSelected(item)}
                  title={item.path}
                  type="button"
                >
                  <img
                    alt={item.alt_text || item.file_name}
                    className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-200"
                    src={item.url}
                    referrerPolicy="no-referrer"
                    loading="lazy"
                  />
                  <div className="absolute inset-0 bg-black/0 group-hover:bg-black/20 transition-colors" />
                  {item.in_gallery && (
                    <span
                      className="absolute bottom-1.5 left-1.5 inline-flex items-center gap-1 rounded-md bg-black/60 px-1.5 py-0.5 text-[10px] font-medium text-white"
                      title="On the public photo gallery"
                    >
                      <Images className="w-3 h-3" aria-hidden="true" />
                      Gallery
                    </span>
                  )}
                </button>
                {isAdmin && (
                  <button
                    className="absolute top-1.5 right-1.5 p-1.5 rounded-lg bg-white/90 text-destructive opacity-0 group-hover:opacity-100 focus:opacity-100 transition-opacity hover:bg-white"
                    onClick={() => void handleDelete(item)}
                    title="Delete"
                    type="button"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                )}
                <div className="px-2 pb-2">
                  <p className="text-xs text-foreground truncate" title={item.file_name}>
                    {item.file_name}
                  </p>
                  <p className="text-[11px] text-muted-foreground">
                    {[item.width && item.height ? `${String(item.width)}×${String(item.height)}` : "", formatBytes(item.size_bytes)]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
              </div>
            ))}
          </div>

          {hasMore && (
            <div className="flex justify-center">
              <button
                className="px-4 py-2 rounded-lg border border-border text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50 transition-colors"
                disabled={isLoadingMore}
                onClick={() => void loadMore()}
                type="button"
              >
                {isLoadingMore ? "Loading..." : `Load more (${String(mediaItems.length)} of ${String(totalCount)})`}
              </button>
            </div>
          )}
        </>
      )}

      {/* Keyed on the asset so selecting a different one remounts the panel
          with fresh field values instead of re-seeding them in an effect. */}
      {selected && (
        <MediaDetailPanel
          key={selected.id}
          item={selected}
          canEdit={isAdmin}
          onClose={() => setSelected(null)}
          onSave={handleSaveDetails}
        />
      )}
    </div>
  )
}

type MediaDetailPanelProps = {
  item: MediaItem
  canEdit: boolean
  onClose: () => void
  onSave: (item: MediaItem, altText: string, caption: string, inGallery: boolean) => Promise<void>
}

function MediaDetailPanel({ item, canEdit, onClose, onSave }: MediaDetailPanelProps) {
  const [altText, setAltText] = useState(item.alt_text ?? "")
  const [caption, setCaption] = useState(item.caption ?? "")
  const [inGallery, setInGallery] = useState(item.in_gallery ?? false)
  const [isSaving, setIsSaving] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [onClose])

  const copyPath = async () => {
    try {
      await navigator.clipboard.writeText(item.path)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  const save = async () => {
    setIsSaving(true)
    await onSave(item, altText, caption, inGallery)
    setIsSaving(false)
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40" onClick={onClose} role="presentation">
      <aside
        aria-label={`Details for ${item.file_name}`}
        className="h-full w-full max-w-md overflow-y-auto bg-background border-l border-border p-6 flex flex-col gap-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3">
          <h2 className="text-lg font-semibold text-foreground break-all">{item.file_name}</h2>
          <button aria-label="Close" className="p-1 text-muted-foreground hover:text-foreground" onClick={onClose} type="button">
            <X className="w-5 h-5" />
          </button>
        </div>

        <img
          alt={item.alt_text || item.file_name}
          className="w-full rounded-lg border border-border bg-muted object-contain max-h-64"
          src={item.url}
          referrerPolicy="no-referrer"
        />

        <dl className="grid grid-cols-3 gap-x-3 gap-y-1.5 text-sm">
          <dt className="text-muted-foreground">Type</dt>
          <dd className="col-span-2 text-foreground">{item.mime_type || "—"}</dd>
          <dt className="text-muted-foreground">Size</dt>
          <dd className="col-span-2 text-foreground">{formatBytes(item.size_bytes) || "—"}</dd>
          <dt className="text-muted-foreground">Dimensions</dt>
          <dd className="col-span-2 text-foreground">
            {item.width && item.height ? `${String(item.width)} × ${String(item.height)}` : "—"}
          </dd>
        </dl>

        <div className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">Path</span>
          <div className="flex items-center gap-2">
            <code className="flex-1 rounded-lg bg-muted px-2 py-1.5 text-xs text-foreground break-all">{item.path}</code>
            <button
              aria-label="Copy path"
              className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
              onClick={() => void copyPath()}
              type="button"
            >
              {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
            </button>
          </div>
        </div>

        <label className="flex flex-col gap-1 text-sm">
          <span className="text-muted-foreground">Alt text</span>
          <input
            className="rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 disabled:opacity-60"
            disabled={!canEdit}
            onChange={(e) => setAltText(e.target.value)}
            placeholder="Describe the image for screen readers"
            value={altText}
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          <span className="text-muted-foreground">Caption</span>
          <textarea
            className="rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 disabled:opacity-60"
            disabled={!canEdit}
            onChange={(e) => setCaption(e.target.value)}
            rows={3}
            value={caption}
          />
        </label>

        {/* The library is every file on the media server, house ads and comics
            included, so the public gallery is opt-in per image. */}
        <label className="flex items-start gap-2 text-sm">
          <input
            checked={inGallery}
            className="mt-0.5 accent-primary disabled:opacity-60"
            disabled={!canEdit}
            onChange={(e) => setInGallery(e.target.checked)}
            type="checkbox"
          />
          <span className="flex flex-col">
            <span className="text-foreground">Show on the photo gallery</span>
            <span className="text-xs text-muted-foreground">
              Readers see this on thetriangle.org/photo once you save.
            </span>
          </span>
        </label>

        {canEdit && (
          <button
            className="self-start px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-50 transition-colors"
            disabled={isSaving}
            onClick={() => void save()}
            type="button"
          >
            {isSaving ? "Saving..." : "Save details"}
          </button>
        )}
      </aside>
    </div>
  )
}

export default MediaView
