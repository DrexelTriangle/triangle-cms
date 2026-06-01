import { useEffect, useState } from "react"
import { Search, Plus, Pencil, Trash2, ChevronFirst, ChevronLast, ChevronLeft, ChevronRight } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type Author = {
  id: number
  slug: string
  display_name: string
  email?: string
  article_count?: number
}

type AuthorsResponse = {
  authors?: Author[]
  pagination?: {
    has_more?: boolean
    hasMore?: boolean
    total_count?: number
    totalCount?: number
  }
}

const PAGE_SIZE = 50

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

const AVATAR_COLORS = [
  "bg-blue-500", "bg-violet-500", "bg-green-500", "bg-orange-500",
  "bg-rose-500", "bg-teal-500", "bg-indigo-500", "bg-amber-500",
  "bg-cyan-500", "bg-pink-500",
]

function AuthorsView() {
  const apiFetch = useApiFetch()
  const [authors, setAuthors] = useState<Author[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState("")
  const [page, setPage] = useState(0)
  const [totalAuthorCount, setTotalAuthorCount] = useState(0)

  useEffect(() => {
    setIsLoading(true)
    apiFetch(`/v1/authors?limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}&sort_by=display_name&sort_direction=asc`)
      .then((r) => {
        if (!r.ok) throw new Error(`Request failed (${r.status})`)
        return r.json() as Promise<Author[] | AuthorsResponse>
      })
      .then((data) => {
        const items = Array.isArray(data) ? data : (data.authors ?? [])
        setAuthors(items)
        const apiTotalCount = Array.isArray(data) ? undefined : (data.pagination?.total_count ?? data.pagination?.totalCount)
        const hasMore = Array.isArray(data) ? (items.length === PAGE_SIZE) : Boolean(data.pagination?.has_more ?? data.pagination?.hasMore)
        const fallbackTotalCount = (page * PAGE_SIZE) + items.length + (hasMore ? 1 : 0)
        setTotalAuthorCount(typeof apiTotalCount === "number" ? apiTotalCount : fallbackTotalCount)
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load authors"))
      .finally(() => setIsLoading(false))
  }, [apiFetch, page])

  useEffect(() => {
    setPage(0)
  }, [search])

  const filtered = authors.filter((a) =>
    a.display_name.toLowerCase().includes(search.toLowerCase()) ||
    (a.email ?? "").toLowerCase().includes(search.toLowerCase()) ||
    a.slug.toLowerCase().includes(search.toLowerCase())
  )
  const effectiveTotalCount = Math.max(totalAuthorCount, (page * PAGE_SIZE) + authors.length)
  const totalPages = Math.max(1, Math.ceil(effectiveTotalCount / PAGE_SIZE))

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Authors</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading…" : `${effectiveTotalCount} authors total`}
          </p>
        </div>
        <button className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
          <Plus className="w-4 h-4" />
          Add New
        </button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          placeholder="Search authors..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground w-10" scope="col" />
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Articles</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Email</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>Loading authors…</td>
              </tr>
            ) : error ? (
              <tr>
                <td className="px-4 py-8 text-center text-destructive" colSpan={6}>{error}</td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>
                  {search ? `No authors found for "${search}"` : "No authors yet."}
                </td>
              </tr>
            ) : (
              filtered.map((author, i) => (
                <tr key={author.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <div className={`w-8 h-8 rounded-full ${AVATAR_COLORS[i % AVATAR_COLORS.length]} flex items-center justify-center text-white text-xs font-bold`}>
                      {initials(author.display_name)}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-medium text-foreground">{author.display_name}</td>
                  <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{author.slug}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">
                      {author.article_count}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{author.email ?? "—"}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Edit">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete">
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

      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{filtered.length} author{filtered.length === 1 ? "" : "s"} shown</span>
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

export default AuthorsView
