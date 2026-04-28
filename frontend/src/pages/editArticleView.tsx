import { buttonVariants } from "@cloudflare/kumo"
import { useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, FloppyDiskIcon, ImageIcon, MagnifyingGlassIcon, XIcon } from "@phosphor-icons/react"
import { useNavigate, useParams } from "react-router-dom"

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
        const response = await fetch(`/v1/articles/${encodeURIComponent(slug)}`)
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
        const response = await fetch("/v1/media?limit=200")
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
      const response = await fetch(`/v1/articles/${encodeURIComponent(slug)}`, {
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

  return (
    <div className="article-edit-page">
      <div className="article-edit-header">
        <button className="article-edit-back-button" onClick={() => navigate(-1)} type="button">
          <ArrowLeftIcon aria-hidden="true" />
          Back
        </button>
        <h1 className="article-list-title">Edit Article</h1>
      </div>

      {isLoading ? (
        <div className="article-edit-card">
          <p>Loading article...</p>
        </div>
      ) : (
        <div className="article-edit-layout">
          <section className="article-edit-card article-edit-content-block">
            <label className="article-edit-field">
              <span className="article-filter-label">Title</span>
              <input className="article-search-input" onChange={(e) => setTitle(e.target.value)} type="text" value={title} />
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Featured Image</span>
              {photoURL ? (
                <div className="article-image-selector-card">
                  <img alt="Selected featured" className="article-image-selector-preview" src={photoURL} />
                  <div className="article-image-selector-actions">
                    <button className="article-edit-link-button" onClick={() => setImagePickerOpen(true)} type="button">
                      Change
                    </button>
                    <button className="article-action-button danger" onClick={() => setPhotoURL("")} type="button">
                      <XIcon className="article-action-icon" />
                    </button>
                  </div>
                </div>
              ) : (
                <button
                  aria-label="Select image"
                  className="article-image-selector-empty"
                  onClick={() => setImagePickerOpen(true)}
                  type="button"
                >
                  <ImageIcon className="article-image-selector-empty-icon" />
                </button>
              )}
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Excerpt</span>
              <textarea className="article-edit-textarea article-edit-textarea-sm" onChange={(e) => setExcerpt(e.target.value)} value={excerpt} />
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Content</span>
              <textarea className="article-edit-textarea article-edit-textarea-lg" onChange={(e) => setContent(e.target.value)} value={content} />
            </label>
          </section>

          <aside className="article-edit-card article-edit-publish-block">
            <h2 className="article-edit-block-title article-edit-publish-title">Publish</h2>

            <label className="article-edit-field">
              <span className="article-filter-label">Slug</span>
              <input className="article-search-input" readOnly type="text" value={slug} />
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Status</span>
              <select className="article-filter-select" onChange={(e) => setStatus(e.target.value as EditableStatus)} value={status}>
                <option value="draft">Draft</option>
                <option value="published">Published</option>
              </select>
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Comment Status</span>
              <input className="article-filter-select" onChange={(e) => setCommentStatus(e.target.value)} type="text" value={commentStatus} />
            </label>

            <label className="article-edit-field">
              <span className="article-filter-label">Categories (comma-separated)</span>
              <input className="article-search-input" onChange={(e) => setCategoriesInput(e.target.value)} type="text" value={categoriesInput} />
            </label>

            {error && <p className="article-edit-error">Failed to load/save article: {error}</p>}
            {successMessage && <span className="article-edit-success">{successMessage}</span>}

            <div className="article-edit-actions article-edit-actions-stacked">
              <button className={`${buttonVariants()} article-add-new-button`} disabled={isSaving} onClick={() => void saveArticle("draft")} type="button">
                <FloppyDiskIcon aria-hidden="true" className="me-2 h-4 w-4" />
                {isSaving ? "Saving..." : "Save Draft"}
              </button>
              <button className="article-publish-button article-publish-button-wide" disabled={isSaving} onClick={() => void saveArticle("published")} type="button">
                {isSaving ? "Publishing..." : "Publish"}
              </button>
            </div>
          </aside>
        </div>
      )}

      {imagePickerOpen && (
        <div className="article-image-modal-backdrop" role="dialog">
          <div className="article-image-modal-card">
            <div className="article-image-modal-header">
              <h3 className="article-image-modal-title">Select image</h3>
              <button className="article-action-button" onClick={() => setImagePickerOpen(false)} type="button">
                <XIcon className="article-action-icon" />
              </button>
            </div>

            <div className="article-image-modal-search-wrap">
              <MagnifyingGlassIcon className="article-search-icon" />
              <input
                className="article-search-input"
                onChange={(e) => setMediaSearch(e.target.value)}
                placeholder="Search media..."
                type="search"
                value={mediaSearch}
              />
            </div>

            <div className="article-image-modal-grid">
              {mediaLoading ? (
                <p className="article-count">Loading media...</p>
              ) : mediaItems.length === 0 ? (
                <p className="article-count">
                  {mediaError ?? "No media items available yet. You can paste an image URL below."}
                </p>
              ) : (
                mediaItems
                  .filter((item) => item.fileName.toLowerCase().includes(mediaSearch.trim().toLowerCase()))
                  .map((item) => (
                    <button
                      className="article-image-option"
                      key={`${item.id}-${item.url}`}
                      onClick={() => {
                        setPhotoURL(item.url)
                        setImagePickerOpen(false)
                      }}
                      type="button"
                    >
                      <img alt={item.fileName || "Media"} className="article-image-option-thumb" src={item.url} />
                      <span className="article-image-option-name">{item.fileName || item.url}</span>
                    </button>
                  ))
              )}
            </div>

            <div className="article-image-modal-footer">
              <input
                className="article-search-input"
                onChange={(e) => setCustomImageURL(e.target.value)}
                placeholder="Or paste image URL"
                type="url"
                value={customImageURL}
              />
              <button
                className="article-publish-button"
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
