import { useEffect, useState } from "react"
import { Search, Upload, Trash2, ImageOff } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type MediaItem = {
  id: string
  url: string
  fileName: string
  fileSize?: string
}

const normalizeMediaItems = (payload: unknown): MediaItem[] => {
  const asRecord = (value: unknown): Record<string, unknown> | null =>
    value && typeof value === "object" ? (value as Record<string, unknown>) : null
  const root = asRecord(payload)
  const source = Array.isArray(payload)
    ? payload
    : Array.isArray(root?.items)
      ? root.items
      : Array.isArray(root?.media)
        ? root.media
        : []

  const items = source
    .map((raw) => asRecord(raw))
    .filter((item): item is Record<string, unknown> => Boolean(item))
    .map((item, index) => {
      const url = String(item.url ?? item.photo_url ?? "").trim()
      const fileName = String(item.file_name ?? item.title ?? item.name ?? url).trim()
      const id = String(item.id ?? item.media_id ?? index)
      return { id, url, fileName }
    })
    .filter((item) => item.url.length > 0)

  const deduped = new Map<string, MediaItem>()
  for (const item of items) {
    if (!deduped.has(item.url)) deduped.set(item.url, item)
  }
  return [...deduped.values()]
}

function MediaView() {
  const apiFetch = useApiFetch()
  const [mediaItems, setMediaItems] = useState<MediaItem[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState("")

  useEffect(() => {
    let cancelled = false
    const fetchMedia = async () => {
      setIsLoading(true)
      setError(null)
      try {
        const response = await apiFetch("/v1/media?limit=200")
        if (!response.ok) throw new Error(`Request failed (${response.status})`)
        const payload = (await response.json()) as unknown
        if (!cancelled) setMediaItems(normalizeMediaItems(payload))
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : "Unable to load media."
          setError(message)
        }
      } finally {
        if (!cancelled) setIsLoading(false)
      }
    }
    void fetchMedia()
    return () => { cancelled = true }
  }, [apiFetch])

  const filtered = mediaItems.filter((item) =>
    item.fileName.toLowerCase().includes(searchQuery.trim().toLowerCase()),
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-foreground">Media</h1>
        <button
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          type="button"
        >
          <Upload className="w-4 h-4" aria-hidden="true" />
          Upload
        </button>
      </div>

      {/* Search */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          aria-label="Search media"
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder="Search media..."
          type="search"
          value={searchQuery}
        />
      </div>

      {/* Grid */}
      {isLoading ? (
        <div className="text-center text-muted-foreground py-16">Loading media...</div>
      ) : error ? (
        <div className="text-center text-destructive py-16">{error}</div>
      ) : filtered.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted-foreground">
          <ImageOff className="w-10 h-10" />
          <p className="text-sm">{searchQuery ? `No results for "${searchQuery}"` : "No media items yet."}</p>
        </div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4">
          {filtered.map((item) => (
            <div key={`${item.id}-${item.url}`} className="group relative flex flex-col gap-1.5 rounded-xl border border-border bg-card overflow-hidden hover:border-primary transition-colors">
              <div className="relative aspect-square bg-muted overflow-hidden">
                <img
                  alt={item.fileName || "Media"}
                  className="w-full h-full object-cover group-hover:scale-105 transition-transform duration-200"
                  src={item.url}
                  referrerPolicy="no-referrer"
                  loading="lazy"
                />
                <div className="absolute inset-0 bg-black/0 group-hover:bg-black/20 transition-colors" />
                <button
                  className="absolute top-1.5 right-1.5 p-1.5 rounded-lg bg-white/90 text-destructive opacity-0 group-hover:opacity-100 transition-opacity hover:bg-white"
                  title="Delete"
                  type="button"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </button>
              </div>
              <p className="px-2 pb-2 text-xs text-muted-foreground truncate">{item.fileName || item.url}</p>
            </div>
          ))}
        </div>
      )}

      {!isLoading && !error && (
        <p className="text-xs text-muted-foreground">{filtered.length} item{filtered.length === 1 ? "" : "s"}</p>
      )}
    </div>
  )
}

export default MediaView
