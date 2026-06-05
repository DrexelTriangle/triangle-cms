import { useCallback, useEffect, useState } from "react"
import type { FormEvent } from "react"
import { Search, Plus, Pencil, Trash2, X, ChevronFirst, ChevronLast, ChevronLeft, ChevronRight } from "lucide-react"
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

const PAGE_SIZE_OPTIONS = [25, 50, 100, 200]
const DEFAULT_PAGE_SIZE = 50

type SortMode = "alpha" | "newest" | "oldest"

// authors has no timestamp column, so newest/oldest sort by the auto-increment id.
const SORT_PARAMS: Record<SortMode, { sortBy: string; sortDirection: "asc" | "desc" }> = {
  alpha: { sortBy: "display_name", sortDirection: "asc" },
  newest: { sortBy: "id", sortDirection: "desc" },
  oldest: { sortBy: "id", sortDirection: "asc" },
}

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

// Mirrors the server's canonical slug rules (lowercase, non-alphanumeric runs -> "-", trimmed).
function slugify(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "")
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
  const [debouncedSearch, setDebouncedSearch] = useState("")
  const [page, setPage] = useState(0)
  const [sortMode, setSortMode] = useState<SortMode>("alpha")
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [totalAuthorCount, setTotalAuthorCount] = useState(0)
  const [isCreateOpen, setIsCreateOpen] = useState(false)

  const loadAuthors = useCallback(() => {
    setIsLoading(true)
    const searchParam = debouncedSearch.trim() ? `&search=${encodeURIComponent(debouncedSearch.trim())}` : ""
    const { sortBy, sortDirection } = SORT_PARAMS[sortMode]
    return apiFetch(`/v1/authors?limit=${pageSize}&offset=${page * pageSize}&sort_by=${sortBy}&sort_direction=${sortDirection}${searchParam}`)
      .then((r) => {
        if (!r.ok) throw new Error(`Request failed (${r.status})`)
        return r.json() as Promise<Author[] | AuthorsResponse>
      })
      .then((data) => {
        const items = Array.isArray(data) ? data : (data.authors ?? [])
        setAuthors(items)
        const apiTotalCount = Array.isArray(data) ? undefined : (data.pagination?.total_count ?? data.pagination?.totalCount)
        const hasMore = Array.isArray(data) ? (items.length === pageSize) : Boolean(data.pagination?.has_more ?? data.pagination?.hasMore)
        const fallbackTotalCount = (page * pageSize) + items.length + (hasMore ? 1 : 0)
        setTotalAuthorCount(typeof apiTotalCount === "number" ? apiTotalCount : fallbackTotalCount)
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load authors"))
      .finally(() => setIsLoading(false))
  }, [apiFetch, page, pageSize, debouncedSearch, sortMode])

  useEffect(() => {
    loadAuthors()
  }, [loadAuthors])

  // Debounce search input before it hits the backend.
  useEffect(() => {
    const handle = setTimeout(() => setDebouncedSearch(search), 250)
    return () => clearTimeout(handle)
  }, [search])

  useEffect(() => {
    setPage(0)
  }, [debouncedSearch, pageSize, sortMode])

  const filtered = authors
  const effectiveTotalCount = Math.max(totalAuthorCount, (page * pageSize) + authors.length)
  const totalPages = Math.max(1, Math.ceil(effectiveTotalCount / pageSize))

  const sortTagClass = (active: boolean) =>
    `px-3 py-1 rounded-full text-xs font-medium transition-colors cursor-pointer border ${
      active
        ? "bg-primary text-primary-foreground border-primary"
        : "bg-background text-muted-foreground border-border hover:border-primary hover:text-primary"
    }`

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Authors</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading…" : `${effectiveTotalCount} authors total`}
          </p>
        </div>
        <button
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          type="button"
          onClick={() => setIsCreateOpen(true)}
        >
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

      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Sort</span>
        <div className="flex gap-1.5">
          <button className={sortTagClass(sortMode === "alpha")} onClick={() => setSortMode("alpha")} type="button">
            Alphabetical
          </button>
          <button className={sortTagClass(sortMode === "newest")} onClick={() => setSortMode("newest")} type="button">
            Newest first
          </button>
          <button className={sortTagClass(sortMode === "oldest")} onClick={() => setSortMode("oldest")} type="button">
            Oldest first
          </button>
        </div>
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
        <div className="flex items-center gap-3">
          <span>{filtered.length} author{filtered.length === 1 ? "" : "s"} shown</span>
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
      {isCreateOpen && (
        <CreateAuthorModal
          onClose={() => setIsCreateOpen(false)}
          onCreated={() => {
            setIsCreateOpen(false)
            loadAuthors()
          }}
        />
      )}
    </div>
  )
}

function CreateAuthorModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const apiFetch = useApiFetch()
  const [displayName, setDisplayName] = useState("")
  const [firstName, setFirstName] = useState("")
  const [lastName, setLastName] = useState("")
  const [email, setEmail] = useState("")
  const [slug, setSlug] = useState("")
  const [slugEdited, setSlugEdited] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const effectiveSlug = slugEdited ? slugify(slug) : slugify(displayName)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!displayName.trim()) {
      setError("Display name is required")
      return
    }
    if (!effectiveSlug) {
      setError("A valid slug is required")
      return
    }
    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/authors", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          slug: effectiveSlug,
          display_name: displayName.trim(),
          first_name: firstName.trim(),
          last_name: lastName.trim(),
          email: email.trim(),
        }),
      })
      if (!res.ok) {
        let message = `Failed to create author (${res.status})`
        try {
          const data = await res.json()
          if (data?.error) message = data.error
        } catch { /* keep default message */ }
        throw new Error(message)
      }
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create author")
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div className="w-full max-w-md rounded-xl border border-border bg-card shadow-lg" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-border px-5 py-4">
          <h2 className="text-lg font-semibold text-foreground">New Author</h2>
          <button className="p-1 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors" type="button" onClick={onClose} title="Close">
            <X className="w-4 h-4" />
          </button>
        </div>
        <form className="flex flex-col gap-4 px-5 py-4" onSubmit={submit}>
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium text-foreground">Display name <span className="text-destructive">*</span></span>
            <input
              className="rounded-lg border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              autoFocus
            />
          </label>
          <div className="grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1.5 text-sm">
              <span className="font-medium text-foreground">First name</span>
              <input
                className="rounded-lg border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                value={firstName}
                onChange={(e) => setFirstName(e.target.value)}
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span className="font-medium text-foreground">Last name</span>
              <input
                className="rounded-lg border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
                value={lastName}
                onChange={(e) => setLastName(e.target.value)}
              />
            </label>
          </div>
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium text-foreground">Email</span>
            <input
              type="email"
              className="rounded-lg border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </label>
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium text-foreground">Slug</span>
            <input
              className="rounded-lg border border-border bg-background px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              value={effectiveSlug}
              placeholder="auto-generated"
              onChange={(e) => { setSlugEdited(true); setSlug(e.target.value) }}
            />
          </label>

          {error && <p className="text-sm text-destructive">{error}</p>}

          <div className="flex items-center justify-end gap-2 pt-1">
            <button
              className="px-4 py-2 rounded-lg border border-border text-sm font-medium text-foreground hover:bg-muted transition-colors"
              type="button"
              onClick={onClose}
              disabled={isSaving}
            >
              Cancel
            </button>
            <button
              className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              type="submit"
              disabled={isSaving}
            >
              {isSaving ? "Creating…" : "Create author"}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

export default AuthorsView
