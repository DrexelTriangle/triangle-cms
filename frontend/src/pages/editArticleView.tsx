import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ArrowLeft, Save, Image, Search, X } from "lucide-react"
import { useNavigate, useParams } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { publicSiteUrl } from "../auth/urls"
import TrixEditor from "../components/TrixEditor"
import { DateTimeField } from "../components/ui/datetime-field"

// Lazy-loaded so the heavy yoastseo bundle only loads when editing an article.
const SeoAnalysis = lazy(() => import("../components/SeoAnalysis"))

type EditableStatus = "draft" | "published"
type PublishTiming = "draft" | "now" | "schedule"

type ApiArticleDetail = {
  id: number
  title: string
  slug: string
  content: string
  excerpt?: string
  status?: string
  published_date?: string
  comment_status?: string
  featured_image?: string
  breaking_news?: boolean
  categories?: Array<{
    name?: string
    slug?: string
  }>
  authors?: Array<{
    id?: number
    name?: string
  }>
  seo?: {
    seo_title?: string
    meta_description?: string
    focus_keyword?: string
  }
}

type PatchPayload = {
  title: string
  excerpt: string
  content: string
  status: EditableStatus
  published_date?: string
  comment_status: string
  photo_url: string
  breaking_news: boolean
  categories: string[]
  authors: number[]
  focus_keyword: string
  meta_description: string
  seo_title: string
}

type MediaItem = {
  id: string
  url: string
  fileName: string
}

type ApiAuthor = {
  id: number
  display_name: string
}

type TaxonomyItem = {
  id: number
  type: string
  slug: string
  canonical_title: string
  parent_slug?: string | null
}

type AuthorsResponse = {
  authors?: ApiAuthor[]
  pagination?: {
    has_more?: boolean
    hasMore?: boolean
  }
}

// Stored comment_status data isn't perfectly clean (e.g. "Open" vs "open", stray
// whitespace), so fuzzy-match it to the canonical enum the dropdown expects.
// The ETL pipeline normalizes the source, but this keeps the editor resilient.
const normalizeCommentStatus = (raw: string | undefined): string =>
  (raw ?? "").trim().toLowerCase() === "closed" ? "closed" : "open"

const slugifyCategory = (value: string): string =>
  value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")

const toLocalInput = (value?: string): string => {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const localInputToISO = (value: string): string => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toISOString()
}

const isFutureDate = (value: string): boolean => {
  if (!value) return false
  const timestamp = new Date(value).getTime()
  return !Number.isNaN(timestamp) && timestamp > Date.now()
}

const formatPublishDate = (value: string): string => {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}

const PUBLISH_TIMING_OPTIONS: Array<{ value: PublishTiming; label: string; blurb: string }> = [
  { value: "draft", label: "Draft", blurb: "Save without publishing." },
  { value: "now", label: "Publish now", blurb: "Goes live as soon as you save." },
  { value: "schedule", label: "Schedule", blurb: "Goes live at the publish date." },
]

const AUTOSAVE_DELAY_MS = 2500

// ArticleView caches list results in sessionStorage keyed by query; clear those
// entries so the list refetches after an article is created or edited.
const clearArticleListCache = () => {
  if (typeof window === "undefined") return
  const keysToDelete: string[] = []
  for (let i = 0; i < window.sessionStorage.length; i += 1) {
    const key = window.sessionStorage.key(i)
    if (key?.startsWith("articleView:") && key.endsWith(":results")) {
      keysToDelete.push(key)
    }
  }
  for (const key of keysToDelete) {
    window.sessionStorage.removeItem(key)
  }
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
  const isNew = !rawSlug

  const [slugInput, setSlugInput] = useState("")
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isAutoSaving, setIsAutoSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [autoSaveMessage, setAutoSaveMessage] = useState<string | null>(null)
  const currentSnapshotRef = useRef("")
  const lastSavedSnapshotRef = useRef("")
  const snapshotInitializedRef = useRef(false)
  // Name of another editor currently holding the edit lock, or null when we
  // hold it (or it's a new article). When set, editing is blocked so nobody
  // starts work they won't be able to save.
  const [lockedBy, setLockedBy] = useState<string | null>(null)
  const [lockChecking, setLockChecking] = useState(false)

  const [title, setTitle] = useState("")
  const [excerpt, setExcerpt] = useState("")
  const excerptRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const el = excerptRef.current
    if (!el) return
    el.style.height = "auto"
    el.style.height = `${el.scrollHeight}px`
  }, [excerpt])
  const [content, setContent] = useState("")
  const [publishTiming, setPublishTiming] = useState<PublishTiming>("draft")
  const [publishedAt, setPublishedAt] = useState("")
  const [commentStatus, setCommentStatus] = useState("open")
  const [photoURL, setPhotoURL] = useState("")
  const [breakingNews, setBreakingNews] = useState(false)
  const [selectedCategorySlugs, setSelectedCategorySlugs] = useState<string[]>([])
  const [sectionSearch, setSectionSearch] = useState("")
  const [legacyCategoryTitlesBySlug, setLegacyCategoryTitlesBySlug] = useState<Record<string, string>>({})
  const [keyphrase, setKeyphrase] = useState("")
  const [metaDescription, setMetaDescription] = useState("")
  const [seoTitle, setSeoTitle] = useState("")
  const [imagePickerOpen, setImagePickerOpen] = useState(false)
  const [selectedAuthorId, setSelectedAuthorId] = useState<string>("")
  const [currentArticleAuthor, setCurrentArticleAuthor] = useState<ApiAuthor | null>(null)
  const [authorSearch, setAuthorSearch] = useState("")
  const [authors, setAuthors] = useState<ApiAuthor[]>([])
  const [authorsLoading, setAuthorsLoading] = useState(false)
  const [authorsError, setAuthorsError] = useState<string | null>(null)
  const [taxonomyItems, setTaxonomyItems] = useState<TaxonomyItem[]>([])
  const [taxonomyLoading, setTaxonomyLoading] = useState(false)
  const [taxonomyError, setTaxonomyError] = useState<string | null>(null)
  const [mediaItems, setMediaItems] = useState<MediaItem[]>([])
  const [mediaLoading, setMediaLoading] = useState(false)
  const [mediaError, setMediaError] = useState<string | null>(null)
  const [mediaSearch, setMediaSearch] = useState("")
  const [customImageURL, setCustomImageURL] = useState("")
  const articleSnapshot = useMemo(() => JSON.stringify({
    title,
    slugInput: isNew ? slugInput : "",
    excerpt,
    content,
    publishTiming,
    publishedAt: publishTiming === "schedule" ? publishedAt : "",
    commentStatus,
    photoURL,
    breakingNews,
    selectedCategorySlugs,
    selectedAuthorId,
    keyphrase,
    metaDescription,
    seoTitle,
  }), [
    title,
    slugInput,
    isNew,
    excerpt,
    content,
    publishTiming,
    publishedAt,
    commentStatus,
    photoURL,
    breakingNews,
    selectedCategorySlugs,
    selectedAuthorId,
    keyphrase,
    metaDescription,
    seoTitle,
  ])

  useEffect(() => {
    currentSnapshotRef.current = articleSnapshot
  }, [articleSnapshot])

  useEffect(() => {
    if (isLoading || snapshotInitializedRef.current) return
    lastSavedSnapshotRef.current = articleSnapshot
    currentSnapshotRef.current = articleSnapshot
    snapshotInitializedRef.current = true
  }, [articleSnapshot, isLoading])

  useEffect(() => {
    if (isNew) {
      setIsLoading(false)
      return
    }
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
          setKeyphrase(payload.seo?.focus_keyword ?? "")
          setSeoTitle(payload.seo?.seo_title ?? "")
          // Fall back to the excerpt as a starting point when no meta description
          // has been saved yet.
          setMetaDescription(payload.seo?.meta_description ?? payload.excerpt ?? "")
          setContent(payload.content ?? "")
          const payloadStatus = (payload.status ?? "draft").toLowerCase()
          const localPublishedAt = toLocalInput(payload.published_date)
          setPublishTiming(
            payloadStatus === "scheduled" || isFutureDate(localPublishedAt)
              ? "schedule"
              : payloadStatus === "published"
                ? "now"
                : "draft",
          )
          setPublishedAt(localPublishedAt)
          setCommentStatus(normalizeCommentStatus(payload.comment_status))
          setPhotoURL(payload.featured_image ?? "")
          setBreakingNews(Boolean(payload.breaking_news))
          const legacyCategories: Record<string, string> = {}
          const categorySlugs = (payload.categories ?? [])
            .map((category) => {
              const name = (category.name ?? "").trim()
              const categorySlug = slugifyCategory(category.slug ?? name)
              if (categorySlug && name) {
                legacyCategories[categorySlug] = name
              }
              return categorySlug
            })
            .filter((categorySlug) => categorySlug.length > 0)
          setLegacyCategoryTitlesBySlug(legacyCategories)
          setSelectedCategorySlugs([...new Set(categorySlugs)])
          const firstAuthor = payload.authors?.[0]
          const firstAuthorId = firstAuthor?.id
          setSelectedAuthorId(typeof firstAuthorId === "number" ? String(firstAuthorId) : "")
          setCurrentArticleAuthor(
            typeof firstAuthorId === "number"
              ? { id: firstAuthorId, display_name: (firstAuthor?.name ?? "").trim() }
              : null,
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
  }, [apiFetch, slug, isNew])

  // Try to claim an advisory edit lock while this article is open. If someone
  // else already holds it we surface who, block editing, and keep re-checking
  // so the editor unblocks automatically once they leave. The lock is released
  // on unmount (and on tab close via keepalive) and refreshed on a heartbeat so
  // an abandoned session frees it after the server-side TTL.
  const acquireLock = useCallback(async (): Promise<void> => {
    if (isNew || !slug) return
    setLockChecking(true)
    try {
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}/edit-lock`, { method: "PUT" })
      if (response.status === 409) {
        const payload = (await response.json().catch(() => null)) as { holder_name?: string } | null
        setLockedBy(payload?.holder_name?.trim() || "another editor")
        return
      }
      // On success (or an unexpected error) don't block on an advisory lock.
      setLockedBy(null)
    } catch {
      setLockedBy(null)
    } finally {
      setLockChecking(false)
    }
  }, [apiFetch, slug, isNew])

  useEffect(() => {
    if (isNew || !slug) return
    let released = false

    void acquireLock()
    const heartbeat = window.setInterval(() => { void acquireLock() }, 30_000)

    const release = () => {
      if (released) return
      released = true
      void apiFetch(`/v1/articles/${encodeURIComponent(slug)}/edit-lock`, {
        method: "DELETE",
        keepalive: true,
      }).catch(() => {})
    }
    window.addEventListener("beforeunload", release)

    return () => {
      window.clearInterval(heartbeat)
      window.removeEventListener("beforeunload", release)
      release()
    }
  }, [apiFetch, slug, isNew, acquireLock])

  useEffect(() => {
    let cancelled = false

    const fetchAuthors = async () => {
      setAuthorsLoading(true)
      setAuthorsError(null)
      try {
        const pageSize = 200
        let offset = 0
        let hasMore = true
        const allAuthors: ApiAuthor[] = []

        while (hasMore) {
          const response = await apiFetch(`/v1/authors?limit=${pageSize}&offset=${offset}&sort_by=display_name&sort_direction=asc`)
          if (!response.ok) {
            throw new Error(`Authors request failed (${response.status})`)
          }
          const payload = (await response.json()) as ApiAuthor[] | AuthorsResponse
          const authorList = Array.isArray(payload) ? payload : (payload.authors ?? [])
          allAuthors.push(...authorList)

          const apiHasMore = Array.isArray(payload)
            ? undefined
            : (payload.pagination?.has_more ?? payload.pagination?.hasMore)
          hasMore = typeof apiHasMore === "boolean" ? apiHasMore : authorList.length === pageSize
          offset += authorList.length
          if (authorList.length === 0) {
            hasMore = false
          }
        }

        if (!cancelled) {
          setAuthors(allAuthors)
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : "Unable to load authors."
          setAuthorsError(message)
          setAuthors([])
        }
      } finally {
        if (!cancelled) {
          setAuthorsLoading(false)
        }
      }
    }

    void fetchAuthors()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  useEffect(() => {
    let cancelled = false

    const fetchTaxonomy = async () => {
      setTaxonomyLoading(true)
      setTaxonomyError(null)
      try {
        const response = await apiFetch("/v1/taxonomy")
        if (!response.ok) {
          throw new Error(`Taxonomy request failed (${response.status})`)
        }
        const payload = (await response.json()) as TaxonomyItem[]
        if (!cancelled) {
          setTaxonomyItems(
            (Array.isArray(payload) ? payload : []).filter(
              (item) => item.type === "section" || item.type === "subsection",
            ),
          )
        }
      } catch (err) {
        if (!cancelled) {
          const message = err instanceof Error ? err.message : "Unable to load sections."
          setTaxonomyError(message)
          setTaxonomyItems([])
        }
      } finally {
        if (!cancelled) {
          setTaxonomyLoading(false)
        }
      }
    }

    void fetchTaxonomy()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

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
  }, [apiFetch, imagePickerOpen, mediaItems.length, photoURL])

  const saveArticle = async (nextTiming?: PublishTiming, options: { autosave?: boolean } = {}) => {
    const autosave = options.autosave === true
    const snapshotToSave = currentSnapshotRef.current
    const setSavingState = autosave ? setIsAutoSaving : setIsSaving
    const validationError = (message: string, autosaveMessage?: string) => {
      if (autosave) {
        setAutoSaveMessage(autosaveMessage ?? "Autosave paused.")
      } else {
        setError(message)
      }
      setSavingState(false)
    }

    setSavingState(true)
    if (autosave) {
      setAutoSaveMessage("Autosaving...")
    } else {
      setError(null)
      setSuccessMessage(null)
      setAutoSaveMessage(null)
    }

    const effectiveTiming = nextTiming ?? publishTiming
    const effectiveStatus: EditableStatus = effectiveTiming === "draft" ? "draft" : "published"
    const taxonomyBySlug = new Map(taxonomyItems.map((item) => [item.slug, item]))
    const categories = selectedCategorySlugs
      .map((categorySlug) => (
        taxonomyBySlug.get(categorySlug)?.canonical_title
        ?? legacyCategoryTitlesBySlug[categorySlug]
        ?? categorySlug
      ).trim())
      .filter((category) => category.length > 0)

    // Rows with neither an author nor a category are filtered out of the listing
    // as import artifacts. Drafts are exempt from that filter for editors, so
    // this only has to block on the way to published — where the row would
    // otherwise vanish from both the CMS list and the public site.
    if (effectiveStatus === "published" && !selectedAuthorId && categories.length === 0) {
      validationError(
        "Add at least one author or category so the article shows up in the list.",
        "Autosave paused until an author or section is set.",
      )
      return
    }
    const publishedDateISO = effectiveTiming === "schedule" && publishedAt ? localInputToISO(publishedAt) : ""
    if (effectiveTiming === "schedule" && !publishedAt) {
      validationError("Choose a publish date before scheduling.", "Autosave paused until the schedule date is set.")
      return
    }
    if (effectiveTiming === "schedule" && publishedAt && !publishedDateISO) {
      validationError("Publish date is invalid.", "Autosave paused until the schedule date is valid.")
      return
    }
    if (effectiveTiming === "schedule" && !isFutureDate(publishedAt)) {
      validationError("Choose a future publish date, or use Publish now.", "Autosave paused until the schedule date is in the future.")
      return
    }

    try {
      if (isNew) {
        if (!title.trim()) {
          throw new Error("A title is required.")
        }
        const createPayload = {
          title: title.trim(),
          slug: slugInput.trim(),
          content: content.trim(),
          excerpt: excerpt.trim(),
          status: effectiveStatus,
          ...(publishedDateISO ? { published_date: publishedDateISO } : {}),
          comment_status: commentStatus.trim() || "open",
          photo_url: photoURL.trim(),
          breaking_news: breakingNews,
          categories,
          authors: selectedAuthorId ? [Number(selectedAuthorId)] : [],
          focus_keyword: keyphrase.trim(),
          meta_description: metaDescription.trim(),
          seo_title: seoTitle.trim(),
        }
        const response = await apiFetch("/v1/articles", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify(createPayload),
        })
        if (!response.ok) {
          throw new Error(`Create failed (${response.status})`)
        }
        clearArticleListCache()
        setSuccessMessage("Article created.")
        navigate("/articles")
        return
      }

      if (!slug) return

      const payload: PatchPayload = {
        title: title.trim(),
        excerpt: excerpt.trim(),
        content: content.trim(),
        status: effectiveStatus,
        ...(publishedDateISO ? { published_date: publishedDateISO } : {}),
        comment_status: commentStatus.trim() || "open",
        photo_url: photoURL.trim(),
        breaking_news: breakingNews,
        categories,
        authors: selectedAuthorId ? [Number(selectedAuthorId)] : [],
        focus_keyword: keyphrase.trim(),
        meta_description: metaDescription.trim(),
        seo_title: seoTitle.trim(),
      }

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
      if (nextTiming) {
        setPublishTiming(nextTiming)
        if (nextTiming !== "schedule") setPublishedAt("")
      }
      clearArticleListCache()
      lastSavedSnapshotRef.current = snapshotToSave
      if (autosave) {
        setAutoSaveMessage("Autosaved.")
      } else {
        setSuccessMessage("Article saved.")
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to save article."
      if (autosave) {
        setAutoSaveMessage("Autosave failed.")
      } else {
        setError(message)
      }
    } finally {
      setSavingState(false)
    }
  }

  useEffect(() => {
    if (isNew || isLoading || lockedBy || isSaving || isAutoSaving) return
    if (!snapshotInitializedRef.current) return
    if (articleSnapshot === lastSavedSnapshotRef.current) return
    if (publishTiming !== "draft" && !selectedAuthorId && selectedCategorySlugs.length === 0) {
      setAutoSaveMessage("Autosave paused until an author or section is set.")
      return
    }
    if (publishTiming === "schedule" && (!publishedAt || !isFutureDate(publishedAt))) {
      setAutoSaveMessage("Autosave paused until the schedule date is valid.")
      return
    }

    setAutoSaveMessage("Unsaved changes.")
    const timer = window.setTimeout(() => {
      void saveArticle(publishTiming, { autosave: true })
    }, AUTOSAVE_DELAY_MS)

    return () => window.clearTimeout(timer)
  }, [articleSnapshot, isAutoSaving, isLoading, isNew, isSaving, lockedBy, publishTiming, publishedAt, selectedAuthorId, selectedCategorySlugs])

  const inputClass ="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const selectClass = "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const labelClass = "flex flex-col gap-1.5"
  const labelTextClass = "text-xs font-semibold text-muted-foreground uppercase tracking-wide"
  const publishDateInFuture = publishTiming === "schedule" && isFutureDate(publishedAt)
  const publishDateHint = (() => {
    if (publishTiming !== "schedule") return "Publishes immediately."
    if (!publishedAt) return "Choose when this should go live."
    return publishDateInFuture
      ? `Goes on the site ${formatPublishDate(publishedAt)}.`
      : "Choose a future date, or use Publish now."
  })()
  const publishActionLabel = publishTiming === "schedule" ? "Schedule" : publishTiming === "now" ? "Publish" : "Save Draft"
  const publishSavingLabel = publishTiming === "schedule" ? "Scheduling..." : publishTiming === "now" ? "Publishing..." : "Saving..."
  const taxonomyBySlug = useMemo(() => new Map(taxonomyItems.map((item) => [item.slug, item])), [taxonomyItems])
  const categoryGroups = useMemo(() => {
    const sections = taxonomyItems
      .filter((item) => item.type === "section")
      .sort((left, right) => left.canonical_title.localeCompare(right.canonical_title))
    const subsectionsByParent = new Map<string, TaxonomyItem[]>()
    for (const item of taxonomyItems) {
      if (item.type !== "subsection") continue
      const parentSlug = item.parent_slug ?? ""
      if (!subsectionsByParent.has(parentSlug)) {
        subsectionsByParent.set(parentSlug, [])
      }
      subsectionsByParent.get(parentSlug)?.push(item)
    }
    for (const subsections of subsectionsByParent.values()) {
      subsections.sort((left, right) => left.canonical_title.localeCompare(right.canonical_title))
    }
    return sections.map((section) => ({
      section,
      subsections: subsectionsByParent.get(section.slug) ?? [],
    }))
  }, [taxonomyItems])
  const legacyCategoryChoices = useMemo(() => (
    selectedCategorySlugs
      .filter((categorySlug) => !taxonomyBySlug.has(categorySlug))
      .map((categorySlug) => ({
        slug: categorySlug,
        title: legacyCategoryTitlesBySlug[categorySlug] ?? categorySlug,
      }))
  ), [legacyCategoryTitlesBySlug, selectedCategorySlugs, taxonomyBySlug])
  const visibleCategoryGroups = useMemo(() => {
    const query = sectionSearch.trim().toLowerCase()
    if (!query) return categoryGroups

    return categoryGroups.flatMap(({ section, subsections }) => {
      const sectionMatches = section.canonical_title.toLowerCase().includes(query)
      const sectionSelected = selectedCategorySlugs.includes(section.slug)
      const visibleSubsections = subsections.filter((subsection) => (
        subsection.canonical_title.toLowerCase().includes(query)
        || selectedCategorySlugs.includes(subsection.slug)
      ))

      if (sectionMatches) {
        return [{ section, subsections }]
      }
      if (sectionSelected || visibleSubsections.length > 0) {
        return [{ section, subsections: visibleSubsections }]
      }
      return []
    })
  }, [categoryGroups, sectionSearch, selectedCategorySlugs])
  const visibleLegacyCategoryChoices = useMemo(() => {
    const query = sectionSearch.trim().toLowerCase()
    if (!query) return legacyCategoryChoices
    return legacyCategoryChoices.filter((category) => (
      category.title.toLowerCase().includes(query)
      || selectedCategorySlugs.includes(category.slug)
    ))
  }, [legacyCategoryChoices, sectionSearch, selectedCategorySlugs])
  const visibleSectionChoiceCount = useMemo(() => (
    visibleCategoryGroups.reduce((count, group) => count + 1 + group.subsections.length, 0)
    + visibleLegacyCategoryChoices.length
  ), [visibleCategoryGroups, visibleLegacyCategoryChoices])
  const visibleAuthors = useMemo(() => {
    const query = authorSearch.trim().toLowerCase()
    const namedAuthors = authors.filter((author) => author.display_name.trim().length > 0)
    const matches = query
      ? namedAuthors.filter((author) => author.display_name.toLowerCase().includes(query))
      : namedAuthors

    if (!selectedAuthorId) {
      return matches
    }

    const selectedAuthor = namedAuthors.find((author) => String(author.id) === selectedAuthorId)
      ?? (currentArticleAuthor && String(currentArticleAuthor.id) === selectedAuthorId && currentArticleAuthor.display_name.trim()
        ? currentArticleAuthor
        : null)
    if (!selectedAuthor) {
      return matches
    }

    return [
      selectedAuthor,
      ...matches.filter((author) => String(author.id) !== selectedAuthorId),
    ]
  }, [authorSearch, authors, currentArticleAuthor, selectedAuthorId])
  const toggleCategory = (categorySlug: string) => {
    setSelectedCategorySlugs((current) => (
      current.includes(categorySlug)
        ? current.filter((slugValue) => slugValue !== categorySlug)
        : [...current, categorySlug]
    ))
  }

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
        <h1 className="text-2xl font-bold text-foreground">{isNew ? "New Article" : "Edit Article"}</h1>
      </div>

      {isLoading ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-muted-foreground">
          Loading article...
        </div>
      ) : lockedBy ? (
        <div className="rounded-xl border border-border bg-card p-8 flex flex-col items-center gap-4 text-center">
          <h2 className="text-lg font-semibold text-foreground">This article is being edited</h2>
          <p className="max-w-md text-sm text-muted-foreground">
            <span className="font-medium text-foreground">{lockedBy}</span> is currently editing this article.
            To avoid overwriting each other's work, editing is locked until they're done. This page will
            unlock automatically once they leave.
          </p>
          <div className="flex items-center gap-2 pt-1">
            <button
              className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              disabled={lockChecking}
              onClick={() => void acquireLock()}
              type="button"
            >
              {lockChecking ? "Checking..." : "Check again"}
            </button>
            <button
              className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-muted text-foreground text-sm font-medium hover:bg-muted/70 transition-colors"
              onClick={() => navigate(-1)}
              type="button"
            >
              Back
            </button>
          </div>
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
                  <img alt="Selected featured" className="w-24 h-16 object-cover rounded-md flex-shrink-0" src={photoURL} referrerPolicy="no-referrer" />
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
                ref={excerptRef}
                className={`${inputClass} resize-none overflow-hidden min-h-[80px]`}
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
          <aside className="flex flex-col gap-6">
            <div className="flex flex-col gap-5 rounded-xl border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">Publish</h2>

            <label className={labelClass}>
              <span className={labelTextClass}>Slug</span>
              {isNew ? (
                <input
                  className={inputClass}
                  onChange={(e) => setSlugInput(e.target.value)}
                  placeholder="Auto-generated from title if left blank"
                  type="text"
                  value={slugInput}
                />
              ) : (
                <input className={`${inputClass} bg-muted/50 text-muted-foreground cursor-default`} readOnly type="text" value={slug} />
              )}
            </label>

            <div className={labelClass}>
              <span className={labelTextClass}>Publish timing</span>
              <div className="grid grid-cols-1 gap-2">
                {PUBLISH_TIMING_OPTIONS.map((option) => (
                  <label
                    key={option.value}
                    className={`cursor-pointer rounded-lg border px-3 py-2.5 transition-colors ${
                      publishTiming === option.value
                        ? "border-primary bg-primary/5 ring-1 ring-primary/30"
                        : "border-border hover:bg-muted/40"
                    }`}
                  >
                    <div className="flex items-center gap-2">
                      <input
                        type="radio"
                        name="article-publish-timing"
                        className="accent-primary"
                        checked={publishTiming === option.value}
                        onChange={() => {
                          setPublishTiming(option.value)
                          if (option.value !== "schedule") setPublishedAt("")
                        }}
                      />
                      <span className="text-sm font-medium text-foreground">{option.label}</span>
                    </div>
                    <p className="text-xs text-muted-foreground mt-1 pl-6">{option.blurb}</p>
                  </label>
                ))}
              </div>
            </div>

            {publishTiming === "schedule" ? (
              <DateTimeField
                label="Publish date"
                value={publishedAt}
                onChange={setPublishedAt}
                clearable
                hint={publishDateHint}
              />
            ) : null}

            <div className={labelClass}>
              <span className={labelTextClass}>Author</span>
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  className={`${inputClass} pl-9`}
                  onChange={(e) => setAuthorSearch(e.target.value)}
                  placeholder="Search author name..."
                  type="search"
                  value={authorSearch}
                />
              </div>
              <div className="max-h-44 overflow-y-auto rounded-lg border border-border bg-background p-2">
                {authorsLoading ? (
                  <p className="px-2 py-3 text-xs text-muted-foreground">Loading authors...</p>
                ) : visibleAuthors.length === 0 ? (
                  <p className="px-2 py-3 text-xs text-muted-foreground">
                    {authorSearch.trim() ? `No authors found for "${authorSearch}"` : "No authors available."}
                  </p>
                ) : (
                  <div className="flex flex-col gap-1">
                    <label className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                      <input
                        checked={selectedAuthorId === ""}
                        className="h-4 w-4 border-border"
                        onChange={() => setSelectedAuthorId("")}
                        type="radio"
                      />
                      <span className="font-medium text-foreground">No author</span>
                    </label>
                    {visibleAuthors.map((author) => (
                      <label key={author.id} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                        <input
                          checked={selectedAuthorId === String(author.id)}
                          className="h-4 w-4 border-border"
                          onChange={() => setSelectedAuthorId(String(author.id))}
                          type="radio"
                        />
                        <span className="text-foreground">{author.display_name}</span>
                      </label>
                    ))}
                  </div>
                )}
              </div>
              {authorSearch.trim() && !authorsLoading ? (
                <span className="text-[11px] text-muted-foreground">
                  {visibleAuthors.length} match{visibleAuthors.length === 1 ? "" : "es"}
                </span>
              ) : null}
            </div>
            {authorsError ? (
              <p className="text-xs text-destructive bg-destructive/10 rounded-lg px-3 py-2">
                {authorsError}
              </p>
            ) : null}

            <label className={labelClass}>
              <span className={labelTextClass}>Comment Status</span>
              <select className={selectClass} onChange={(e) => setCommentStatus(e.target.value)} value={commentStatus}>
                <option value="open">Open</option>
                <option value="closed">Closed</option>
              </select>
            </label>

            <label className="flex items-start gap-3 rounded-lg border border-border bg-background px-3 py-3 text-sm">
              <input
                checked={breakingNews}
                className="mt-0.5 h-4 w-4 rounded border-border"
                onChange={(e) => setBreakingNews(e.target.checked)}
                type="checkbox"
              />
              <span className="font-medium text-foreground">Breaking news</span>
            </label>

            <div className={labelClass}>
              <span className={labelTextClass}>Sections</span>
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  className={`${inputClass} pl-9`}
                  onChange={(e) => setSectionSearch(e.target.value)}
                  placeholder="Search sections..."
                  type="search"
                  value={sectionSearch}
                />
              </div>
              <div className="max-h-64 overflow-y-auto rounded-lg border border-border bg-background p-2">
                {taxonomyLoading ? (
                  <p className="px-2 py-3 text-xs text-muted-foreground">Loading sections...</p>
                ) : visibleCategoryGroups.length === 0 && visibleLegacyCategoryChoices.length === 0 ? (
                  <p className="px-2 py-3 text-xs text-muted-foreground">
                    {sectionSearch.trim() ? `No sections found for "${sectionSearch}"` : "No sections available."}
                  </p>
                ) : (
                  <div className="flex flex-col gap-2">
                    {visibleCategoryGroups.map(({ section, subsections }) => (
                      <div key={section.slug} className="flex flex-col gap-1">
                        <label className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                          <input
                            checked={selectedCategorySlugs.includes(section.slug)}
                            className="h-4 w-4 rounded border-border"
                            onChange={() => toggleCategory(section.slug)}
                            type="checkbox"
                          />
                          <span className="font-medium text-foreground">{section.canonical_title}</span>
                        </label>
                        {subsections.length > 0 ? (
                          <div className="ml-6 flex flex-col gap-0.5">
                            {subsections.map((subsection) => (
                              <label key={subsection.slug} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                                <input
                                  checked={selectedCategorySlugs.includes(subsection.slug)}
                                  className="h-4 w-4 rounded border-border"
                                  onChange={() => toggleCategory(subsection.slug)}
                                  type="checkbox"
                                />
                                <span className="text-muted-foreground">{subsection.canonical_title}</span>
                              </label>
                            ))}
                          </div>
                        ) : null}
                      </div>
                    ))}
                    {visibleLegacyCategoryChoices.length > 0 ? (
                      <div className="border-t border-border pt-2">
                        {visibleLegacyCategoryChoices.map((category) => (
                          <label key={category.slug} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                            <input
                              checked={selectedCategorySlugs.includes(category.slug)}
                              className="h-4 w-4 rounded border-border"
                              onChange={() => toggleCategory(category.slug)}
                              type="checkbox"
                            />
                            <span className="text-muted-foreground">{category.title}</span>
                          </label>
                        ))}
                      </div>
                    ) : null}
                  </div>
                )}
              </div>
              {sectionSearch.trim() && !taxonomyLoading ? (
                <span className="text-[11px] text-muted-foreground">
                  {visibleSectionChoiceCount} match{visibleSectionChoiceCount === 1 ? "" : "es"}
                </span>
              ) : null}
              {taxonomyError ? (
                <p className="text-xs text-destructive bg-destructive/10 rounded-lg px-3 py-2">
                  {taxonomyError}
                </p>
              ) : null}
            </div>

            <label className={labelClass}>
              <span className={labelTextClass}>Focus Keyphrase</span>
              <input
                className={inputClass}
                onChange={(e) => setKeyphrase(e.target.value)}
                placeholder="e.g. campus housing"
                type="text"
                value={keyphrase}
              />
            </label>

            <label className={labelClass}>
              <span className={labelTextClass}>SEO Title</span>
              <input
                className={inputClass}
                onChange={(e) => setSeoTitle(e.target.value)}
                placeholder="Defaults to the article title"
                type="text"
                value={seoTitle}
              />
            </label>

            <label className={labelClass}>
              <span className={labelTextClass}>Meta Description</span>
              <textarea
                className={`${inputClass} resize-y min-h-[72px]`}
                onChange={(e) => setMetaDescription(e.target.value)}
                placeholder="Search-result summary (aim for under 156 characters)"
                value={metaDescription}
              />
              <span className="text-[11px] text-muted-foreground">{metaDescription.length} characters</span>
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
            {!isNew && (isAutoSaving || autoSaveMessage) ? (
              <p className="text-xs text-muted-foreground bg-muted/60 rounded-lg px-3 py-2">
                {isAutoSaving ? "Autosaving..." : autoSaveMessage}
              </p>
            ) : null}

            <div className="flex flex-col gap-2 pt-1">
              <button
                className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={isSaving}
                onClick={() => void saveArticle(publishTiming)}
                type="button"
              >
                <Save className="w-4 h-4" aria-hidden="true" />
                {isSaving ? publishSavingLabel : publishActionLabel}
              </button>
            </div>
            </div>

            <Suspense
              fallback={(
                <div className="rounded-xl border border-border bg-card p-6 text-xs text-muted-foreground">
                  Loading SEO analysis…
                </div>
              )}
            >
              <SeoAnalysis
                content={content}
                keyphrase={keyphrase}
                title={seoTitle.trim() || title}
                description={metaDescription}
                slug={isNew ? slugInput : slug}
                permalink={`${publicSiteUrl()}/${isNew ? slugInput : slug}`}
              />
            </Suspense>
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
                          referrerPolicy="no-referrer"
                          loading="lazy"
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
