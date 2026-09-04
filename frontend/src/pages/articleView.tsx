import { useCallback, useEffect, useMemo, useState } from "react"
import { ExternalLink, Search, Pencil, Plus, Trash2, X, ChevronFirst, ChevronLast, ChevronLeft, ChevronRight, Undo2, Copy, Check } from "lucide-react"
import { Link, useNavigate } from "react-router-dom"
import { articleUrl } from "../auth/urls"
import { useApiFetch } from "../hooks/useApiFetch"
import { copyText } from "../lib/clipboard"
import { articleStatusChipClass } from "../lib/articleStatus"

type ArticleStatus = "Published" | "Scheduled" | "Draft" | "Archived"

type ArticleItem = {
  id: string
  title: string
  authors: string
  status: ArticleStatus
  date: string
  slug?: string
  featuredImage?: string
  breakingNews: boolean
  isFeatured: boolean
}

type ApiArticle = {
  id: number
  title: string
  slug: string
  status: string
  published_date?: string
  creation_date?: string
  featured_image?: string
  breaking_news?: boolean
  is_featured?: boolean
  authors?: Array<{
    name?: string
  }>
}

type ApiArticleResponse = {
  articles?: ApiArticle[]
  pagination?: {
    has_more?: boolean
    hasMore?: boolean
    total_count?: number
    totalCount?: number
  }
}

type ApiAuthor = {
  id: number
  slug: string
  display_name: string
}

type ApiTaxonomyItem = {
  id: number
  type: string
  slug: string
  canonical_title: string
  // Only subsections carry one; it names the section or subsection above them.
  parent_slug?: string | null
}

const PAGE_SIZE_OPTIONS = [25, 50, 100, 200]
const DEFAULT_PAGE_SIZE = 25
const AUTHORS_PAGE_SIZE = 200

const isFutureDate = (value?: string) => {
  if (!value) return false
  const timestamp = new Date(value).getTime()
  return !Number.isNaN(timestamp) && timestamp > Date.now()
}

const mapApiStatus = (status: string, activeTab: "all" | "trash", publishedDate?: string): ArticleStatus => {
  if (activeTab === "trash") return "Archived"
  if (status.toLowerCase() === "scheduled") return "Scheduled"
  if (status.toLowerCase() !== "published") return "Draft"
  return isFutureDate(publishedDate) ? "Scheduled" : "Published"
}

const formatArticleDate = (publishedDate?: string) => {
  if (!publishedDate) return "-"
  const parsed = new Date(publishedDate)
  if (Number.isNaN(parsed.getTime())) return "-"

  return parsed.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  })
}

type ArticleViewProps = {
  pageTitle?: string
  fixedType?: string
  excludeType?: string
}

// Mirrors the status values the articles endpoint accepts for editors; "all"
// means send no status filter at all.
type PublishedFilter = "all" | "published" | "draft" | "scheduled"

type ArticleViewUIState = {
  searchQuery?: string
  activeTab?: "all" | "trash"
  authorQuery?: string
  sectionFilterSlug?: string
  subsectionFilterSlug?: string
  publishedFilter?: PublishedFilter
  // Each narrows to the flagged articles when on and does not filter at all
  // when off, so neither ever asks for the unflagged half. The endpoint still
  // accepts breaking=false/featured=false; nothing here sends it.
  breakingOnly?: boolean
  featuredOnly?: boolean
  dateSortDirection?: "asc" | "desc"
  pageSize?: number
}

type ArticleResultsCacheEntry = {
  items: ArticleItem[]
  totalArticleCount: number
}

const readSessionJSON = <T,>(key: string, fallback: T): T => {
  if (typeof window === "undefined") return fallback
  const raw = window.sessionStorage.getItem(key)
  if (!raw) return fallback
  try {
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

const writeSessionJSON = (key: string, value: unknown) => {
  if (typeof window === "undefined") return
  window.sessionStorage.setItem(key, JSON.stringify(value))
}

// A switch rather than a checkbox because these read as "on/off", not as items
// ticked off a list. Hand-rolled: there is no switch in components/ui and no
// @radix-ui/react-switch in the tree, and pulling one in for two toggles would
// be more code than this.
//
// role="switch" with aria-checked is what makes it announce as a switch; the
// track and knob are presentational, so they are spans inside the button rather
// than focusable elements of their own.
function FilterSwitch({
  checked,
  label,
  onChange,
}: {
  checked: boolean
  label: string
  onChange: (next: boolean) => void
}) {
  return (
    <button
      aria-checked={checked}
      className="group flex items-center gap-2 text-sm text-foreground cursor-pointer rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
      onClick={() => onChange(!checked)}
      role="switch"
      type="button"
    >
      <span
        aria-hidden="true"
        className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border transition-colors ${
          checked ? "bg-primary border-primary" : "bg-muted border-border group-hover:border-primary"
        }`}
      >
        <span
          className={`inline-block h-3.5 w-3.5 rounded-full bg-background shadow-sm transition-transform ${
            checked ? "translate-x-[17px]" : "translate-x-[3px]"
          }`}
        />
      </span>
      {label}
    </button>
  )
}

function ArticleView({ pageTitle = "Articles", fixedType, excludeType }: ArticleViewProps) {
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const storageKeyBase = `articleView:${fixedType ?? "all"}:${excludeType ?? "none"}`
  const uiStateKey = `${storageKeyBase}:ui`
  const resultsCacheKey = `${storageKeyBase}:results`
  const authorsCacheKey = `${storageKeyBase}:authors`
  const sectionsCacheKey = "articleView:sections"
  const subsectionsCacheKey = "articleView:subsections"
  const loadUIState = () => readSessionJSON<ArticleViewUIState>(uiStateKey, {})

  const [searchQuery, setSearchQuery] = useState(() => loadUIState().searchQuery ?? "")
  const [activeTab, setActiveTab] = useState<"all" | "trash">(() => loadUIState().activeTab ?? "all")
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(() => loadUIState().pageSize ?? DEFAULT_PAGE_SIZE)
  const [articles, setArticles] = useState<ArticleItem[]>([])
  const [totalArticleCount, setTotalArticleCount] = useState(0)
  const [trashCount, setTrashCount] = useState(0)
  const [authors, setAuthors] = useState<ApiAuthor[]>([])
  const [authorQuery, setAuthorQuery] = useState(() => loadUIState().authorQuery ?? "")
  const [sections, setSections] = useState<ApiTaxonomyItem[]>([])
  const [sectionFilterSlug, setSectionFilterSlug] = useState(() => loadUIState().sectionFilterSlug ?? "")
  const [subsections, setSubsections] = useState<ApiTaxonomyItem[]>([])
  const [subsectionFilterSlug, setSubsectionFilterSlug] = useState(() => loadUIState().subsectionFilterSlug ?? "")
  const [publishedFilter, setPublishedFilter] = useState<PublishedFilter>(() => loadUIState().publishedFilter ?? "all")
  const [breakingOnly, setBreakingOnly] = useState(() => loadUIState().breakingOnly ?? false)
  const [featuredOnly, setFeaturedOnly] = useState(() => loadUIState().featuredOnly ?? false)
  const [dateSortDirection, setDateSortDirection] = useState<"asc" | "desc">(() => loadUIState().dateSortDirection ?? "desc")
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [deletingArticleId, setDeletingArticleId] = useState<string | null>(null)
  const [copiedArticleId, setCopiedArticleId] = useState<string | null>(null)

  useEffect(() => {
    writeSessionJSON(uiStateKey, {
      searchQuery,
      activeTab,
      authorQuery,
      sectionFilterSlug,
      subsectionFilterSlug,
      publishedFilter,
      breakingOnly,
      featuredOnly,
      dateSortDirection,
      pageSize,
    } satisfies ArticleViewUIState)
  }, [
    activeTab,
    authorQuery,
    breakingOnly,
    dateSortDirection,
    featuredOnly,
    pageSize,
    publishedFilter,
    searchQuery,
    sectionFilterSlug,
    subsectionFilterSlug,
    uiStateKey,
  ])

  const selectedAuthorSlug = useMemo(() => {
    const normalizedValue = authorQuery.trim().toLowerCase()
    if (!normalizedValue) return ""

    const matchedAuthor = authors.find((author) => {
      const displayName = author.display_name.trim().toLowerCase()
      const slug = author.slug.trim().toLowerCase()
      return displayName === normalizedValue || slug === normalizedValue
    })
    return matchedAuthor?.slug ?? ""
  }, [authorQuery, authors])

  useEffect(() => {
    let cancelled = false

    const fetchTaxonomy = async (
      type: "section" | "subsection",
      cacheKey: string,
      apply: (items: ApiTaxonomyItem[]) => void,
    ) => {
      const cached = readSessionJSON<ApiTaxonomyItem[] | null>(cacheKey, null)
      if (cached) {
        apply(cached)
        return
      }

      try {
        const response = await apiFetch(`/v1/taxonomy?type=${type}`)
        if (!response.ok) {
          throw new Error(`Taxonomy request failed (${response.status})`)
        }

        const payload = (await response.json()) as ApiTaxonomyItem[]
        const nextItems = (Array.isArray(payload) ? payload : [])
          .filter((item) => item.type === type)
          .sort((left, right) => left.id - right.id)

        if (!cancelled) {
          apply(nextItems)
          writeSessionJSON(cacheKey, nextItems)
        }
      } catch {
        if (!cancelled) {
          apply([])
        }
      }
    }

    void fetchTaxonomy("section", sectionsCacheKey, setSections)
    void fetchTaxonomy("subsection", subsectionsCacheKey, setSubsections)
    return () => {
      cancelled = true
    }
  }, [apiFetch, sectionsCacheKey, subsectionsCacheKey])

  useEffect(() => {
    if (!sectionFilterSlug || sections.length === 0) return
    if (sections.some((section) => section.slug === sectionFilterSlug)) return
    setSectionFilterSlug("")
  }, [sectionFilterSlug, sections])

  // childrenOf answers with the subsections parented directly by a slug, which
  // is what one row of the drill-down strip lists.
  const childrenOf = useCallback(
    (parentSlug: string) => subsections.filter((subsection) => subsection.parent_slug === parentSlug),
    [subsections],
  )

  // subsectionTrail is the chain from the selected section down to the selected
  // subsection, so the strip can show a row of siblings at every level. It comes
  // back empty when the selection no longer resolves, which is the signal the
  // effect below uses to drop a stale filter.
  const subsectionTrail = useMemo(() => {
    if (!subsectionFilterSlug || !sectionFilterSlug) return []

    const bySlug = new Map(subsections.map((subsection) => [subsection.slug, subsection]))
    const trail: ApiTaxonomyItem[] = []
    let current = bySlug.get(subsectionFilterSlug)
    while (current) {
      trail.unshift(current)
      const parent = current.parent_slug ?? ""
      if (parent === sectionFilterSlug) return trail
      // A cycle would be a server-side bug, but the walk must still terminate.
      if (trail.some((item) => item.slug === parent)) return []
      current = bySlug.get(parent)
    }
    return []
  }, [sectionFilterSlug, subsectionFilterSlug, subsections])

  useEffect(() => {
    if (!subsectionFilterSlug) return
    if (!sectionFilterSlug) {
      // A subsection filter only means anything under its section.
      setSubsectionFilterSlug("")
      return
    }
    if (subsections.length === 0) return
    if (subsectionTrail.length > 0) return
    setSubsectionFilterSlug("")
  }, [sectionFilterSlug, subsectionFilterSlug, subsectionTrail, subsections])

  useEffect(() => {
    let cancelled = false

    const fetchAuthors = async () => {
      const cachedAuthors = readSessionJSON<ApiAuthor[] | null>(authorsCacheKey, null)
      if (cachedAuthors) {
        setAuthors(cachedAuthors)
        return
      }

      try {
        const allAuthors: ApiAuthor[] = []
        let offset = 0
        let keepFetching = true

        while (keepFetching) {
          const response = await apiFetch(`/v1/authors?limit=${AUTHORS_PAGE_SIZE}&offset=${offset}&sort_by=display_name&sort_direction=asc`)
          if (!response.ok) {
            throw new Error(`Authors request failed (${response.status})`)
          }

          const payload = (await response.json()) as ApiAuthor[]
          allAuthors.push(...payload)
          keepFetching = payload.length === AUTHORS_PAGE_SIZE
          offset += AUTHORS_PAGE_SIZE
        }

        if (!cancelled) {
          setAuthors(allAuthors)
          writeSessionJSON(authorsCacheKey, allAuthors)
        }
      } catch {
        if (!cancelled) {
          setAuthors([])
        }
      }
    }

    void fetchAuthors()
    return () => {
      cancelled = true
    }
  }, [apiFetch, authorsCacheKey])

  useEffect(() => {
    let cancelled = false

    const fetchTrashCount = async () => {
      try {
        const params = new URLSearchParams({
          archived: "1",
          limit: "1",
        })
        if (fixedType) {
          params.set("type", fixedType)
        }
        if (excludeType) {
          params.set("exclude_type", excludeType)
        }
        const response = await apiFetch(`/v1/articles?${params.toString()}`)
        if (!response.ok) return
        const payload = (await response.json()) as ApiArticleResponse
        const count = payload.pagination?.total_count ?? payload.pagination?.totalCount ?? 0
        if (!cancelled) {
          setTrashCount(count)
        }
      } catch {
        if (!cancelled) {
          setTrashCount(0)
        }
      }
    }

    const fetchArticles = async () => {
      setIsLoading(true)
      setError(null)

      let paintedFromCache = false
      try {
        const params = new URLSearchParams({
          limit: String(pageSize),
          page: String(page + 1),
          sort_by: "published_date",
          sort_direction: dateSortDirection,
        })
        if (activeTab === "trash") {
          params.set("archived", "1")
        }
        if (selectedAuthorSlug) {
          params.set("author_slug", selectedAuthorSlug)
        } else if (authorQuery.trim()) {
          params.set("author", authorQuery.trim())
        }
        if (activeTab !== "trash" && publishedFilter !== "all") {
          params.set("status", publishedFilter)
        }
        if (searchQuery.trim()) {
          params.set("title", searchQuery.trim())
        }
        if (sectionFilterSlug) {
          params.set("section_slug", sectionFilterSlug)
        }
        if (subsectionFilterSlug) {
          params.set("subsection_slug", subsectionFilterSlug)
        }
        if (breakingOnly) {
          params.set("breaking", "true")
        }
        if (featuredOnly) {
          params.set("featured", "true")
        }
        if (fixedType) {
          params.set("type", fixedType)
        }
        if (excludeType) {
          params.set("exclude_type", excludeType)
        }

        const queryKey = params.toString()
        const cache = readSessionJSON<Record<string, ArticleResultsCacheEntry>>(resultsCacheKey, {})
        const cachedEntry = cache[queryKey]
        const shouldUseCache = activeTab !== "trash"
        // Stale-while-revalidate: paint cached results immediately (no spinner),
        // then always refetch in the background so the list can't stay stale
        // after a create/edit/delete.
        paintedFromCache = Boolean(shouldUseCache && cachedEntry)
        if (paintedFromCache && cachedEntry) {
          if (!cancelled) {
            setArticles(cachedEntry.items)
            setTotalArticleCount(cachedEntry.totalArticleCount)
            setIsLoading(false)
          }
        }

        const response = await apiFetch(`/v1/articles?${params.toString()}`)
        if (!response.ok) {
          throw new Error(`Could not load articles (${response.status})`)
        }

        const payload = (await response.json()) as ApiArticleResponse
        const items = (payload.articles ?? []).map((item) => ({
          id: String(item.id),
          title: item.title,
          authors: (item.authors ?? [])
            .map((author) => (author.name ?? "").trim())
            .filter((name) => name.length > 0)
            .join(", "),
          status: mapApiStatus(item.status, activeTab, item.published_date),
          // Drafts have no published_date, so fall back to when the row was created.
          date: formatArticleDate(item.published_date ?? item.creation_date),
          slug: item.slug,
          featuredImage: item.featured_image,
          breakingNews: Boolean(item.breaking_news),
          isFeatured: Boolean(item.is_featured),
        }))

        if (!cancelled) {
          setArticles(items)
          const apiTotalCount = payload.pagination?.total_count ?? payload.pagination?.totalCount
          const fallbackTotalCount = (page * pageSize) + items.length + ((payload.pagination?.has_more ?? payload.pagination?.hasMore) ? 1 : 0)
          const computedTotal = typeof apiTotalCount === "number" ? apiTotalCount : fallbackTotalCount
          setTotalArticleCount(computedTotal)
          writeSessionJSON(resultsCacheKey, {
            ...cache,
            [queryKey]: {
              items,
              totalArticleCount: computedTotal,
            },
          } satisfies Record<string, ArticleResultsCacheEntry>)
        }
      } catch (err) {
        if (!cancelled && !paintedFromCache) {
          // Keep the already-painted cached results if a background refresh fails.
          const message = err instanceof Error ? err.message : "Unable to load articles."
          setError(message)
          setArticles([])
          setTotalArticleCount(0)
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false)
        }
      }
    }

    void fetchTrashCount()
    void fetchArticles()

    return () => {
      cancelled = true
    }
  }, [activeTab, apiFetch, authorQuery, breakingOnly, dateSortDirection, excludeType, featuredOnly, fixedType, page, pageSize, publishedFilter, resultsCacheKey, searchQuery, sectionFilterSlug, selectedAuthorSlug, subsectionFilterSlug])

  const onChangeTab = (tab: "all" | "trash") => {
    setActiveTab(tab)
    setPage(0)
  }

  const onSearch = (value: string) => {
    setSearchQuery(value)
    setPage(0)
  }

  useEffect(() => {
    setPage(0)
  }, [authorQuery, publishedFilter, breakingOnly, featuredOnly, dateSortDirection, searchQuery, sectionFilterSlug, subsectionFilterSlug, pageSize])

  const effectiveTotalCount = Math.max(totalArticleCount, (page * pageSize) + articles.length)
  const totalPages = Math.max(1, Math.ceil(effectiveTotalCount / pageSize))
  const listLabel = pageTitle.toLowerCase()
  const editPathForArticle = (item: ArticleItem) =>
    fixedType === "developing-stories"
      ? `/developing-stories/${encodeURIComponent(item.id)}/${encodeURIComponent(item.slug ?? "")}/edit`
      : `/articles/${encodeURIComponent(item.id)}/${encodeURIComponent(item.slug ?? "")}/edit`

  // subsectionRows walks the section, then each subsection on the trail, and
  // keeps the levels that actually have children to offer.
  const subsectionRows = useMemo(() => {
    if (!sectionFilterSlug) return []

    const parents = [sectionFilterSlug, ...subsectionTrail.map((item) => item.slug)]
    return parents.flatMap((parentSlug, depth) => {
      const items = childrenOf(parentSlug)
      if (items.length === 0) return []
      const parentIsSection = depth === 0
      return [{
        parentSlug,
        parentIsSection,
        items,
        // Deselecting a nested row means falling back to its parent subsection,
        // not to the whole section.
        allLabel: parentIsSection ? "All subsections" : `All of ${subsectionTrail[depth - 1].canonical_title}`,
        selectedSlug: subsectionTrail[depth]?.slug ?? "",
      }]
    })
  }, [childrenOf, sectionFilterSlug, subsectionTrail])

  const filterTagClass = (active: boolean) =>
    `px-3 py-1 rounded-full text-xs font-medium transition-colors cursor-pointer border ${
      active
        ? "bg-primary text-primary-foreground border-primary"
        : "bg-background text-muted-foreground border-border hover:border-primary hover:text-primary"
    }`

  const tabClass = (active: boolean) =>
    `px-4 py-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5 ${
      active
        ? "border-primary text-primary"
        : "border-transparent text-muted-foreground hover:text-foreground hover:border-border"
    }`

  const clearArticleViewCache = () => {
    if (typeof window === "undefined") return
    const keysToDelete: string[] = []
    for (let i = 0; i < window.sessionStorage.length; i += 1) {
      const key = window.sessionStorage.key(i)
      if (key?.startsWith("articleView:")) {
        keysToDelete.push(key)
      }
    }
    for (const key of keysToDelete) {
      window.sessionStorage.removeItem(key)
    }
  }

  const copyArticleLink = async (item: ArticleItem) => {
    if (!item.slug) return
    if (await copyText(articleUrl(item.slug))) {
      setCopiedArticleId(item.id)
      setTimeout(() => setCopiedArticleId((current) => (current === item.id ? null : current)), 1500)
      return
    }
    setCopiedArticleId(null)
    setDeleteError("Could not copy the link. Your browser blocked clipboard access.")
  }

  const deleteArticle = async (item: ArticleItem) => {
    if (!item.slug || deletingArticleId) return
    const shouldDelete = window.confirm(`Move "${item.title}" to trash?`)
    if (!shouldDelete) return

    setDeleteError(null)
    setDeletingArticleId(item.id)
    try {
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(item.slug)}?id=${encodeURIComponent(item.id)}`, {
        method: "DELETE",
      })
      if (!response.ok) {
        throw new Error(`Delete failed (${response.status})`)
      }

      setArticles((prev) => prev.filter((article) => article.id !== item.id))
      setTotalArticleCount((prev) => Math.max(0, prev - 1))
      setTrashCount((prev) => (activeTab === "all" ? prev + 1 : prev))
      clearArticleViewCache()
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to delete article."
      setDeleteError(message)
    } finally {
      setDeletingArticleId(null)
    }
  }

  const restoreArticle = async (item: ArticleItem) => {
    if (!item.slug || deletingArticleId) return
    const shouldRestore = window.confirm(`Restore "${item.title}"?`)
    if (!shouldRestore) return

    setDeleteError(null)
    setDeletingArticleId(item.id)
    try {
      const response = await apiFetch(`/v1/articles/${encodeURIComponent(item.slug)}/restore?id=${encodeURIComponent(item.id)}`, {
        method: "PATCH",
      })
      if (!response.ok) {
        throw new Error(`Restore failed (${response.status})`)
      }

      setArticles((prev) => prev.filter((article) => article.id !== item.id))
      setTotalArticleCount((prev) => Math.max(0, prev - 1))
      setTrashCount((prev) => Math.max(0, prev - 1))
      clearArticleViewCache()
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to restore article."
      setDeleteError(message)
    } finally {
      setDeletingArticleId(null)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-foreground">{pageTitle}</h1>
        <button
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          onClick={() => navigate(fixedType === "developing-stories" ? "/developing-stories/new" : "/articles/new")}
          type="button"
        >
          <Plus className="w-4 h-4" aria-hidden="true" />
          New article
        </button>
      </div>

      {/* Search */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          aria-label={`Search ${listLabel}`}
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          onChange={(e) => onSearch(e.target.value)}
          placeholder={`Search ${listLabel}`}
          type="search"
          value={searchQuery}
        />
      </div>

      {sections.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Sections</span>
          <div className="flex flex-wrap gap-1.5">
            <button
              aria-pressed={sectionFilterSlug === ""}
              className={filterTagClass(sectionFilterSlug === "")}
              onClick={() => setSectionFilterSlug("")}
              type="button"
            >
              All sections
            </button>
            {sections.map((section) => (
              <button
                aria-pressed={sectionFilterSlug === section.slug}
                className={filterTagClass(sectionFilterSlug === section.slug)}
                key={section.id}
                onClick={() => {
                  setSectionFilterSlug(section.slug)
                  setSubsectionFilterSlug("")
                }}
                type="button"
              >
                {section.canonical_title}
              </button>
            ))}
          </div>
          {/* One row per level: the selected section's subsections, then the
              selected subsection's own, for as deep as the tree goes. */}
          {subsectionRows.map((row) => (
            <div className="flex flex-wrap gap-1.5 pl-3 border-l-2 border-border" key={row.parentSlug}>
              <button
                aria-pressed={row.selectedSlug === ""}
                className={filterTagClass(row.selectedSlug === "")}
                onClick={() => setSubsectionFilterSlug(row.parentIsSection ? "" : row.parentSlug)}
                type="button"
              >
                {row.allLabel}
              </button>
              {row.items.map((subsection) => (
                <button
                  aria-pressed={row.selectedSlug === subsection.slug}
                  className={filterTagClass(row.selectedSlug === subsection.slug)}
                  key={subsection.id}
                  onClick={() => setSubsectionFilterSlug(subsection.slug)}
                  type="button"
                >
                  {subsection.canonical_title}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}

      {/* Filters */}
      <div className="flex flex-wrap gap-6 items-start">
        {/* Author filter */}
        <div className="flex flex-col gap-1.5">
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide" htmlFor="article-author-filter">
            Author
          </label>
          <div className="relative flex items-center">
            <input
              className="pr-7 pl-3 py-1.5 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              id="article-author-filter"
              list="article-author-options"
              onChange={(e) => setAuthorQuery(e.target.value)}
              placeholder="All authors"
              type="text"
              value={authorQuery}
            />
            {authorQuery.trim() && (
              <button
                aria-label="Clear author filter"
                className="absolute right-2 text-muted-foreground hover:text-foreground"
                onClick={() => setAuthorQuery("")}
                title="Clear author"
                type="button"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
          <datalist id="article-author-options">
            {authors.map((author) => (
              <option key={author.id} value={author.display_name} />
            ))}
          </datalist>
        </div>

        {/* Date sort */}
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Date</span>
          <div className="flex gap-1.5">
            <button className={filterTagClass(dateSortDirection === "desc")} onClick={() => setDateSortDirection("desc")} type="button">
              Newest first
            </button>
            <button className={filterTagClass(dateSortDirection === "asc")} onClick={() => setDateSortDirection("asc")} type="button">
              Oldest first
            </button>
          </div>
        </div>

        {activeTab !== "trash" && (
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Status</span>
            <div className="flex gap-1.5">
              <button className={filterTagClass(publishedFilter === "all")} onClick={() => setPublishedFilter("all")} type="button">All</button>
              <button className={filterTagClass(publishedFilter === "published")} onClick={() => setPublishedFilter("published")} type="button">Published</button>
              <button className={filterTagClass(publishedFilter === "draft")} onClick={() => setPublishedFilter("draft")} type="button">Draft</button>
              <button className={filterTagClass(publishedFilter === "scheduled")} onClick={() => setPublishedFilter("scheduled")} type="button">Scheduled</button>
            </div>
          </div>
        )}

        {/* Breaking and Featured are the two flags an editor sets from the
            article form, and the two they need to audit: which stories are
            still flagged as breaking, and what is currently pinned. Each one
            narrows to its flag or does nothing, so it is a switch rather than a
            row of chips -- there is no third thing to pick. */}
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Flags</span>
          <div className="flex flex-wrap items-center gap-4 py-1">
            <FilterSwitch checked={breakingOnly} label="Breaking only" onChange={setBreakingOnly} />
            <FilterSwitch checked={featuredOnly} label="Featured only" onChange={setFeaturedOnly} />
          </div>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-border">
        <button aria-pressed={activeTab === "all"} className={tabClass(activeTab === "all")} onClick={() => onChangeTab("all")} type="button">
          All
        </button>
        <button aria-pressed={activeTab === "trash"} className={tabClass(activeTab === "trash")} onClick={() => onChangeTab("trash")} type="button">
          <Trash2 className="w-3.5 h-3.5" />
          Trash
          <span className="ml-1 px-1.5 py-0.5 rounded-full text-xs bg-muted text-muted-foreground">{trashCount}</span>
        </button>
      </div>

      {/* Table */}
      <div className="rounded-xl border border-border bg-card overflow-hidden">
        {deleteError ? (
          <p className="px-4 py-3 text-sm text-destructive bg-destructive/10 border-b border-destructive/20">
            {deleteError}
          </p>
        ) : null}
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              {/* min-w as well as w: see the thumbnail cell below. */}
              <th className="w-16 min-w-[4rem] px-3 py-3" scope="col" aria-label="Featured image" />
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Title</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Authors</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Status</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Date</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>
                  Loading articles...
                </td>
              </tr>
            ) : error ? (
              <tr>
                <td className="px-4 py-8 text-center text-destructive" colSpan={6}>
                  Could not load articles: {error}
                </td>
              </tr>
            ) : articles.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>
                  {searchQuery ? `No results for "${searchQuery}"` : `No ${activeTab === "trash" ? "trashed" : ""} ${listLabel} yet.`}
                </td>
              </tr>
            ) : (
              articles.map((item) => (
                <tr key={item.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-3 py-2 w-16">
                    {item.featuredImage ? (
                      // min-w, because w-12 alone does not survive a crowded
                      // table. Tailwind's preflight puts max-width:100% on every
                      // img, which makes this one's min-content width zero, and
                      // auto table layout then takes the space it needs from the
                      // only column that will yield. The result is a 4px-wide
                      // sliver of photo at full height: the second badge on a
                      // row was enough to trigger it.
                      <img
                        alt=""
                        className="w-12 min-w-[3rem] h-10 object-cover rounded-md bg-muted flex-shrink-0"
                        src={item.featuredImage}
                        // The image proxy blocks cross-origin referers (returns 403),
                        // so suppress the Referer header to let the thumbnail load.
                        referrerPolicy="no-referrer"
                        loading="lazy"
                      />
                    ) : (
                      <div className="w-12 h-10 rounded-md bg-muted flex items-center justify-center flex-shrink-0">
                        <svg className="w-4 h-4 text-muted-foreground/40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                        </svg>
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-3 font-medium text-foreground max-w-xs">
                    <div className="flex items-center gap-2 min-w-0">
                      {item.slug ? (
                        <Link
                          className="block min-w-0 truncate rounded-sm hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40"
                          title={item.title}
                          to={editPathForArticle(item)}
                        >
                          {item.title}
                        </Link>
                      ) : (
                        <span className="block min-w-0 truncate" title={item.title}>{item.title}</span>
                      )}
                      {item.breakingNews ? (
                        <span className="inline-flex shrink-0 items-center rounded-full bg-red-100 px-2 py-0.5 text-[11px] font-semibold uppercase text-red-700 dark:bg-red-950/40 dark:text-red-300">
                          Breaking
                        </span>
                      ) : null}
                      {item.isFeatured ? (
                        <span className="inline-flex shrink-0 items-center rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold uppercase text-amber-700 dark:bg-amber-950/40 dark:text-amber-300">
                          Featured
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{item.authors || "-"}</td>
                  <td className="px-4 py-3">
                    <span className={articleStatusChipClass(item.status)}>
                      {item.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground whitespace-nowrap">{item.date}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      {activeTab !== "trash" && item.status === "Published" && (
                        <a
                          className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium text-primary hover:bg-primary/10 transition-colors"
                          href={item.slug ? articleUrl(item.slug) : "#"}
                          rel="noreferrer"
                          target="_blank"
                        >
                          <ExternalLink className="w-3.5 h-3.5" />
                          View live
                        </a>
                      )}
                      {/* Unlike View Live this is offered at any status: the
                          link is wanted precisely while the piece is still a
                          draft or scheduled, to write around it. */}
                      {activeTab !== "trash" && (
                        <button
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                          disabled={!item.slug}
                          onClick={() => void copyArticleLink(item)}
                          title={item.slug ? "Copy article link" : "No link yet"}
                          type="button"
                        >
                          {copiedArticleId === item.id ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
                        </button>
                      )}
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                        disabled={!item.slug}
                        onClick={() => {
                          if (!item.slug) return
                          navigate(editPathForArticle(item))
                        }}
                        title={item.slug ? "Edit" : "Edit unavailable"}
                        type="button"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                        disabled={!item.slug || deletingArticleId === item.id}
                        onClick={() => void (activeTab === "trash" ? restoreArticle(item) : deleteArticle(item))}
                        title={activeTab === "trash" ? "Restore" : "Delete"}
                        type="button"
                      >
                        {activeTab === "trash" ? <Undo2 className="w-4 h-4" /> : <Trash2 className="w-4 h-4" />}
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <div className="flex items-center gap-3">
          <span>{articles.length} article{articles.length === 1 ? "" : "s"} shown</span>
          <label className="flex items-center gap-1.5">
            <span>Per page</span>
            <select
              className="rounded-lg border border-border bg-background px-2 py-1 text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary"
              value={pageSize}
              onChange={(e) => setPageSize(Number(e.target.value))}
            >
              {PAGE_SIZE_OPTIONS.map((size) => (
                <option key={size} value={size}>{size}</option>
              ))}
            </select>
          </label>
        </div>
        <div className="flex items-center gap-1">
          <button
            className="p-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={page === 0}
            onClick={() => setPage(0)}
            type="button"
          >
            <ChevronFirst className="w-4 h-4" />
          </button>
          <button
            className="p-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={page === 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            type="button"
          >
            <ChevronLeft className="w-4 h-4" />
          </button>
          <span className="px-3 py-1 font-medium text-foreground">
            {page + 1} / {totalPages}
          </span>
          <button
            className="p-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={(page + 1) * pageSize >= effectiveTotalCount}
            onClick={() => setPage((p) => p + 1)}
            type="button"
          >
            <ChevronRight className="w-4 h-4" />
          </button>
          <button
            className="p-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={(page + 1) * pageSize >= effectiveTotalCount}
            onClick={() => setPage(Math.max(0, totalPages - 1))}
            type="button"
          >
            <ChevronLast className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
  )
}

export default ArticleView
