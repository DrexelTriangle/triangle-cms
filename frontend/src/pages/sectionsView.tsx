import { useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import { Search, Plus, Pencil, Trash2, RefreshCw, X, Check } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type TaxonomyType = "section" | "subsection"

type TaxonomyItem = {
  id: number
  type: TaxonomyType | "tag"
  slug: string
  canonical_title: string
  parent_slug?: string | null
}

type SectionRow = {
  item: TaxonomyItem
  isChild: boolean
  isLast: boolean
  parentTitle?: string
}

function toCanonicalSlug(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

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
  const [sections, setSections] = useState<TaxonomyItem[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState("")
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [newTitle, setNewTitle] = useState("")
  const [newSlug, setNewSlug] = useState("")
  const [newType, setNewType] = useState<TaxonomyType>("section")
  const [newParentSlug, setNewParentSlug] = useState("")
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [editTitle, setEditTitle] = useState("")
  const [editSlug, setEditSlug] = useState("")
  const [editParentSlug, setEditParentSlug] = useState("")

  const loadSections = async () => {
    setIsLoading(true)
    setError(null)
    try {
      const response = await apiFetch("/v1/taxonomy")
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      const body = await response.json() as TaxonomyItem[]
      setSections((Array.isArray(body) ? body : []).filter((item) => item.type === "section" || item.type === "subsection"))
    } catch (err) {
      setSections([])
      setError(err instanceof Error ? err.message : "Failed to load taxonomy")
    } finally {
      setIsLoading(false)
    }
  }

  useEffect(() => {
    void loadSections()
  }, [apiFetch])

  useEffect(() => {
    if (!newTitle.trim()) {
      setNewSlug("")
      return
    }
    setNewSlug((current) => {
      const next = toCanonicalSlug(newTitle)
      if (!current || current === toCanonicalSlug(current)) return next
      return current
    })
  }, [newTitle])

  const sectionOptions = useMemo(
    () => sections
      .filter((item) => item.type === "section")
      .sort((left, right) => left.canonical_title.localeCompare(right.canonical_title)),
    [sections],
  )

  const rows = useMemo(() => {
    const bySlug = new Map(sections.map((item) => [item.slug, item]))
    const parents = sections
      .filter((item) => item.type === "section")
      .sort((left, right) => left.canonical_title.localeCompare(right.canonical_title))
    const children = sections
      .filter((item) => item.type === "subsection")
      .sort((left, right) => left.canonical_title.localeCompare(right.canonical_title))

    const childrenByParent = new Map<string, TaxonomyItem[]>()
    for (const child of children) {
      const parentSlug = child.parent_slug ?? ""
      if (!childrenByParent.has(parentSlug)) {
        childrenByParent.set(parentSlug, [])
      }
      childrenByParent.get(parentSlug)?.push(child)
    }

    const builtRows: SectionRow[] = []
    for (const parent of parents) {
      const parentChildren = childrenByParent.get(parent.slug) ?? []
      builtRows.push({ item: parent, isChild: false, isLast: false })
      parentChildren.forEach((child, index) => {
        builtRows.push({
          item: child,
          isChild: true,
          isLast: index === parentChildren.length - 1,
          parentTitle: bySlug.get(parent.slug)?.canonical_title ?? parent.slug,
        })
      })
      childrenByParent.delete(parent.slug)
    }

    const orphanedChildren = [...childrenByParent.values()].flat()
    orphanedChildren.forEach((child, index) => {
      builtRows.push({
        item: child,
        isChild: true,
        isLast: index === orphanedChildren.length - 1,
        parentTitle: child.parent_slug ?? "Missing parent",
      })
    })

    return builtRows
  }, [sections])

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    if (!query) return rows
    return rows.filter(({ item, parentTitle }) =>
      item.canonical_title.toLowerCase().includes(query)
      || item.slug.toLowerCase().includes(query)
      || (parentTitle ?? "").toLowerCase().includes(query),
    )
  }, [rows, search])

  async function createSection(e: FormEvent) {
    e.preventDefault()
    const canonicalTitle = newTitle.trim()
    const slug = toCanonicalSlug(newSlug)
    if (!canonicalTitle || !slug) return

    setIsSaving(true)
    setError(null)
    try {
      const payload: {
        type: TaxonomyType
        slug: string
        canonical_title: string
        parent_slug?: string
      } = {
        type: newType,
        slug,
        canonical_title: canonicalTitle,
      }

      if (newType === "subsection" && newParentSlug.trim()) {
        payload.parent_slug = newParentSlug.trim()
      }

      const response = await apiFetch("/v1/taxonomy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      setNewTitle("")
      setNewSlug("")
      setNewType("section")
      setNewParentSlug("")
      setShowCreateForm(false)
      await loadSections()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create section")
    } finally {
      setIsSaving(false)
    }
  }

  function beginEdit(item: TaxonomyItem) {
    setEditingKey(`${item.type}:${item.slug}`)
    setEditTitle(item.canonical_title)
    setEditSlug(item.slug)
    setEditParentSlug(item.parent_slug ?? "")
  }

  async function saveEdit(item: TaxonomyItem) {
    const canonicalTitle = editTitle.trim()
    const slug = toCanonicalSlug(editSlug)
    if (!canonicalTitle || !slug) return

    setIsSaving(true)
    setError(null)
    try {
      const payload: {
        canonical_title: string
        slug: string
        parent_slug?: string
      } = {
        canonical_title: canonicalTitle,
        slug,
      }

      if (item.type === "subsection" && editParentSlug.trim()) {
        payload.parent_slug = editParentSlug.trim()
      }

      const response = await apiFetch(`/v1/taxonomy/${encodeURIComponent(item.type)}/${encodeURIComponent(item.slug)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      setEditingKey(null)
      await loadSections()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update section")
    } finally {
      setIsSaving(false)
    }
  }

  async function deleteItem(item: TaxonomyItem) {
    if (!window.confirm(`Delete "${item.canonical_title}"?`)) return

    setIsSaving(true)
    setError(null)
    try {
      const response = await apiFetch(`/v1/taxonomy/${encodeURIComponent(item.type)}/${encodeURIComponent(item.slug)}`, {
        method: "DELETE",
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      await loadSections()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete section")
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Sections</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${sections.length} taxonomy-backed sections and subsections`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors disabled:opacity-60"
            type="button"
            onClick={() => void loadSections()}
            disabled={isLoading || isSaving}
          >
            <RefreshCw className="w-4 h-4" />
            Refresh
          </button>
          <button
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
            type="button"
            onClick={() => setShowCreateForm((value) => !value)}
          >
            <Plus className="w-4 h-4" />
            Add Section
          </button>
        </div>
      </div>

      {showCreateForm && (
        <form onSubmit={createSection} className="rounded-xl border border-border bg-card p-4 grid grid-cols-1 md:grid-cols-4 gap-3">
          <input
            className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            placeholder="Section name"
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
          />
          <input
            className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            placeholder="slug"
            value={newSlug}
            onChange={(e) => setNewSlug(e.target.value)}
          />
          <select
            className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            value={newType}
            onChange={(e) => {
              const nextType = e.target.value as TaxonomyType
              setNewType(nextType)
              if (nextType === "section") setNewParentSlug("")
            }}
          >
            <option value="section">Section</option>
            <option value="subsection">Subsection</option>
          </select>
          <select
            className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            value={newParentSlug}
            onChange={(e) => setNewParentSlug(e.target.value)}
            disabled={newType !== "subsection"}
          >
            <option value="">No parent</option>
            {sectionOptions.map((section) => (
              <option key={section.slug} value={section.slug}>{section.canonical_title}</option>
            ))}
          </select>
          <div className="md:col-span-4 flex items-center gap-2">
            <button
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-60"
              type="submit"
              disabled={isSaving || !newTitle.trim() || !toCanonicalSlug(newSlug)}
            >
              Create
            </button>
            <button
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors"
              type="button"
              onClick={() => setShowCreateForm(false)}
            >
              Cancel
            </button>
          </div>
        </form>
      )}

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
          <div className="px-4 py-3 border-b border-border text-sm text-destructive bg-destructive/5">{error}</div>
        ) : null}
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Type</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Parent</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={5}>Loading sections...</td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={5}>No sections found.</td>
              </tr>
            ) : (
              filtered.map(({ item, isChild, isLast, parentTitle }) => {
                const itemKey = `${item.type}:${item.slug}`
                const isEditing = editingKey === itemKey
                return (
                  <tr key={itemKey} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-3 font-medium text-foreground">
                      {isEditing ? (
                        <input
                          className="w-full max-w-xs px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                          value={editTitle}
                          onChange={(e) => setEditTitle(e.target.value)}
                        />
                      ) : isChild ? (
                        <span className="flex items-start gap-1.5">
                          <span className="flex flex-col items-center w-4 shrink-0 mt-0.5">
                            <span className="w-px h-2 bg-border" />
                            <span className="w-3 h-px bg-border" />
                            {!isLast && <span className="w-px flex-1 bg-transparent" />}
                          </span>
                          <span className="text-muted-foreground">{item.canonical_title}</span>
                        </span>
                      ) : (
                        item.canonical_title
                      )}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground font-mono text-xs">
                      {isEditing ? (
                        <input
                          className="w-full max-w-xs px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                          value={editSlug}
                          onChange={(e) => setEditSlug(e.target.value)}
                        />
                      ) : (
                        item.slug
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary capitalize">
                        {item.type}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {isEditing && item.type === "subsection" ? (
                        <select
                          className="w-full max-w-xs px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                          value={editParentSlug}
                          onChange={(e) => setEditParentSlug(e.target.value)}
                        >
                          <option value="">No parent</option>
                          {sectionOptions
                            .filter((section) => section.slug !== item.slug)
                            .map((section) => (
                              <option key={section.slug} value={section.slug}>{section.canonical_title}</option>
                            ))}
                        </select>
                      ) : (
                        parentTitle ?? "—"
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        {isEditing ? (
                          <>
                            <button
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                              type="button"
                              onClick={() => void saveEdit(item)}
                              title="Save"
                              disabled={isSaving}
                            >
                              <Check className="w-4 h-4" />
                            </button>
                            <button
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                              type="button"
                              onClick={() => setEditingKey(null)}
                              title="Cancel"
                            >
                              <X className="w-4 h-4" />
                            </button>
                          </>
                        ) : (
                          <>
                            <button
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                              type="button"
                              onClick={() => beginEdit(item)}
                              title="Edit"
                            >
                              <Pencil className="w-4 h-4" />
                            </button>
                            <button
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                              type="button"
                              onClick={() => void deleteItem(item)}
                              title="Delete"
                              disabled={isSaving}
                            >
                              <Trash2 className="w-4 h-4" />
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
