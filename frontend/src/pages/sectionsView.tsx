import { useEffect, useMemo, useState } from "react"
import { Search, Plus, Pencil, Trash2, RefreshCw } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type TaxonomyItem = {
  id: number
  type: string
  slug: string
  canonical_title: string
  parent_slug?: string | null
  article_count: number
}

type SectionRow = {
  section: {
    id: number
    name: string
    slug: string
    parent: string | null
    articles: number | null
  }
  isChild: boolean
  isLast: boolean
}

const SECTION_ORDER = [
  "news",
  "sports",
  "opinion",
  "columns",
  "entertainment",
  "comics-puzzles",
  "special-editions",
]

async function readErrorMessage(response: Response) {
  try {
    const body = await response.json() as { error?: string }
    return body.error ?? `Request failed (${response.status})`
  } catch {
    return `Request failed (${response.status})`
  }
}

export default function SectionsView() {
  const apiFetch = useApiFetch()
  const [search, setSearch] = useState("")
  const [items, setItems] = useState<TaxonomyItem[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const loadSections = async () => {
    setIsLoading(true)
    setError(null)
    try {
      const response = await apiFetch("/v1/taxonomy")
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      const body = await response.json() as TaxonomyItem[]
      setItems(
        (Array.isArray(body) ? body : []).filter(
          (item) => item.type === "section" || item.type === "subsection",
        ),
      )
    } catch (err) {
      setItems([])
      setError(err instanceof Error ? err.message : "Failed to load sections")
    } finally {
      setIsLoading(false)
    }
  }

  useEffect(() => {
    void loadSections()
  }, [apiFetch])

  const rows = useMemo(() => {
    const parents = items
      .filter((item) => item.type === "section")
      .sort((left, right) => {
        const leftIndex = SECTION_ORDER.indexOf(left.slug)
        const rightIndex = SECTION_ORDER.indexOf(right.slug)
        if (leftIndex !== -1 || rightIndex !== -1) {
          if (leftIndex === -1) return 1
          if (rightIndex === -1) return -1
          return leftIndex - rightIndex
        }
        return left.id - right.id
      })
    const children = items
      .filter((item) => item.type === "subsection")
      .sort((left, right) => left.id - right.id)

    const childrenByParent = new Map<string, TaxonomyItem[]>()
    for (const child of children) {
      const parentSlug = child.parent_slug ?? ""
      if (!childrenByParent.has(parentSlug)) {
        childrenByParent.set(parentSlug, [])
      }
      childrenByParent.get(parentSlug)?.push(child)
    }

    const nextRows: SectionRow[] = []
    for (const parent of parents) {
      const parentName = parent.canonical_title
      const parentChildren = childrenByParent.get(parent.slug) ?? []
      nextRows.push({
        section: {
          id: parent.id,
          name: parentName,
          slug: parent.slug,
          parent: null,
          articles: parent.article_count ?? 0,
        },
        isChild: false,
        isLast: false,
      })
      parentChildren.forEach((child, index) => {
        nextRows.push({
          section: {
            id: child.id,
            name: child.canonical_title,
            slug: child.slug,
            parent: parentName,
            articles: child.article_count ?? 0,
          },
          isChild: true,
          isLast: index === parentChildren.length - 1,
        })
      })
    }

    return nextRows
  }, [items])

  const filtered = useMemo(() => (
    rows.filter(({ section }) =>
      section.name.toLowerCase().includes(search.toLowerCase())
      || section.slug.toLowerCase().includes(search.toLowerCase()),
    )
  ), [rows, search])

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Sections</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${items.length} taxonomy items`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors"
            type="button"
            onClick={() => void loadSections()}
          >
            <RefreshCw className="w-4 h-4" />
            Refresh
          </button>
          <button
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium transition-colors opacity-60 cursor-not-allowed"
            type="button"
            disabled
            title="Editing is not wired on this screen yet."
          >
            <Plus className="w-4 h-4" />
            Add Section
          </button>
        </div>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          placeholder="Search sections..."
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        {error ? (
          <div className="px-4 py-3 border-b border-border text-sm text-destructive bg-destructive/5">
            {error}
          </div>
        ) : null}
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Articles</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={4}>Loading sections...</td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={4}>No sections found.</td>
              </tr>
            ) : (
              filtered.map(({ section: s, isChild, isLast }) => (
                <tr key={`${s.parent ?? "root"}-${s.slug}`} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3 font-medium text-foreground">
                    {isChild ? (
                      <span className="flex items-start gap-1.5">
                        <span className="flex flex-col items-center w-4 shrink-0 mt-0.5">
                          <span className="w-px h-2 bg-border" />
                          <span className="w-3 h-px bg-border" />
                          {!isLast && <span className="w-px flex-1 bg-transparent" />}
                        </span>
                        <span className="text-muted-foreground">{s.name}</span>
                      </span>
                    ) : (
                      s.name
                    )}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{s.slug}</td>
                  <td className="px-4 py-3">
                    <span
                      className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary"
                    >
                      {s.articles?.toLocaleString() ?? "0"}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground transition-colors opacity-50 cursor-not-allowed"
                        type="button"
                        disabled
                        title="Editing is not wired on this screen yet."
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground transition-colors opacity-50 cursor-not-allowed"
                        type="button"
                        disabled
                        title="Deletion is not wired on this screen yet."
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
    </div>
  )
}
