import { useEffect, useMemo, useState } from "react"
import { ArrowLeft, Save, Image, Search, X } from "lucide-react"
import { useNavigate, useParams } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import TrixEditor from "../components/TrixEditor"

type EditableStatus = "draft" | "published"

type ApiArticleDetail = {
  id: number
  title: string
  slug: string
  content: string
  excerpt?: string
  status?: string
  comment_status?: string
  featured_image?: string
  categories?: Array<{
    name?: string
  }>
}

type PatchPayload = {
  title: string
  excerpt: string
  content: string
  status: EditableStatus
  comment_status: string
  photo_url: string
  categories: string[]
}

type MediaItem = {
  id: string
  url: string
  fileName: string
}

const normalizeMediaItems = (payload: unknown): MediaItem[] => {
  const asRecord = (value: unknown): Record<string, unknown> | null => (value && typeof value === "object" ? (value as Record<string, unknown>) : null)
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
    if (!deduped.has(item.url)) {
      deduped.set(item.url, item)
    }
  }
  return [...deduped.values()]
}

function EditArticleView() {
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const { slug: rawSlug } = useParams<{ slug: string }>()
  const slug = useMemo(() => (rawSlug ? decodeURIComponent(rawSlug) : ""), [rawSlug])

  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)

  const [title, setTitle] = useState("")
  const [excerpt, setExcerpt] = useState("")
  const [content, setContent] = useState("")
  const [status, setStatus] = useState<EditableStatus>("draft")
  const [commentStatus, setCommentStatus] = useState("open")
  const [photoURL, setPhotoURL] = useState("")
  const [categoriesInput, setCategoriesInput] = useState("")
  const [imagePickerOpen, setImagePickerOpen] = useState(false)
  const [mediaItems, setMediaItems] = useState<MediaItem[]>([])
  const [mediaLoading, setMediaLoading] = useState(false)
  const [mediaError, setMediaError] = useState<string | null>(null)
  const [mediaSearch, setMediaSearch] = useState("")
  const [customImageURL, setCustomImageURL] = useState("")

  useEffect(() => {
    if (!slug) {
      setError("Missing article slug.")
      setIsLoading(false)
      return
    }

    let cancelled = false
    const fetchArticle = async () => {
      setIsLoading(true)
      setError(null)
      setSuccessMessage(null)
      try {
        const response = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}`)
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`)
        }
        const payload = (await response.json()) as ApiArticleDetail
        if (!cancelled) {
          setTitle(payload.title ?? "")
          setExcerpt(payload.excerpt ?? "")
          setContent(payload.content ?? "")
          setStatus((payload.status ?? "draft").toLowerCase() === "published" ? "published" : "draft")
          setCommentStatus(payload.comment_status ?? "open")
          setPhotoURL(payload.featured_image ?? "")
          setCategoriesInput(
            (payload.categories ?? [])
              .map((category) => (category.name ?? "").trim())
              .filter((name) => name.length > 0)
              .join(", "),
          )
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : "Unable to load article."
          setError(message)
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false)
        }
      }
    }

    void fetchArticle()
    return () => {
      cancelled = true
    }
  }, [slug])

  useEffect(() => {
    if (!imagePickerOpen) return
    let cancelled = false

    setCustomImageURL(photoURL)
    setMediaSearch("")
    setMediaError(null)

    const fetchMedia = async () => {
      if (mediaItems.length > 0) return
      setMediaLoading(true)
      try {
        const response = await apiFetch("/v1/media?limit=200")
        if (!response.ok) {
          throw new Error(`Media request failed (${response.status})`)
        }
        const payload = (await response.json()) as unknown
        if (cancelled) return
        setMediaItems(normalizeMediaItems(payload))
      } catch (err) {
        if (cancelled) return
        const message = err instanceof Error ? err.message : "Unable to load media items."
        setMediaError(message)
      } finally {
        if (!cancelled) {
          setMediaLoading(false)
        }
      }
    }

    void fetchMedia()
    return () => {
      cancelled = true
    }
  }, [imagePickerOpen, mediaItems.length, photoURL])

  const saveArticle = async (nextStatus?: EditableStatus) => {
    if (!slug) return

    setIsSaving(true)
    setError(null)
    setSuccessMessage(null)

    const payload: PatchPayload = {
      title: title.trim(),
      excerpt: excerpt.trim(),
      content: content.trim(),
      status: nextStatus ?? status,
      comment_status: commentStatus.trim() || "open",
      photo_url: photoURL.trim(),
      categories: categoriesInput
        .split(",")
        .map((category) => category.trim())
        .filter((category) => category.length > 0),
    }

    try {
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}`, {
        method: "PATCH",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        throw new Error(`Save failed (${response.status})`)
      }
      if (nextStatus) {
        setStatus(nextStatus)
      }
      setSuccessMessage("Article saved.")
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to save article."
      setError(message)
    } finally {
      setIsSaving(false)
    }
  }

  const inputClass = "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const selectClass = "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const labelClass = "flex flex-col gap-1.5"
  const labelTextClass = "text-xs font-semibold text-muted-foreground uppercase tracking-wide"

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center gap-4">
        <button
          className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
          onClick={() => navigate(-1)}
          type="button"
        >
          <ArrowLeft className="w-4 h-4" aria-hidden="true" />
          Back
        </button>
        <h1 className="text-2xl font-bold text-foreground">Edit Article</h1>
      </div>

      {isLoading ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-muted-foreground">
          Loading article...
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-[1fr_300px] gap-6 items-start">
          {/* Main content */}
          <div className="flex flex-col gap-5 rounded-xl border border-border bg-card p-6">
            <label className={labelClass}>
              <span className={labelTextClass}>Title</span>
              <input className={inputClass} onChange={(e) => setTitle(e.target.value)} type="text" value={title} />
            </label>

            <div className={labelClass}>
              <span className={labelTextClass}>Featured Image</span>
              {photoURL ? (
                <div className="flex items-start gap-3 p-3 rounded-lg border border-border bg-muted/30">
                  <img alt="Selected featured" className="w-24 h-16 object-cover rounded-md flex-shrink-0" src={photoURL} />
                  <div className="flex flex-col gap-2">
                    <button
                      className="text-xs font-medium text-primary hover:underline text-left"
                      onClick={() => setImagePickerOpen(true)}
                      type="button"
                    >
                      Change image
                    </button>
                    <button
                      className="inline-flex items-center gap-1 text-xs font-medium text-destructive hover:underline"
                      onClick={() => setPhotoURL("")}
                      type="button"
                    >
                      <X className="w-3 h-3" />
                      Remove
                    </button>
                  </div>
                </div>
              ) : (
                <button
                  aria-label="Select image"
                  className="flex flex-col items-center justify-center gap-2 h-28 rounded-lg border-2 border-dashed border-border hover:border-primary hover:bg-primary/5 transition-colors text-muted-foreground hover:text-primary"
                  onClick={() => setImagePickerOpen(true)}
                  type="button"
                >
                  <Image className="w-6 h-6" />
                  <span className="text-xs font-medium">Click to select image</span>
                </button>
              )}
            </div>

            <label className={labelClass}>
              <span className={labelTextClass}>Excerpt</span>
              <textarea
                className={`${inputClass} resize-y min-h-[80px]`}
                onChange={(e) => setExcerpt(e.target.value)}
                value={excerpt}
              />
            </label>

            <div className={labelClass}>
              <span className={labelTextClass}>Content</span>
              <TrixEditor onChange={setContent} value={content} />
            </div>
          </div>

          {/* Sidebar */}
          <aside className="flex flex-col gap-5 rounded-xl border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">Publish</h2>

            <label className={labelClass}>
              <span className={labelTextClass}>Slug</span>
              <input className={`${inputClass} bg-muted/50 text-muted-foreground cursor-default`} readOnly type="text" value={slug} />
            </label>

            <label className={labelClass}>
              <span className={labelTextClass}>Status</span>
              <select className={selectClass} onChange={(e) => setStatus(e.target.value as EditableStatus)} value={status}>
                <option value="draft">Draft</option>
                <option value="published">Published</option>
              </select>
            </label>

            <label className={labelClass}>
              <span className={labelTextClass}>Comment Status</span>
              <input className={inputClass} onChange={(e) => setCommentStatus(e.target.value)} type="text" value={commentStatus} />
            </label>

            <label className={labelClass}>
              <span className={labelTextClass}>Categories (comma-separated)</span>
              <input className={inputClass} onChange={(e) => setCategoriesInput(e.target.value)} type="text" value={categoriesInput} />
            </label>

            {error && (
              <p className="text-xs text-destructive bg-destructive/10 rounded-lg px-3 py-2">
                {error}
              </p>
            )}
            {successMessage && (
              <p className="text-xs text-green-700 dark:text-green-400 bg-green-100 dark:bg-green-900/20 rounded-lg px-3 py-2">
                {successMessage}
              </p>
            )}

            <div className="flex flex-col gap-2 pt-1">
              <button
                className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-muted text-foreground text-sm font-medium hover:bg-muted/70 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={isSaving}
                onClick={() => void saveArticle("draft")}
                type="button"
              >
                <Save className="w-4 h-4" aria-hidden="true" />
                {isSaving ? "Saving..." : "Save Draft"}
              </button>
              <button
                className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={isSaving}
                onClick={() => void saveArticle("published")}
                type="button"
              >
                {isSaving ? "Publishing..." : "Publish"}
              </button>
            </div>
          </aside>
        </div>
      )}

      {/* Image picker modal */}
      {imagePickerOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4"
          role="dialog"
          aria-modal="true"
          aria-label="Select image"
        >
          <div className="flex flex-col w-full max-w-2xl max-h-[85vh] rounded-xl border border-border bg-card shadow-2xl overflow-hidden">
            {/* Modal header */}
            <div className="flex items-center justify-between px-5 py-4 border-b border-border">
              <h3 className="text-base font-semibold text-foreground">Select image</h3>
              <button
                className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                onClick={() => setImagePickerOpen(false)}
                type="button"
                aria-label="Close"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            {/* Search */}
            <div className="px-5 py-3 border-b border-border">
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                  onChange={(e) => setMediaSearch(e.target.value)}
                  placeholder="Search media..."
                  type="search"
                  value={mediaSearch}
                />
              </div>
            </div>

            {/* Media grid */}
            <div className="flex-1 overflow-y-auto p-4">
              {mediaLoading ? (
                <p className="text-center text-sm text-muted-foreground py-8">Loading media...</p>
              ) : mediaItems.length === 0 ? (
                <p className="text-center text-sm text-muted-foreground py-8">
                  {mediaError ?? "No media items available yet. You can paste an image URL below."}
                </p>
              ) : (
                <div className="grid grid-cols-3 sm:grid-cols-4 gap-3">
                  {mediaItems
                    .filter((item) => item.fileName.toLowerCase().includes(mediaSearch.trim().toLowerCase()))
                    .map((item) => (
                      <button
                        className="flex flex-col gap-1.5 p-1.5 rounded-lg border border-border hover:border-primary hover:bg-primary/5 transition-colors text-left"
                        key={`${item.id}-${item.url}`}
                        onClick={() => {
                          setPhotoURL(item.url)
                          setImagePickerOpen(false)
                        }}
                        type="button"
                      >
                        <img
                          alt={item.fileName || "Media"}
                          className="w-full aspect-square object-cover rounded-md bg-muted"
                          src={item.url}
                        />
                        <span className="text-xs text-muted-foreground truncate w-full">{item.fileName || item.url}</span>
                      </button>
                    ))}
                </div>
              )}
            </div>

            {/* Footer: paste URL */}
            <div className="flex gap-2 px-5 py-4 border-t border-border bg-muted/30">
              <input
                className="flex-1 px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                onChange={(e) => setCustomImageURL(e.target.value)}
                placeholder="Or paste image URL"
                type="url"
                value={customImageURL}
              />
              <button
                className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors whitespace-nowrap"
                onClick={() => {
                  if (!customImageURL.trim()) return
                  setPhotoURL(customImageURL.trim())
                  setImagePickerOpen(false)
                }}
                type="button"
              >
                Use URL
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default EditArticleView
