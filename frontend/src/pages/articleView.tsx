import { Pagination, buttonVariants } from "@cloudflare/kumo"
import { useEffect, useMemo, useState } from "react"
import { ArrowSquareOutIcon, MagnifyingGlassIcon, PencilIcon, PlusIcon, TrashIcon } from "@phosphor-icons/react"

type ArticleStatus = "Published" | "Draft"
type ArticleItem = {
  id: string
  title: string
  status: ArticleStatus
  date: string
}

const PAGE_SIZE = 8

const articleData: ArticleItem[] = [
  { id: "1", title: "Budget negotiations continue into weekend", status: "Published", date: "Apr 16, 2026" },
  { id: "2", title: "Campus rail extension proposal advances", status: "Draft", date: "Apr 15, 2026" },
  { id: "3", title: "Editorial: Student housing priorities for fall", status: "Draft", date: "Apr 14, 2026" },
  { id: "4", title: "Alumni panel highlights startup trends", status: "Published", date: "Apr 13, 2026" },
  { id: "5", title: "Women in STEM fellowship opens applications", status: "Published", date: "Apr 12, 2026" },
  { id: "6", title: "Opinion: Why local reporting still matters", status: "Draft", date: "Apr 11, 2026" },
  { id: "7", title: "New sustainability grants announced", status: "Draft", date: "Apr 10, 2026" },
  { id: "8", title: "Voter guide for spring municipal election", status: "Published", date: "Apr 9, 2026" },
  { id: "9", title: "Faculty senate debates policy changes", status: "Draft", date: "Apr 8, 2026" },
  { id: "10", title: "Profiles: Five student founders to watch", status: "Published", date: "Apr 7, 2026" },
]

function ArticleView() {
  const [searchQuery, setSearchQuery] = useState("")
  const [activeTab, setActiveTab] = useState<"all" | "trash">("all")
  const [page, setPage] = useState(0)

  const trashedItems: ArticleItem[] = []

  const filteredItems = useMemo(() => {
    const pool = activeTab === "all" ? articleData : trashedItems
    if (!searchQuery.trim()) return pool
    const query = searchQuery.trim().toLowerCase()
    return pool.filter((item) => item.title.toLowerCase().includes(query))
  }, [activeTab, searchQuery, trashedItems])

  const totalPages = Math.max(1, Math.ceil(filteredItems.length / PAGE_SIZE))
  const paginatedItems = filteredItems.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE)

  useEffect(() => {
    setPage((current) => Math.min(current, totalPages - 1))
  }, [totalPages])

  const onChangeTab = (tab: "all" | "trash") => {
    setActiveTab(tab)
    setPage(0)
  }

  const onSearch = (value: string) => {
    setSearchQuery(value)
    setPage(0)
  }

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
            {paginatedItems.length === 0 ? (
                <tr>
                <td className="empty" colSpan={4}>
                  {searchQuery ? `No results for "${searchQuery}"` : `No ${activeTab === "trash" ? "trashed" : ""} articles yet.`}
                </td>
              </tr>
            ) : (
              paginatedItems.map((item) => (
                <tr key={item.id}>
                  <td>{item.title}</td>
                  <td>
                    <span className={`article-status ${item.status.toLowerCase()}`}>{item.status}</span>
                  </td>
                  <td>{item.date}</td>
                  <td className="actions">
                    {item.status === "Published" && (
                      <a className="article-view-live-link" href="#" rel="noreferrer" target="_blank">
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
        page={Math.min(page + 1, totalPages)}
        perPage={PAGE_SIZE}
        setPage={(nextPage) => setPage(Math.max(0, Math.min(totalPages - 1, nextPage - 1)))}
        totalCount={filteredItems.length}
      >
        <Pagination.Info>
          {({ totalCount }) => (
            <span className="article-count">
              {(totalCount ?? 0)} article{(totalCount ?? 0) === 1 ? "" : "s"}
            </span>
          )}
        </Pagination.Info>
        <Pagination.Controls controls="full" />
      </Pagination>
    </div>
  )
}

export default ArticleView
