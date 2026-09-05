import { readErrorMessage } from "../lib/apiError"
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react"
import type { KeyboardEvent } from "react"
import { ArrowLeft, Save, Image, Search, X, Copy, Check, RefreshCw, Plus } from "lucide-react"
import { useNavigate, useParams } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { articleUrl } from "../auth/urls"
import TrixEditor from "../components/TrixEditor"
import MediaPicker from "../components/MediaPicker"
import { copyText } from "../lib/clipboard"
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
  featured_image_alt?: string
  breaking_news?: boolean
  is_featured?: boolean
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
    canonical_url?: string
    noindex?: boolean
    tags?: Array<{
      name?: string
      slug?: string
    }>
  }
}

type PatchPayload = {
  title: string
  excerpt: string
  content: string
  // Only sent when the editor regenerated the slug, and never by an autosave:
  // changing it moves the article's public URL.
  slug?: string
  // Both are omitted by autosave: a publish transition only happens when the
  // editor presses the publish button.
  status?: EditableStatus
  published_date?: string
  comment_status: string
  photo_url: string
  photo_alt: string
  breaking_news: boolean
  is_featured: boolean
  categories: string[]
  tags: string[]
  authors: number[]
  focus_keyword: string
  meta_description: string
  seo_title: string
  canonical_url: string
  noindex: boolean
}

// A tag the archive already uses, with how many articles carry it. Aggregated
// server-side from the articles themselves; there is no tags table.
type PopularTag = {
  name: string
  uses: number
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

// Mirrors NormalizeCanonicalURL in the backend (database/http_models.go): blank
// means "no override", anything else must be an absolute http(s) URL. Checked
// here too so a half-typed URL surfaces as inline help rather than as a 400 on
// the next autosave.
const isValidCanonicalUrl = (value: string): boolean => {
  const trimmed = value.trim()
  if (!trimmed) return true
  let parsed: URL
  try {
    parsed = new URL(trimmed)
  } catch {
    return false
  }
  return (parsed.protocol === "http:" || parsed.protocol === "https:") && parsed.host !== ""
}

// Mirrors db.CanonicalizeSlug on the server: lowercase, every run of
// non-alphanumerics collapsed to a single dash, no leading or trailing dash.
// Anything else is rejected as non-canonical by the API.
const slugify = (value: string): string =>
  value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")

const parseSEOTags = (value: string): string[] => {
  const seen = new Set<string>()
  return value
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => {
      if (!tag) return false
      const key = tag.toLowerCase()
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
}

// How many suggestions sit under the tag box. The list is a shortcut for the
// handful of tags a desk adds to nearly every article, so a longer row would
// cost more to read than typing the tag.
const SEO_TAG_SUGGESTION_LIMIT = 8

// How long the tag box sits still before its contents are searched. Long enough
// that typing a whole word is one request rather than six; short enough that the
// results arrive while the editor is still looking at the box.
const TAG_SEARCH_DEBOUNCE_MS = 200

// The suggestions worth showing, out of whichever set the caller passes: the
// popular tags when the box is empty, the search results once it is not.
//
// The text filter is applied here as well as on the server, and that is the
// point: it narrows the tags already on screen on the keystroke, without waiting
// for the request, and it drops results belonging to an earlier query. The
// server's ordering is preserved rather than re-ranked, so the row does not
// reshuffle under a person reaching for it.
const filterTagSuggestions = (
  candidates: PopularTag[],
  selected: string[],
  draft: string,
): string[] => {
  const taken = new Set(selected.map((tag) => tag.toLowerCase()))
  const query = draft.trim().toLowerCase()
  const matches: string[] = []
  for (const tag of candidates) {
    const name = tag.name.toLowerCase()
    if (taken.has(name)) continue
    if (query && !name.includes(query)) continue
    matches.push(tag.name)
    if (matches.length === SEO_TAG_SUGGESTION_LIMIT) break
  }
  return matches
}

// Appends whatever tags are in `raw` (Enter commits one, a pasted list commits
// several) to the already-committed ones, skipping case-insensitive duplicates.
// Returns the original array when nothing new was added so the caller can avoid
// a pointless state update.
const addSEOTags = (existing: string[], raw: string): string[] => {
  const seen = new Set(existing.map((tag) => tag.toLowerCase()))
  const added = parseSEOTags(raw).filter((tag) => {
    const key = tag.toLowerCase()
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
  return added.length > 0 ? [...existing, ...added] : existing
}

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

function EditArticleView() {
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const { id: rawID, slug: rawSlug } = useParams<{ id?: string; slug: string }>()
  const slug = useMemo(() => (rawSlug ? decodeURIComponent(rawSlug) : ""), [rawSlug])
  const articleID = useMemo(() => (rawID && /^\d+$/.test(rawID) ? rawID : ""), [rawID])
  const articleQuery = articleID ? `?id=${encodeURIComponent(articleID)}` : ""
  const articleApiPath = slug ? `/v1/articles/${encodeURIComponent(slug)}${articleQuery}` : ""
  const isNew = !rawSlug

  const [slugInput, setSlugInput] = useState("")
  // A slug regenerated for an existing article, held until the next explicit
  // save: the URL an article is already reachable at must not move under a
  // background autosave. Null means "keep the slug on file".
  const [pendingSlug, setPendingSlug] = useState<string | null>(null)
  const [slugRegenerating, setSlugRegenerating] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [isAutoSaving, setIsAutoSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [autoSaveMessage, setAutoSaveMessage] = useState<string | null>(null)
  const currentSnapshotRef = useRef("")
  const lastSavedSnapshotRef = useRef("")
  const snapshotInitializedRef = useRef(false)
  // Publish timing as it is actually stored, which is not the same thing as the
  // radio selection: picking a timing is intent, and only the publish button
  // commits it. Autosave reads these so it can never move an article between
  // draft and live on its own.
  const savedTimingRef = useRef<PublishTiming>("draft")
  const savedPublishedAtRef = useRef("")
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
  const [photoAlt, setPhotoAlt] = useState("")
  const [breakingNews, setBreakingNews] = useState(false)
  const [isFeatured, setIsFeatured] = useState(false)
  const [selectedCategorySlugs, setSelectedCategorySlugs] = useState<string[]>([])
  const [sectionSearch, setSectionSearch] = useState("")
  const [legacyCategoryTitlesBySlug, setLegacyCategoryTitlesBySlug] = useState<Record<string, string>>({})
  const [keyphrase, setKeyphrase] = useState("")
  const [metaDescription, setMetaDescription] = useState("")
  const [seoTitle, setSeoTitle] = useState("")
  const [canonicalUrl, setCanonicalUrl] = useState("")
  const [noIndex, setNoIndex] = useState(false)
  const [seoTags, setSeoTags] = useState<string[]>([])
  // Text sitting in the tag box that has not been committed to a chip yet. It is
  // still saved, so a half-typed tag is not silently dropped by an autosave.
  const [seoTagDraft, setSeoTagDraft] = useState("")
  // Tag suggestions. A failed fetch leaves this empty and shows nothing: the
  // suggestions are a shortcut, and an error message next to a working text box
  // would be noise.
  const [popularTags, setPopularTags] = useState<PopularTag[]>([])
  // Matches for what is currently in the tag box, searched over every tag the
  // archive has rather than only the popular ones. A beat tag from 2019 is
  // exactly what nobody remembers the spelling of.
  const [tagSearchResults, setTagSearchResults] = useState<PopularTag[]>([])
  const [imagePickerOpen, setImagePickerOpen] = useState(false)
  const [linkCopied, setLinkCopied] = useState(false)
  // Byline order is selection order: the list is sent as-is and rendered in that
  // order downstream, so the first author checked leads the byline.
  const [selectedAuthorIds, setSelectedAuthorIds] = useState<number[]>([])
  const [currentArticleAuthors, setCurrentArticleAuthors] = useState<ApiAuthor[]>([])
  const [authorSearch, setAuthorSearch] = useState("")
  const [authors, setAuthors] = useState<ApiAuthor[]>([])
  const [authorsLoading, setAuthorsLoading] = useState(false)
  const [authorsError, setAuthorsError] = useState<string | null>(null)
  const [taxonomyItems, setTaxonomyItems] = useState<TaxonomyItem[]>([])
  const [taxonomyLoading, setTaxonomyLoading] = useState(false)
  const [taxonomyError, setTaxonomyError] = useState<string | null>(null)
  // Publish timing is deliberately absent: it is not autosaved, so including it
  // would both trigger a save the editor did not ask for and then mark the
  // uncommitted timing as saved.
  const articleSnapshot = useMemo(() => JSON.stringify({
    title,
    slugInput: isNew ? slugInput : "",
    excerpt,
    content,
    commentStatus,
    photoURL,
    photoAlt,
    breakingNews,
    isFeatured,
    selectedCategorySlugs,
    seoTags,
    seoTagDraft,
    selectedAuthorIds,
    keyphrase,
    metaDescription,
    seoTitle,
    canonicalUrl,
    noIndex,
  }), [
    title,
    slugInput,
    isNew,
    excerpt,
    content,
    commentStatus,
    photoURL,
    photoAlt,
    breakingNews,
    isFeatured,
    selectedCategorySlugs,
    seoTags,
    seoTagDraft,
    selectedAuthorIds,
    keyphrase,
    metaDescription,
    seoTitle,
    canonicalUrl,
    noIndex,
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
        const response = await apiFetch(articleApiPath)
        if (!response.ok) {
          throw new Error(await readErrorMessage(response, `Could not load article (${response.status})`))
        }
        const payload = (await response.json()) as ApiArticleDetail
        if (!cancelled) {
          setTitle(payload.title ?? "")
          setExcerpt(payload.excerpt ?? "")
          setKeyphrase(payload.seo?.focus_keyword ?? "")
          setSeoTitle(payload.seo?.seo_title ?? "")
          setCanonicalUrl(payload.seo?.canonical_url ?? "")
          setNoIndex(payload.seo?.noindex ?? false)
          setSeoTags(addSEOTags([], (payload.seo?.tags ?? [])
            .map((tag) => (tag.name ?? "").trim())
            .filter((tag) => tag.length > 0)
            .join(", ")))
          setSeoTagDraft("")
          // Fall back to the excerpt as a starting point when no meta description
          // has been saved yet.
          setMetaDescription(payload.seo?.meta_description ?? payload.excerpt ?? "")
          setContent(payload.content ?? "")
          const payloadStatus = (payload.status ?? "draft").toLowerCase()
          const localPublishedAt = toLocalInput(payload.published_date)
          const loadedTiming: PublishTiming = payloadStatus === "scheduled" || isFutureDate(localPublishedAt)
            ? "schedule"
            : payloadStatus === "published"
              ? "now"
              : "draft"
          setPublishTiming(loadedTiming)
          setPublishedAt(localPublishedAt)
          savedTimingRef.current = loadedTiming
          savedPublishedAtRef.current = loadedTiming === "schedule" ? localPublishedAt : ""
          setCommentStatus(normalizeCommentStatus(payload.comment_status))
          setPhotoURL(payload.featured_image ?? "")
          setPhotoAlt(payload.featured_image_alt ?? "")
          setBreakingNews(Boolean(payload.breaking_news))
          setIsFeatured(Boolean(payload.is_featured))
          const legacyCategories: Record<string, string> = {}
          const categorySlugs = (payload.categories ?? [])
            .map((category) => {
              const name = (category.name ?? "").trim()
              const categorySlug = slugify(category.slug ?? name)
              if (categorySlug && name) {
                legacyCategories[categorySlug] = name
              }
              return categorySlug
            })
            .filter((categorySlug) => categorySlug.length > 0)
          setLegacyCategoryTitlesBySlug(legacyCategories)
          setSelectedCategorySlugs([...new Set(categorySlugs)])
          // Keep the server's order: it is the byline order readers see.
          const articleAuthors = (payload.authors ?? [])
            .filter((author): author is { id: number; name?: string } => typeof author.id === "number")
            .map((author) => ({ id: author.id, display_name: (author.name ?? "").trim() }))
          setSelectedAuthorIds([...new Set(articleAuthors.map((author) => author.id))])
          setCurrentArticleAuthors(articleAuthors)
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
  }, [apiFetch, articleApiPath, slug, isNew])

  // Try to claim an advisory edit lock while this article is open. If someone
  // else already holds it we surface who, block editing, and keep re-checking
  // so the editor unblocks automatically once they leave. The lock is released
  // on unmount (and on tab close via keepalive) and refreshed on a heartbeat so
  // an abandoned session frees it after the server-side TTL.
  const acquireLock = useCallback(async (): Promise<void> => {
    if (isNew || !slug) return
    setLockChecking(true)
    try {
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}/edit-lock${articleQuery}`, { method: "PUT" })
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
  }, [apiFetch, articleQuery, slug, isNew])

  useEffect(() => {
    if (isNew || !slug) return
    let released = false

    void acquireLock()
    const heartbeat = window.setInterval(() => { void acquireLock() }, 30_000)

    const release = () => {
      if (released) return
      released = true
      void apiFetch(`/v1/articles/${encodeURIComponent(slug)}/edit-lock${articleQuery}`, {
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
  }, [apiFetch, articleQuery, slug, isNew, acquireLock])

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

  // Tags the desk already uses, for the suggestions under the tag box. Fetched
  // once per editor session: the server caches the ranking for minutes anyway,
  // so re-fetching per keystroke would buy nothing.
  useEffect(() => {
    let cancelled = false

    const fetchPopularTags = async () => {
      try {
        const response = await apiFetch("/v1/tags")
        if (!response.ok) {
          throw new Error(`Popular tags request failed (${response.status})`)
        }
        const payload = (await response.json()) as PopularTag[]
        if (!cancelled) {
          setPopularTags(Array.isArray(payload) ? payload : [])
        }
      } catch {
        // Silent by design; see the popularTags declaration.
        if (!cancelled) {
          setPopularTags([])
        }
      }
    }

    void fetchPopularTags()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  // Search the archive for whatever is in the tag box. Debounced, and dropped
  // if the box has moved on by the time the answer arrives: responses can land
  // out of order, and stale matches for a prefix the editor has already typed
  // past are worse than none.
  useEffect(() => {
    const query = seoTagDraft.trim()
    if (!query) {
      setTagSearchResults([])
      return
    }

    let cancelled = false
    const timer = setTimeout(() => {
      const search = async () => {
        try {
          const response = await apiFetch(
            `/v1/tags?limit=${SEO_TAG_SUGGESTION_LIMIT}&q=${encodeURIComponent(query)}`,
          )
          if (!response.ok) {
            throw new Error(`Tag search failed (${response.status})`)
          }
          const payload = (await response.json()) as PopularTag[]
          if (!cancelled) {
            setTagSearchResults(Array.isArray(payload) ? payload : [])
          }
        } catch {
          // Silent by design: the popular tags and the text box both still
          // work, so an error banner would be noise.
          if (!cancelled) {
            setTagSearchResults([])
          }
        }
      }
      void search()
    }, TAG_SEARCH_DEBOUNCE_MS)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [apiFetch, seoTagDraft])

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

    // Autosave saves content against the timing already on file; only an
    // explicit save is allowed to act on the radio selection.
    const effectiveTiming = autosave ? savedTimingRef.current : (nextTiming ?? publishTiming)
    const effectiveStatus: EditableStatus = effectiveTiming === "draft" ? "draft" : "published"
    const taxonomyBySlug = new Map(taxonomyItems.map((item) => [item.slug, item]))
    const categories = selectedCategorySlugs
      .map((categorySlug) => (
        taxonomyBySlug.get(categorySlug)?.canonical_title
        ?? legacyCategoryTitlesBySlug[categorySlug]
        ?? categorySlug
      ).trim())
      .filter((category) => category.length > 0)
    const seoTagsToSave = addSEOTags(seoTags, seoTagDraft)

    // Rows with neither an author nor a category are filtered out of the listing
    // as import artifacts. Drafts are exempt from that filter for editors, so
    // this only has to block on the way to published — where the row would
    // otherwise vanish from both the CMS list and the public site.
    if (effectiveStatus === "published" && selectedAuthorIds.length === 0 && categories.length === 0) {
      validationError(
        "Add at least one author or category so the article shows up in the list.",
        "Autosave paused until an author or section is set.",
      )
      return
    }
    // Only an explicit save sends a date, so a half-edited schedule date must not
    // hold the content autosave hostage.
    const publishedDateISO = !autosave && effectiveTiming === "schedule" && publishedAt ? localInputToISO(publishedAt) : ""
    if (!autosave && effectiveTiming === "schedule" && !publishedAt) {
      validationError("Choose a publish date before scheduling.", "Autosave paused until the schedule date is set.")
      return
    }
    if (!autosave && effectiveTiming === "schedule" && publishedAt && !publishedDateISO) {
      validationError("Publish date is invalid.", "Autosave paused until the schedule date is valid.")
      return
    }
    if (!autosave && effectiveTiming === "schedule" && !isFutureDate(publishedAt)) {
      validationError("Choose a future publish date, or use Publish now.", "Autosave paused until the schedule date is in the future.")
      return
    }

    // The API rejects a malformed canonical URL, so sending one mid-keystroke
    // would turn every autosave into a failed save. Pause instead, the same way
    // a half-entered schedule date does.
    if (!isValidCanonicalUrl(canonicalUrl)) {
      validationError(
        "Canonical URL must be an absolute http(s) URL, or left blank.",
        "Autosave paused until the canonical URL is valid.",
      )
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
          photo_alt: photoAlt.trim(),
          breaking_news: breakingNews,
          is_featured: isFeatured,
          categories,
          tags: seoTagsToSave,
          authors: selectedAuthorIds,
          focus_keyword: keyphrase.trim(),
          meta_description: metaDescription.trim(),
          seo_title: seoTitle.trim(),
          canonical_url: canonicalUrl.trim(),
          noindex: noIndex,
        }
        const response = await apiFetch("/v1/articles", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify(createPayload),
        })
        if (!response.ok) {
          throw new Error(await readErrorMessage(response, `Create failed (${response.status})`))
        }
        clearArticleListCache()
        setSuccessMessage("Article created.")
        // The server owns the slug: a title or slug another article already uses
        // comes back with a suffix. Land on the new article rather than the list
        // so the editor is looking at the slug that was actually stored.
        const created = (await response.json().catch(() => null)) as { id?: number; slug?: string } | null
        if (created?.id && created.slug) {
          navigate(`/articles/${encodeURIComponent(String(created.id))}/${encodeURIComponent(created.slug)}/edit`, {
            replace: true,
          })
        } else {
          navigate("/articles")
        }
        return
      }

      if (!slug) return

      // Captured before the request so a regenerate landing mid-save cannot make
      // the redirect below disagree with what was actually sent.
      const slugToSave = !autosave && pendingSlug && pendingSlug !== slug ? pendingSlug : ""

      const payload: PatchPayload = {
        title: title.trim(),
        excerpt: excerpt.trim(),
        content: content.trim(),
        ...(slugToSave ? { slug: slugToSave } : {}),
        // An autosave omits both fields entirely; the handler leaves pub_date and
        // scheduled_pub_date untouched when neither is present, so the article
        // stays exactly as published (or as draft) as the editor left it.
        ...(autosave ? {} : { status: effectiveStatus }),
        ...(publishedDateISO ? { published_date: publishedDateISO } : {}),
        comment_status: commentStatus.trim() || "open",
        photo_url: photoURL.trim(),
        photo_alt: photoAlt.trim(),
        breaking_news: breakingNews,
        is_featured: isFeatured,
        categories,
        tags: seoTagsToSave,
        authors: selectedAuthorIds,
        focus_keyword: keyphrase.trim(),
        meta_description: metaDescription.trim(),
        seo_title: seoTitle.trim(),
        canonical_url: canonicalUrl.trim(),
        noindex: noIndex,
      }

      const response = await apiFetch(articleApiPath, {
        method: "PATCH",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        // A rename onto a slug another article holds comes back 409 with a
        // message worth showing: the generic text would read as a save that
        // failed for no reason.
        throw new Error(await readErrorMessage(response, `Save failed (${response.status})`))
      }
      if (nextTiming) {
        setPublishTiming(nextTiming)
        if (nextTiming !== "schedule") setPublishedAt("")
      }
      if (!autosave) {
        // The transition is now on file, so later autosaves save against it.
        savedTimingRef.current = effectiveTiming
        savedPublishedAtRef.current = effectiveTiming === "schedule" ? publishedAt : ""
      }
      clearArticleListCache()
      lastSavedSnapshotRef.current = snapshotToSave
      if (autosave) {
        setAutoSaveMessage("Autosaved.")
      } else {
        setSuccessMessage("Article saved.")
      }
      if (slugToSave) {
        // The route still points at the old slug, which no longer resolves, so
        // move the editor onto the new one before anything refetches.
        setPendingSlug(null)
        const basePath = articleID ? `/articles/${encodeURIComponent(articleID)}` : "/articles"
        navigate(`${basePath}/${encodeURIComponent(slugToSave)}/edit`, { replace: true })
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
    // Keyed to the stored timing, not the radio: a live article must not lose its
    // byline, and a draft the editor is only thinking about publishing is still a
    // draft as far as autosave is concerned.
    if (savedTimingRef.current !== "draft" && selectedAuthorIds.length === 0 && selectedCategorySlugs.length === 0) {
      setAutoSaveMessage("Autosave paused until an author or section is set.")
      return
    }

    setAutoSaveMessage("Unsaved changes.")
    const timer = window.setTimeout(() => {
      void saveArticle(undefined, { autosave: true })
    }, AUTOSAVE_DELAY_MS)

    return () => window.clearTimeout(timer)
  }, [articleApiPath, articleID, articleSnapshot, isAutoSaving, isLoading, isNew, isSaving, lockedBy, selectedAuthorIds, selectedCategorySlugs])

  const inputClass ="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const selectClass = "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
  const labelClass = "flex flex-col gap-1.5"
  const labelTextClass = "text-xs font-semibold text-muted-foreground uppercase tracking-normal"
  const commitSeoTagDraft = () => {
    if (!seoTagDraft.trim()) {
      setSeoTagDraft("")
      return
    }
    setSeoTags((current) => addSEOTags(current, seoTagDraft))
    setSeoTagDraft("")
  }

  // Search results lead, because they are ranked by how well they match and
  // they cover the whole archive. The popular tags follow as the instant
  // fallback: they are already in memory, so the row narrows on the keystroke
  // instead of sitting empty until the search comes back.
  const tagSuggestions = useMemo(() => {
    const candidates = seoTagDraft.trim()
      ? [...tagSearchResults, ...popularTags.filter(
          (tag) => !tagSearchResults.some((match) => match.name.toLowerCase() === tag.name.toLowerCase()),
        )]
      : popularTags
    return filterTagSuggestions(candidates, seoTags, seoTagDraft)
  }, [popularTags, tagSearchResults, seoTags, seoTagDraft])

  // Clicking a suggestion also clears the draft: the click is the editor
  // finishing the word they had started, so leaving "dre" in the box would
  // commit it as a second tag at the next blur.
  const addSuggestedSeoTag = (tag: string) => {
    setSeoTags((current) => addSEOTags(current, tag))
    setSeoTagDraft("")
  }

  const handleSeoTagKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter" || event.key === ",") {
      // Enter would otherwise submit the form; a comma is accepted as a second
      // way to commit so pasted comma-separated lists still work.
      event.preventDefault()
      commitSeoTagDraft()
      return
    }
    if (event.key === "Backspace" && seoTagDraft === "" && seoTags.length > 0) {
      event.preventDefault()
      setSeoTags((current) => current.slice(0, -1))
    }
  }

  const publishDateInFuture = publishTiming === "schedule" && isFutureDate(publishedAt)
  const autoSaveStatusText = !isNew && (isAutoSaving || autoSaveMessage)
    ? (isAutoSaving ? "Autosaving..." : autoSaveMessage)
    : null
  const autoSaveStatusClass = (() => {
    if (!autoSaveStatusText) return ""
    if (autoSaveStatusText === "Unsaved changes.") {
      return "border-yellow-300 bg-yellow-50 text-yellow-800 dark:border-yellow-900/60 dark:bg-yellow-950/30 dark:text-yellow-300"
    }
    if (autoSaveStatusText === "Autosave failed.") {
      return "border-destructive/30 bg-destructive/10 text-destructive"
    }
    if (autoSaveStatusText.startsWith("Autosave paused")) {
      return "border-yellow-300 bg-yellow-50 text-yellow-800 dark:border-yellow-900/60 dark:bg-yellow-950/30 dark:text-yellow-300"
    }
    return "border-green-300 bg-green-50 text-green-800 dark:border-green-900/60 dark:bg-green-950/30 dark:text-green-300"
  })()
  const publishDateHint = (() => {
    if (publishTiming !== "schedule") return "Publishes immediately."
    if (!publishedAt) return "Choose when this should go live."
    return publishDateInFuture
      ? `Goes on the site ${formatPublishDate(publishedAt)}.`
      : "Choose a future date, or use Publish now."
  })()
  const publishActionLabel = publishTiming === "schedule" ? "Schedule" : publishTiming === "now" ? "Publish" : "Save Draft"
  // Autosave deliberately leaves publish timing alone, so say so: otherwise a
  // green "Autosaved." next to a freshly picked timing reads as if the timing
  // went live too.
  const timingChangePending = !isNew && (
    publishTiming !== savedTimingRef.current
    || (publishTiming === "schedule" && publishedAt !== savedPublishedAtRef.current)
  )
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
  // Checked sections and subsections are pinned to the top of the list and stay
  // there through a search, for the same reason the authors list pins its
  // selection: filing an article under a section you can no longer see is how a
  // section assignment silently gets dropped.
  const visibleCategoryGroups = useMemo(() => {
    const query = sectionSearch.trim().toLowerCase()
    const pinSelectedFirst = (subsections: TaxonomyItem[]) => [
      ...subsections.filter((subsection) => selectedCategorySlugs.includes(subsection.slug)),
      ...subsections.filter((subsection) => !selectedCategorySlugs.includes(subsection.slug)),
    ]

    const groups = !query
      ? categoryGroups.map(({ section, subsections }) => ({
        section,
        subsections: pinSelectedFirst(subsections),
      }))
      : categoryGroups.flatMap(({ section, subsections }) => {
        const sectionMatches = section.canonical_title.toLowerCase().includes(query)
        const sectionSelected = selectedCategorySlugs.includes(section.slug)
        const visibleSubsections = subsections.filter((subsection) => (
          subsection.canonical_title.toLowerCase().includes(query)
          || selectedCategorySlugs.includes(subsection.slug)
        ))

        if (sectionMatches) {
          return [{ section, subsections: pinSelectedFirst(subsections) }]
        }
        if (sectionSelected || visibleSubsections.length > 0) {
          return [{ section, subsections: pinSelectedFirst(visibleSubsections) }]
        }
        return []
      })

    const groupSelected = ({ section, subsections }: { section: TaxonomyItem; subsections: TaxonomyItem[] }) => (
      selectedCategorySlugs.includes(section.slug)
      || subsections.some((subsection) => selectedCategorySlugs.includes(subsection.slug))
    )
    return [
      ...groups.filter((group) => groupSelected(group)),
      ...groups.filter((group) => !groupSelected(group)),
    ]
  }, [categoryGroups, sectionSearch, selectedCategorySlugs])
  const visibleLegacyCategoryChoices = useMemo(() => {
    const query = sectionSearch.trim().toLowerCase()
    const matches = !query
      ? legacyCategoryChoices
      : legacyCategoryChoices.filter((category) => (
        category.title.toLowerCase().includes(query)
        || selectedCategorySlugs.includes(category.slug)
      ))
    return [
      ...matches.filter((category) => selectedCategorySlugs.includes(category.slug)),
      ...matches.filter((category) => !selectedCategorySlugs.includes(category.slug)),
    ]
  }, [legacyCategoryChoices, sectionSearch, selectedCategorySlugs])
  const visibleSectionChoiceCount = useMemo(() => (
    visibleCategoryGroups.reduce((count, group) => count + 1 + group.subsections.length, 0)
    + visibleLegacyCategoryChoices.length
  ), [visibleCategoryGroups, visibleLegacyCategoryChoices])
  // Selected authors sit at the top, in byline order, and stay visible through a
  // search that would otherwise hide them. Unchecking someone you can no longer
  // see is how a byline silently loses a name.
  const visibleAuthors = useMemo(() => {
    const query = authorSearch.trim().toLowerCase()
    const namedAuthors = authors.filter((author) => author.display_name.trim().length > 0)
    const byId = new Map(namedAuthors.map((author) => [author.id, author]))
    for (const author of currentArticleAuthors) {
      if (!byId.has(author.id) && author.display_name.trim()) byId.set(author.id, author)
    }

    const selected = selectedAuthorIds
      .map((id) => byId.get(id))
      .filter((author): author is ApiAuthor => Boolean(author))
    const matches = query
      ? namedAuthors.filter((author) => author.display_name.toLowerCase().includes(query))
      : namedAuthors

    return [
      ...selected,
      ...matches.filter((author) => !selectedAuthorIds.includes(author.id)),
    ]
  }, [authorSearch, authors, currentArticleAuthors, selectedAuthorIds])
  // On a new article the slug the server will assign is not known until it is
  // saved, so only offer the link once there is a real one.
  const effectiveSlug = isNew ? "" : slug
  // Article slugs carry no uniqueness constraint, so handing two articles the
  // same one would make the public URL ambiguous. Probe the API for the plain
  // slug and fall back to -2, -3, ... the way WordPress did.
  const findFreeSlug = async (base: string): Promise<string> => {
    for (let suffix = 1; suffix <= 20; suffix += 1) {
      const candidate = suffix === 1 ? base : `${base}-${suffix}`
      if (candidate === slug) return candidate
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(candidate)}`)
      if (response.status === 404) return candidate
      if (!response.ok) {
        throw new Error(`Slug check failed (${response.status})`)
      }
    }
    throw new Error(`Every slug from "${base}" to "${base}-20" is already taken.`)
  }

  const regenerateSlug = async () => {
    const base = slugify(title)
    if (!base) {
      setError("Add a title before regenerating the slug.")
      return
    }
    setError(null)
    setSuccessMessage(null)
    setSlugRegenerating(true)
    try {
      const next = await findFreeSlug(base)
      if (isNew) {
        setSlugInput(next)
      } else {
        setPendingSlug(next === slug ? null : next)
        if (next === slug) {
          setSuccessMessage("The slug already matches the title.")
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to regenerate the slug.")
    } finally {
      setSlugRegenerating(false)
    }
  }

  const copyArticleLink = async () => {
    if (!effectiveSlug) return
    if (await copyText(articleUrl(effectiveSlug))) {
      setLinkCopied(true)
      setTimeout(() => setLinkCopied(false), 1500)
      return
    }
    setLinkCopied(false)
    setError("Could not copy the link. Your browser blocked clipboard access.")
  }
  const bylinePreview = useMemo(() => {
    const namesById = new Map([...authors, ...currentArticleAuthors].map((author) => [author.id, author.display_name]))
    const names = selectedAuthorIds.map((id) => namesById.get(id)?.trim() || `#${String(id)}`)
    if (names.length <= 2) return names.join(" and ")
    return `${names.slice(0, -1).join(", ")}, and ${names[names.length - 1]}`
  }, [authors, currentArticleAuthors, selectedAuthorIds])
  const toggleAuthor = (authorId: number) => {
    setSelectedAuthorIds((current) => (
      current.includes(authorId)
        ? current.filter((id) => id !== authorId)
        : [...current, authorId]
    ))
  }
  // Checking a subsection also files the article under its parent section. A
  // subsection alone leaves the article off the parent's own section page, which
  // is never what an editor picking "Welcome Week" under "Special Editions"
  // means. Unchecking the subsection deliberately leaves the parent in place --
  // it is a legitimate standalone choice, and removing it silently would undo an
  // explicit selection.
  const toggleCategory = (categorySlug: string) => {
    const parentSlug = taxonomyBySlug.get(categorySlug)?.parent_slug?.trim()
    setSelectedCategorySlugs((current) => {
      if (current.includes(categorySlug)) {
        return current.filter((slugValue) => slugValue !== categorySlug)
      }
      const next = [...current, categorySlug]
      return parentSlug && !next.includes(parentSlug) ? [...next, parentSlug] : next
    })
  }

  return (
    <div className="flex flex-col gap-6 p-6">

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
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
        {autoSaveStatusText ? (
          <p className={`rounded-lg border px-3 py-2 text-xs font-medium sm:ml-auto ${autoSaveStatusClass}`}>
            {autoSaveStatusText}
          </p>
        ) : null}
      </div>

      {isLoading ? (
        <div className="rounded-lg border border-border bg-card p-8 text-center text-muted-foreground">
          Loading article...
        </div>
      ) : lockedBy ? (
        <div className="rounded-lg border border-border bg-card p-8 flex flex-col items-center gap-4 text-center">
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

          <div className="flex flex-col gap-5 rounded-lg border border-border bg-card p-6">
            <label className={labelClass}>
              <span className={labelTextClass}>Title</span>
              <input className={inputClass} onChange={(e) => setTitle(e.target.value)} type="text" value={title} />
            </label>

            <div className={labelClass}>
              <span className={labelTextClass}>Featured Image</span>
              {photoURL ? (
                <div className="flex flex-col gap-3 p-3 rounded-lg border border-border bg-muted/30">
                  <div className="flex items-start gap-3">
                    <img alt={photoAlt || "Selected featured"} className="w-24 h-16 object-cover rounded-md flex-shrink-0" src={photoURL} referrerPolicy="no-referrer" />
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
                        onClick={() => {
                          setPhotoURL("")
                          // The description belonged to the image being removed.
                          setPhotoAlt("")
                        }}
                        type="button"
                      >
                        <X className="w-3 h-3" />
                        Remove
                      </button>
                    </div>
                  </div>
                  {/* Editable here rather than only in the library, because this
                      is the description that publishes and the one an author is
                      thinking about while writing. It saves with the article. */}
                  <div className="flex flex-col gap-1">
                    <label className="flex flex-col gap-1">
                      <span className="text-xs font-medium text-muted-foreground">Alt text</span>
                      <input
                        aria-describedby="featured-image-alt-hint"
                        className={inputClass}
                        onChange={(e) => setPhotoAlt(e.target.value)}
                        placeholder="Describe the image for readers who can't see it"
                        type="text"
                        value={photoAlt}
                      />
                    </label>
                    {/* Outside the label on purpose: inside, it would be read as
                        part of the field's name rather than as its description. */}
                    <span
                      className={`text-xs ${photoAlt.trim() ? "text-muted-foreground" : "text-destructive"}`}
                      id="featured-image-alt-hint"
                    >
                      {photoAlt.trim()
                        ? "Describes this image on this article only."
                        : "No alt text. Screen readers announce nothing for this image."}
                    </span>
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

          <aside className="flex flex-col gap-6">
            <div className="flex flex-col gap-5 rounded-lg border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">Publish</h2>

            {/* The buttons under the field sit outside the label: inside it they
                would be read out as part of the field's own name. */}
            <div className={labelClass}>
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
                  <input
                    className={`${inputClass} bg-muted/50 cursor-default ${pendingSlug ? "text-foreground" : "text-muted-foreground"}`}
                    readOnly
                    type="text"
                    value={pendingSlug ?? slug}
                  />
                )}
              </label>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <button
                  className="inline-flex w-fit items-center gap-1.5 text-xs font-medium text-primary hover:underline disabled:opacity-50 disabled:cursor-not-allowed disabled:no-underline"
                  disabled={slugRegenerating || !title.trim()}
                  onClick={() => void regenerateSlug()}
                  title="Rebuild the slug from the current title"
                  type="button"
                >
                  <RefreshCw className={`h-3.5 w-3.5 ${slugRegenerating ? "animate-spin" : ""}`} />
                  {slugRegenerating ? "Checking..." : "Regenerate from title"}
                </button>
                {/* Available on drafts too: the newsletter and social posts are
                    built ahead of publication and need the link before the article
                    is live. A new article has no slug until it is saved, so the
                    URL would be a guess; offer it only once one exists. */}
                {effectiveSlug ? (
                  <button
                    className="inline-flex w-fit items-center gap-1.5 text-xs font-medium text-primary hover:underline"
                    onClick={() => void copyArticleLink()}
                    title={articleUrl(effectiveSlug)}
                    type="button"
                  >
                    {linkCopied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                    {linkCopied ? "Link copied" : "Copy article link"}
                  </button>
                ) : (
                  <span className="text-[11px] text-muted-foreground">
                    Save once to get a shareable link.
                  </span>
                )}
              </div>
              {pendingSlug ? (
                <span className="text-[11px] text-muted-foreground">
                  Saving moves the article to this slug. Anything already linking to{" "}
                  <span className="font-medium text-foreground">/article/{slug}</span> will 404.{" "}
                  <button
                    className="font-medium text-primary hover:underline"
                    onClick={() => setPendingSlug(null)}
                    type="button"
                  >
                    Keep the current slug
                  </button>
                </span>
              ) : null}
            </div>

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
              <span className={labelTextClass}>Authors</span>
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
                    {visibleAuthors.map((author) => {
                      const bylinePosition = selectedAuthorIds.indexOf(author.id)
                      return (
                        <label key={author.id} className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted/50 cursor-pointer">
                          <input
                            checked={bylinePosition !== -1}
                            className="h-4 w-4 rounded border-border"
                            onChange={() => toggleAuthor(author.id)}
                            type="checkbox"
                          />
                          <span className="text-foreground">{author.display_name}</span>
                          {/* Only worth numbering once there is an order to convey. */}
                          {selectedAuthorIds.length > 1 && bylinePosition !== -1 ? (
                            <span className="ml-auto text-[11px] text-muted-foreground">{bylinePosition + 1}</span>
                          ) : null}
                        </label>
                      )
                    })}
                  </div>
                )}
              </div>
              {!authorsLoading ? (
                <span className="text-[11px] text-muted-foreground">
                  {selectedAuthorIds.length === 0
                    ? "No authors selected."
                    : `Byline: ${bylinePreview}`}
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
              <span className="flex flex-col gap-0.5">
                <span className="font-medium text-foreground">Breaking news</span>
                <span className="text-[11px] text-muted-foreground">
                  Adds this headline to the scrolling homepage banner once the article publishes. Up to three run at
                  once, newest first.
                </span>
              </span>
            </label>

            <label className="flex items-start gap-3 rounded-lg border border-border bg-background px-3 py-3 text-sm">
              <input
                checked={isFeatured}
                className="mt-0.5 h-4 w-4 rounded border-border"
                onChange={(e) => setIsFeatured(e.target.checked)}
                type="checkbox"
              />
              <span className="flex flex-col gap-0.5">
                <span className="font-medium text-foreground">Featured article</span>
                <span className="text-[11px] text-muted-foreground">
                  Pins this story to the top of the homepage. Up to three can be pinned; the newest leads, and pinning
                  this one leaves the others up.
                </span>
              </span>
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
              <span className={labelTextClass}>SEO Tags</span>
              <div className="flex flex-wrap items-center gap-1.5 w-full px-2 py-1.5 rounded-lg border border-border bg-background focus-within:ring-2 focus-within:ring-primary/40 focus-within:border-primary transition cursor-text">
                {seoTags.map((tag) => (
                  <span
                    className="inline-flex items-center gap-1 pl-2.5 pr-1 py-0.5 rounded-full bg-muted text-xs font-medium text-foreground"
                    key={tag.toLowerCase()}
                  >
                    {tag}
                    <button
                      aria-label={`Remove tag ${tag}`}
                      className="rounded-full p-0.5 text-muted-foreground hover:text-foreground hover:bg-border transition-colors cursor-pointer"
                      onClick={() => setSeoTags((current) => current.filter((item) => item !== tag))}
                      type="button"
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ))}
                <input
                  className="flex-1 min-w-[8rem] bg-transparent px-1 py-0.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none"
                  onBlur={commitSeoTagDraft}
                  onChange={(e) => setSeoTagDraft(e.target.value)}
                  onKeyDown={handleSeoTagKeyDown}
                  placeholder={seoTags.length > 0 ? "Add another tag" : "Type a tag, press Enter"}
                  type="text"
                  value={seoTagDraft}
                />
              </div>
              {tagSuggestions.length === 0 && seoTagDraft.trim() ? (
                // Worth saying out loud: nothing in the archive matches, so
                // this tag is a new one. That is allowed, but an editor who
                // meant to reuse an existing tag wants to know now, not after
                // they have coined a near-duplicate of it.
                <p className="pt-1.5 text-[11px] text-muted-foreground">
                  No existing tag matches. Press Enter to create it.
                </p>
              ) : null}
              {tagSuggestions.length > 0 ? (
                <div className="flex flex-wrap items-center gap-1.5 pt-1.5">
                  <span className="text-[11px] text-muted-foreground">
                    {seoTagDraft.trim() ? "Matching tags" : "Frequently used"}
                  </span>
                  {tagSuggestions.map((tag) => (
                    <button
                      aria-label={`Add tag ${tag}`}
                      className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full border border-dashed border-border text-xs font-medium text-muted-foreground hover:text-foreground hover:border-primary hover:bg-muted transition-colors cursor-pointer"
                      key={tag.toLowerCase()}
                      // Keep the caret in the tag box: without this the input's
                      // blur fires first and commits the half-typed draft, so
                      // clicking "drexel" after typing "dre" would add both.
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => addSuggestedSeoTag(tag)}
                      title={`Add the tag "${tag}"`}
                      type="button"
                    >
                      <Plus className="h-3 w-3" />
                      {tag}
                    </button>
                  ))}
                </div>
              ) : null}
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

            <label className={labelClass}>
              <span className={labelTextClass}>Canonical URL</span>
              <input
                className={inputClass}
                onChange={(e) => setCanonicalUrl(e.target.value)}
                placeholder="Defaults to this article's own URL"
                type="url"
                value={canonicalUrl}
              />
              <span className="text-[11px] text-muted-foreground">
                {canonicalUrl.trim() && !isValidCanonicalUrl(canonicalUrl)
                  ? "Must be an absolute http(s) URL, e.g. https://example.com/story."
                  : "Set this only for reprints, to credit the original publisher."}
              </span>
            </label>

            <label className="flex items-start gap-2.5 cursor-pointer">
              <input
                checked={noIndex}
                className="mt-0.5 h-4 w-4 rounded border-border accent-primary cursor-pointer"
                onChange={(e) => setNoIndex(e.target.checked)}
                type="checkbox"
              />
              <span className="flex flex-col gap-0.5">
                <span className={labelTextClass}>Hide from search engines</span>
                <span className="text-[11px] text-muted-foreground">
                  Adds noindex and drops the article from the sitemap. The article stays live.
                </span>
              </span>
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
                className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={isSaving}
                onClick={() => void saveArticle(publishTiming)}
                type="button"
              >
                <Save className="w-4 h-4" aria-hidden="true" />
                {isSaving ? publishSavingLabel : publishActionLabel}
              </button>
              {timingChangePending ? (
                <p className="text-[11px] text-muted-foreground">
                  Publish timing is not autosaved — press {publishActionLabel} to apply it.
                </p>
              ) : null}
            </div>
            </div>

            <Suspense
              fallback={(
                <div className="rounded-lg border border-border bg-card p-6 text-xs text-muted-foreground">
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
                permalink={articleUrl(isNew ? slugInput : slug)}
              />
            </Suspense>
          </aside>
        </div>
      )}

      {/* Shares the library picker with the body editor and settings, so the
          featured image gets the same upload, search and alt-text handling. */}
      {imagePickerOpen && (
        <MediaPicker
          initialUrl={photoURL}
          onClose={() => setImagePickerOpen(false)}
          onSelect={(item) => {
            // Adopt the library's description, so an image described once
            // arrives already described. Only on an actual change of image: the
            // alt belongs to whichever photo is in the slot, so re-picking the
            // same one must not overwrite a description written here.
            if (item.url !== photoURL) {
              setPhotoAlt(item.alt_text ?? "")
            }
            setPhotoURL(item.url)
            setImagePickerOpen(false)
          }}
          onUseUrl={(url) => {
            setPhotoURL(url)
            setImagePickerOpen(false)
          }}
          title="Featured image"
        />
      )}
    </div>
  )
}

export default EditArticleView
