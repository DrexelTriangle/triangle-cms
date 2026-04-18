import { Pagination, buttonVariants } from "@cloudflare/kumo"
import { useEffect, useState } from "react"
import { MagnifyingGlassIcon, PencilIcon, PlusIcon, TrashIcon } from "@phosphor-icons/react"

type ArticleStatus = "Published" | "Draft"

type ArticleItem = {
  id: string
  title: string
  status: ArticleStatus
  date: string
  slug?: string
}

type ApiArticle = {
  id: number
  title: string
  slug: string
  status: string
  published_date?: string
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

  return (
    <div className="article-list-page">
      <div className="article-list-header">
        <div className="article-list-title-row">
          <h1 className="article-list-title">Developing Stories</h1>
        </div>
        <button className={`${buttonVariants()} article-add-new-button`} type="button">
          <PlusIcon aria-hidden="true" className="me-2 h-4 w-4" />
          Add New
        </button>
      </div>

      <div className="article-search-wrap">
        <MagnifyingGlassIcon className="article-search-icon" />
        <input
          aria-label="Search developing stories"
          className="article-search-input"
          onChange={(e) => onSearch(e.target.value)}
          placeholder="Search developing stories..."
          type="search"
          value={searchQuery}
        />
      </div>

      <div className="article-filters-row">
        <div className="article-filter-group">
          <span className="article-filter-label">Date</span>
          <div className="article-filter-tags">
            <button
              className={`article-filter-tag ${dateSortDirection === "desc" ? "active" : ""}`}
              onClick={() => setDateSortDirection("desc")}
              type="button"
            >
              Newest first
            </button>
            <button
              className={`article-filter-tag ${dateSortDirection === "asc" ? "active" : ""}`}
              onClick={() => setDateSortDirection("asc")}
              type="button"
            >
              Oldest first
            </button>
          </div>
        </div>

        <div className="article-filter-group">
          <span className="article-filter-label">Published</span>
          <div className="article-filter-tags">
            <button
              className={`article-filter-tag ${publishedFilter === "all" ? "active" : ""}`}
              onClick={() => setPublishedFilter("all")}
              type="button"
            >
              All
            </button>
            <button
              className={`article-filter-tag ${publishedFilter === "published" ? "active" : ""}`}
              onClick={() => setPublishedFilter("published")}
              type="button"
            >
              Published
            </button>
            <button
              className={`article-filter-tag ${publishedFilter === "draft" ? "active" : ""}`}
              onClick={() => setPublishedFilter("draft")}
              type="button"
            >
              Draft
            </button>
          </div>
        </div>
      </div>

      <div className="article-tabs">
        <button
          aria-pressed={activeTab === "all"}
          className={`article-tab ${activeTab === "all" ? "active" : ""}`}
          onClick={() => onChangeTab("all")}
          type="button"
        >
          All
        </button>
        <button
          aria-pressed={activeTab === "trash"}
          className={`article-tab ${activeTab === "trash" ? "active" : ""}`}
          onClick={() => onChangeTab("trash")}
          type="button"
        >
          <TrashIcon className="article-tab-icon" />
          Trash
          <span className="article-trash-badge">{trashedItems.length}</span>
        </button>
      </div>

      <div className="article-table-card">
        <table className="article-table">
          <thead>
            <tr>
              <th scope="col">Title</th>
              <th scope="col">Status</th>
              <th scope="col">Date</th>
              <th className="actions" scope="col">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="empty" colSpan={4}>
                  Loading articles...
                </td>
              </tr>
            ) : error ? (
              <tr>
                <td className="empty" colSpan={4}>
                  Failed to load articles: {error}
                </td>
              </tr>
            ) : articles.length === 0 ? (
              <tr>
                <td className="empty" colSpan={4}>
                  {searchQuery ? `No results for "${searchQuery}"` : `No ${activeTab === "trash" ? "trashed" : ""} developing stories yet.`}
                </td>
              </tr>
            ) : (
              articles.map((item) => (
                <tr key={item.id}>
                  <td>{item.title}</td>
                  <td>
                    <span className={`article-status ${item.status.toLowerCase()}`}>{item.status}</span>
                  </td>
                  <td>{item.date}</td>
                  <td className="actions">
                    <button className="article-action-button" title="Edit" type="button">
                      <PencilIcon className="article-action-icon" />
                    </button>
                    <button className="article-action-button danger" title="Delete" type="button">
                      <TrashIcon className="article-action-icon" />
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <Pagination
        className="article-kumo-pagination"
        page={page + 1}
        perPage={PAGE_SIZE}
        setPage={(nextPage) => {
          if (!Number.isFinite(nextPage)) return
          const normalizedPage = Math.max(1, Math.trunc(nextPage))
          const maxPage = Math.max(1, Math.ceil(effectiveTotalCount / PAGE_SIZE))
          const boundedPage = Math.min(normalizedPage, maxPage)
          setPage(boundedPage - 1)
        }}
        totalCount={effectiveTotalCount}
      >
        <Pagination.Info>
          {() => (
            <span className="article-count">{articles.length} article{articles.length === 1 ? "" : "s"} shown</span>
          )}
        </Pagination.Info>
        <Pagination.Controls controls="full" />
      </Pagination>
    </div>
  )
}

export default DevelopingStoriesView
