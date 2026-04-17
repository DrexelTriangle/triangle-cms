import { Pagination, buttonVariants } from "@cloudflare/kumo"
import { useEffect, useState } from "react"
import { ArrowSquareOutIcon, MagnifyingGlassIcon, PencilIcon, PlusIcon, TrashIcon, XIcon } from "@phosphor-icons/react"

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

type ApiAuthor = {
  id: number
  slug: string
  display_name: string
}

const PAGE_SIZE = 20
const AUTHORS_PAGE_SIZE = 200

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

function ArticleView() {
  const [searchQuery, setSearchQuery] = useState("")
  const [activeTab, setActiveTab] = useState<"all" | "trash">("all")
  const [page, setPage] = useState(0)
  const [articles, setArticles] = useState<ArticleItem[]>([])
  const [totalArticleCount, setTotalArticleCount] = useState(0)
  const [authors, setAuthors] = useState<ApiAuthor[]>([])
  const [authorQuery, setAuthorQuery] = useState("")
  const [selectedAuthorSlug, setSelectedAuthorSlug] = useState("")
  const [publishedFilter, setPublishedFilter] = useState<"all" | "published" | "draft">("all")
  const [dateSortDirection, setDateSortDirection] = useState<"asc" | "desc">("desc")
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const trashedItems: ArticleItem[] = []

  useEffect(() => {
    let cancelled = false

    const fetchAuthors = async () => {
      try {
        const allAuthors: ApiAuthor[] = []
        let offset = 0
        let keepFetching = true

        while (keepFetching) {
          const response = await fetch(`/v1/authors?limit=${AUTHORS_PAGE_SIZE}&offset=${offset}&sort_by=display_name&sort_direction=asc`)
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
  }, [])

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
        })
        if (selectedAuthorSlug) {
          params.set("author_slug", selectedAuthorSlug)
        }
        if (publishedFilter !== "all") {
          params.set("status", publishedFilter)
        }
        if (searchQuery.trim()) {
          params.set("title", searchQuery.trim())
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
          setTotalArticleCount(typeof apiTotalCount === "number" ? apiTotalCount : fallbackTotalCount)
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
  }, [dateSortDirection, page, publishedFilter, searchQuery, selectedAuthorSlug])

  const onChangeTab = (tab: "all" | "trash") => {
    setActiveTab(tab)
    setPage(0)
  }

  const onSearch = (value: string) => {
    setSearchQuery(value)
    setPage(0)
  }

  useEffect(() => {
    const normalizedValue = authorQuery.trim().toLowerCase()
    if (!normalizedValue) {
      setSelectedAuthorSlug("")
      return
    }

    const matchedAuthor = authors.find((author) => author.display_name.trim().toLowerCase() === normalizedValue)
    setSelectedAuthorSlug(matchedAuthor?.slug ?? "")
  }, [authorQuery, authors])

  useEffect(() => {
    setPage(0)
  }, [selectedAuthorSlug, publishedFilter, dateSortDirection, searchQuery])

  const effectiveTotalCount = Math.max(totalArticleCount, (page * PAGE_SIZE) + articles.length)

  return (
    <div className="article-list-page">
      <div className="article-list-header">
        <div className="article-list-title-row">
          <h1 className="article-list-title">Articles</h1>
        </div>
        <button className={`${buttonVariants()} article-add-new-button`} type="button">
          <PlusIcon aria-hidden="true" className="me-2 h-4 w-4" />
          Add New
        </button>
      </div>

      <div className="article-search-wrap">
        <MagnifyingGlassIcon className="article-search-icon" />
        <input
          aria-label="Search articles"
          className="article-search-input"
          onChange={(e) => onSearch(e.target.value)}
          placeholder="Search articles..."
          type="search"
          value={searchQuery}
        />
      </div>

      <div className="article-filters-row">
        <div className="article-filter-group">
          <label className="article-filter-label" htmlFor="article-author-filter">
            Author
          </label>
          <div className="article-author-input-wrap">
            <input
              className="article-filter-select"
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
                className="article-author-clear-button"
                onClick={() => setAuthorQuery("")}
                title="Clear author"
                type="button"
              >
                <XIcon />
              </button>
            )}
          </div>
          <datalist id="article-author-options">
            {authors.map((author) => (
              <option key={author.id} value={author.display_name} />
            ))}
          </datalist>
        </div>

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
                  {searchQuery ? `No results for "${searchQuery}"` : `No ${activeTab === "trash" ? "trashed" : ""} articles yet.`}
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
                    {item.status === "Published" && (
                      <a
                        className="article-view-live-link"
                        href={item.slug ? `http://localhost:4321/article/${encodeURIComponent(item.slug)}` : "#"}
                        rel="noreferrer"
                        target="_blank"
                      >
                        <ArrowSquareOutIcon className="article-action-icon" />
                        View Live
                      </a>
                    )}
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

export default ArticleView
