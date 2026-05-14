import { useEffect, useState } from "react"
import { Search, Pencil, Plus, Trash2, ChevronFirst, ChevronLast, ChevronLeft, ChevronRight } from "lucide-react"
import { useNavigate } from "react-router-dom"

type ArticleStatus = "Published" | "Draft"

type ArticleItem = {
  id: string
  title: string
  status: ArticleStatus
  date: string
  slug?: string
  featuredImage?: string
}

type ApiArticle = {
  id: number
  title: string
  slug: string
  status: string
  published_date?: string
  featured_image?: string
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

const PAGE_SIZE = 20
const TYPE_FILTER = "developing-stories"

type DevelopingStoriesUIState = {
  searchQuery?: string
  activeTab?: "all" | "trash"
  publishedFilter?: "all" | "published" | "draft"
  dateSortDirection?: "asc" | "desc"
}

type DevelopingStoriesResultsCacheEntry = {
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

const mapApiStatus = (status: string): ArticleStatus => (status.toLowerCase() === "published" ? "Published" : "Draft")

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

function DevelopingStoriesView() {
  const navigate = useNavigate()
  const storageKeyBase = "developingStoriesView"
  const uiStateKey = `${storageKeyBase}:ui`
  const resultsCacheKey = `${storageKeyBase}:results`
  const loadUIState = () => readSessionJSON<DevelopingStoriesUIState>(uiStateKey, {})

  const [searchQuery, setSearchQuery] = useState(() => loadUIState().searchQuery ?? "")
  const [activeTab, setActiveTab] = useState<"all" | "trash">(() => loadUIState().activeTab ?? "all")
  const [page, setPage] = useState(0)
  const [articles, setArticles] = useState<ArticleItem[]>([])
  const [totalArticleCount, setTotalArticleCount] = useState(0)
  const [publishedFilter, setPublishedFilter] = useState<"all" | "published" | "draft">(() => loadUIState().publishedFilter ?? "all")
  const [dateSortDirection, setDateSortDirection] = useState<"asc" | "desc">(() => loadUIState().dateSortDirection ?? "desc")
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const trashedItems: ArticleItem[] = []

  useEffect(() => {
    writeSessionJSON(uiStateKey, {
      searchQuery,
      activeTab,
      publishedFilter,
      dateSortDirection,
    } satisfies DevelopingStoriesUIState)
  }, [activeTab, dateSortDirection, publishedFilter, searchQuery, uiStateKey])

  useEffect(() => {
    let cancelled = false

    const fetchArticles = async () => {
      setIsLoading(true)
      setError(null)

      try {
        const params = new URLSearchParams({
          limit: String(PAGE_SIZE),
          page: String(page + 1),
          sort_by: "published_date",
          sort_direction: dateSortDirection,
          type: TYPE_FILTER,
        })

        if (publishedFilter !== "all") {
          params.set("status", publishedFilter)
        }
        if (searchQuery.trim()) {
          params.set("title", searchQuery.trim())
        }

        const queryKey = params.toString()
        const cache = readSessionJSON<Record<string, DevelopingStoriesResultsCacheEntry>>(resultsCacheKey, {})
        const cachedEntry = cache[queryKey]
        if (cachedEntry) {
          if (!cancelled) {
            setArticles(cachedEntry.items)
            setTotalArticleCount(cachedEntry.totalArticleCount)
            setIsLoading(false)
          }
          return
        }

        const response = await fetch(`/v1/articles?${params.toString()}`)
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`)
        }

        const payload = (await response.json()) as ApiArticleResponse
        const items = (payload.articles ?? []).map((item) => ({
          id: String(item.id),
          title: item.title,
          status: mapApiStatus(item.status),
          date: formatArticleDate(item.published_date),
          slug: item.slug,
          featuredImage: item.featured_image,
        }))

        if (!cancelled) {
          setArticles(items)
          const apiTotalCount = payload.pagination?.total_count ?? payload.pagination?.totalCount
          const fallbackTotalCount = (page * PAGE_SIZE) + items.length + (Boolean(payload.pagination?.has_more ?? payload.pagination?.hasMore) ? 1 : 0)
          const computedTotal = typeof apiTotalCount === "number" ? apiTotalCount : fallbackTotalCount
          setTotalArticleCount(computedTotal)
          writeSessionJSON(resultsCacheKey, {
            ...cache,
            [queryKey]: {
              items,
              totalArticleCount: computedTotal,
            },
          } satisfies Record<string, DevelopingStoriesResultsCacheEntry>)
        }
      } catch (err) {
        if (!cancelled) {
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

    void fetchArticles()

    return () => {
      cancelled = true
    }
  }, [dateSortDirection, page, publishedFilter, resultsCacheKey, searchQuery])

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
  }, [publishedFilter, dateSortDirection, searchQuery])

  const effectiveTotalCount = Math.max(totalArticleCount, (page * PAGE_SIZE) + articles.length)
  const totalPages = Math.max(1, Math.ceil(effectiveTotalCount / PAGE_SIZE))

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

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-foreground">Developing Stories</h1>
        <button
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          type="button"
        >
          <Plus className="w-4 h-4" aria-hidden="true" />
          Add New
        </button>
      </div>

      {/* Search */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          aria-label="Search developing stories"
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          onChange={(e) => onSearch(e.target.value)}
          placeholder="Search developing stories..."
          type="search"
          value={searchQuery}
        />
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-6 items-start">
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

        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Status</span>
          <div className="flex gap-1.5">
            <button className={filterTagClass(publishedFilter === "all")} onClick={() => setPublishedFilter("all")} type="button">All</button>
            <button className={filterTagClass(publishedFilter === "published")} onClick={() => setPublishedFilter("published")} type="button">Published</button>
            <button className={filterTagClass(publishedFilter === "draft")} onClick={() => setPublishedFilter("draft")} type="button">Draft</button>
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
          <span className="ml-1 px-1.5 py-0.5 rounded-full text-xs bg-muted text-muted-foreground">{trashedItems.length}</span>
        </button>
      </div>

      {/* Table */}
      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="w-16 px-3 py-3" scope="col" aria-label="Featured image" />
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Title</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Status</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Date</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={5}>
                  Loading articles...
                </td>
              </tr>
            ) : error ? (
              <tr>
                <td className="px-4 py-8 text-center text-destructive" colSpan={5}>
                  Failed to load articles: {error}
                </td>
              </tr>
            ) : articles.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={5}>
                  {searchQuery ? `No results for "${searchQuery}"` : `No ${activeTab === "trash" ? "trashed" : ""} developing stories yet.`}
                </td>
              </tr>
            ) : (
              articles.map((item) => (
                <tr key={item.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-3 py-2 w-16">
                    {item.featuredImage ? (
                      <img
                        alt=""
                        className="w-12 h-10 object-cover rounded-md bg-muted flex-shrink-0"
                        src={item.featuredImage}
                      />
                    ) : (
                      <div className="w-12 h-10 rounded-md bg-muted flex items-center justify-center flex-shrink-0">
                        <svg className="w-4 h-4 text-muted-foreground/40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                        </svg>
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-3 font-medium text-foreground max-w-xs truncate">{item.title}</td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                      item.status === "Published"
                        ? "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400"
                        : "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400"
                    }`}>
                      {item.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground whitespace-nowrap">{item.date}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                        disabled={!item.slug}
                        onClick={() => {
                          if (!item.slug) return
                          navigate(`/developing-stories/${encodeURIComponent(item.slug)}/edit`)
                        }}
                        title={item.slug ? "Edit" : "Edit unavailable"}
                        type="button"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                        title="Delete"
                        type="button"
                      >
                        <Trash2 className="w-4 h-4" />
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
        <span>{articles.length} article{articles.length === 1 ? "" : "s"} shown</span>
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
            disabled={(page + 1) * PAGE_SIZE >= effectiveTotalCount}
            onClick={() => setPage((p) => p + 1)}
            type="button"
          >
            <ChevronRight className="w-4 h-4" />
          </button>
          <button
            className="p-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={(page + 1) * PAGE_SIZE >= effectiveTotalCount}
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

export default DevelopingStoriesView
