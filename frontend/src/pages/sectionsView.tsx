import { useCallback, useEffect, useMemo, useState } from "react"
import { Columns3, Pencil, Plus, RefreshCw, Search, Trash2, X } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type TaxonomyKind = "section" | "subsection"

type TaxonomyItem = {
  id: number
  type: string
  slug: string
  canonical_title: string
  parent_slug?: string | null
  article_count: number
}

type SectionRow = {
  item: TaxonomyItem
  parentTitle: string | null
  childCount: number
  isChild: boolean
  isLast: boolean
}

type FormState = {
  type: TaxonomyKind
  canonicalTitle: string
  slug: string
  parentSlug: string
}

type EditorState =
  | { mode: "create"; label: string }
  | { mode: "edit"; item: TaxonomyItem; label: string }

const SECTION_ORDER = [
  "news",
  "sports",
  "opinion",
  "columns",
  "entertainment",
  "comics-puzzles",
  "special-editions",
]

const emptyForm: FormState = {
  type: "section",
  canonicalTitle: "",
  slug: "",
  parentSlug: "",
}

const slugify = (value: string) =>
  value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")

async function readErrorMessage(response: Response) {
  try {
    const body = await response.json() as { error?: string }
    return body.error ?? `Request failed (${response.status})`
  } catch {
    return `Request failed (${response.status})`
  }
}

function typeLabel(item: TaxonomyItem) {
  if (item.type === "section") return "Section"
  if (item.parent_slug === "columns") return "Column"
  return "Subsection"
}

export default function SectionsView() {
  const apiFetch = useApiFetch()
  const [search, setSearch] = useState("")
  const [items, setItems] = useState<TaxonomyItem[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [editor, setEditor] = useState<EditorState | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<TaxonomyItem | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [slugTouched, setSlugTouched] = useState(false)

  const loadSections = useCallback(async () => {
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
  }, [apiFetch])

  useEffect(() => {
    void loadSections()
  }, [loadSections])

  const parentSections = useMemo(() => {
    return items
      .filter((item) => item.type === "section")
      .sort((left, right) => {
        const leftIndex = SECTION_ORDER.indexOf(left.slug)
        const rightIndex = SECTION_ORDER.indexOf(right.slug)
        if (leftIndex !== -1 || rightIndex !== -1) {
          if (leftIndex === -1) return 1
          if (rightIndex === -1) return -1
          return leftIndex - rightIndex
        }
        return left.canonical_title.localeCompare(right.canonical_title)
      })
  }, [items])

  const rows = useMemo(() => {
    const children = items
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

    const nextRows: SectionRow[] = []
    const renderedChildren = new Set<number>()

    for (const parent of parentSections) {
      const parentChildren = childrenByParent.get(parent.slug) ?? []
      nextRows.push({
        item: parent,
        parentTitle: null,
        childCount: parentChildren.length,
        isChild: false,
        isLast: false,
      })
      parentChildren.forEach((child, index) => {
        renderedChildren.add(child.id)
        nextRows.push({
          item: child,
          parentTitle: parent.canonical_title,
          childCount: 0,
          isChild: true,
          isLast: index === parentChildren.length - 1,
        })
      })
    }

    for (const child of children) {
      if (renderedChildren.has(child.id)) continue
      nextRows.push({
        item: child,
        parentTitle: child.parent_slug ? `Unknown parent: ${child.parent_slug}` : "No parent",
        childCount: 0,
        isChild: true,
        isLast: true,
      })
    }

    return nextRows
  }, [items, parentSections])

  const filtered = useMemo(() => {
    const query = search.toLowerCase().trim()
    if (!query) return rows
    return rows.filter(({ item, parentTitle }) =>
      item.canonical_title.toLowerCase().includes(query)
      || item.slug.toLowerCase().includes(query)
      || typeLabel(item).toLowerCase().includes(query)
      || (parentTitle ?? "").toLowerCase().includes(query),
    )
  }, [rows, search])

  const openCreate = (type: TaxonomyKind, parentSlug = "") => {
    const parentExists = parentSlug && parentSections.some((section) => section.slug === parentSlug)
    const nextParent = type === "subsection"
      ? (parentExists ? parentSlug : parentSections.some((section) => section.slug === "columns") ? "columns" : parentSections[0]?.slug ?? "")
      : ""
    setForm({ ...emptyForm, type, parentSlug: nextParent })
    setSlugTouched(false)
    setError(null)
    setEditor({ mode: "create", label: nextParent === "columns" ? "Add Column" : type === "section" ? "Add Section" : "Add Subsection" })
  }

  const openEdit = (item: TaxonomyItem) => {
    if (item.type !== "section" && item.type !== "subsection") return
    setForm({
      type: item.type,
      canonicalTitle: item.canonical_title,
      slug: item.slug,
      parentSlug: item.parent_slug ?? "",
    })
    setSlugTouched(true)
    setError(null)
    setEditor({ mode: "edit", item, label: `Edit ${typeLabel(item)}` })
  }

  const closeEditor = () => {
    if (isSaving) return
    setEditor(null)
    setForm(emptyForm)
    setSlugTouched(false)
  }

  const updateTitle = (value: string) => {
    setForm((current) => ({
      ...current,
      canonicalTitle: value,
      slug: slugTouched ? current.slug : slugify(value),
    }))
  }

  const updateType = (type: TaxonomyKind) => {
    setForm((current) => ({
      ...current,
      type,
      parentSlug: type === "subsection"
        ? current.parentSlug || (parentSections.some((section) => section.slug === "columns") ? "columns" : parentSections[0]?.slug ?? "")
        : "",
    }))
  }

  const saveTaxonomy = async () => {
    if (!editor) return
    const canonicalTitle = form.canonicalTitle.trim()
    const slug = slugify(form.slug)
    const parentSlug = form.type === "subsection" ? form.parentSlug.trim() : ""

    if (!canonicalTitle) {
      setError("Name is required.")
      return
    }
    if (!slug) {
      setError("Slug is required.")
      return
    }
    if (form.type === "subsection" && !parentSlug) {
      setError("Choose a parent section.")
      return
    }

    setIsSaving(true)
    setError(null)
    try {
      const body = {
        type: form.type,
        slug,
        canonical_title: canonicalTitle,
        parent_slug: form.type === "subsection" ? parentSlug : null,
      }

      const response = editor.mode === "create"
        ? await apiFetch("/v1/taxonomy", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        })
        : await apiFetch(`/v1/taxonomy/${encodeURIComponent(editor.item.type)}/${encodeURIComponent(editor.item.slug)}`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            slug,
            canonical_title: canonicalTitle,
            parent_slug: form.type === "subsection" ? parentSlug : null,
          }),
        })

      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      setEditor(null)
      setForm(emptyForm)
      setSlugTouched(false)
      await loadSections()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to save taxonomy item")
    } finally {
      setIsSaving(false)
    }
  }

  const deleteTaxonomy = async () => {
    if (!deleteTarget) return
    setIsSaving(true)
    setError(null)
    try {
      const response = await apiFetch(`/v1/taxonomy/${encodeURIComponent(deleteTarget.type)}/${encodeURIComponent(deleteTarget.slug)}`, {
        method: "DELETE",
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      setDeleteTarget(null)
      await loadSections()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to delete taxonomy item")
    } finally {
      setIsSaving(false)
    }
  }
  const hasColumnsSection = parentSections.some((section) => section.slug === "columns")

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Sections</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${parentSections.length} sections, ${items.filter((item) => item.type === "subsection").length} subsections`}
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
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors disabled:opacity-50"
            type="button"
            disabled={!hasColumnsSection}
            title={hasColumnsSection ? "Add a column under Columns" : "Create the Columns section before adding a column."}
            onClick={() => openCreate("subsection", "columns")}
          >
            <Columns3 className="w-4 h-4" />
            Add Column
          </button>
          <button
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
            type="button"
            onClick={() => openCreate("section")}
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
          placeholder="Search sections, columns, or slugs..."
          type="search"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
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
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Type</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Articles</th>
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
              filtered.map(({ item, parentTitle, childCount, isChild, isLast }) => {
                const articleCount = item.article_count ?? 0
                const canDelete = articleCount === 0 && childCount === 0
                const deleteTitle = articleCount > 0
                  ? "Cannot delete while articles use this item."
                  : childCount > 0
                    ? "Cannot delete while subsections use this section."
                    : `Delete ${item.canonical_title}`

                return (
                  <tr key={`${item.type}-${item.slug}-${item.id}`} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-3 font-medium text-foreground">
                      {isChild ? (
                        <span className="flex items-start gap-1.5">
                          <span className="flex flex-col items-center w-4 shrink-0 mt-0.5">
                            <span className="w-px h-2 bg-border" />
                            <span className="w-3 h-px bg-border" />
                            {!isLast && <span className="w-px flex-1 bg-transparent" />}
                          </span>
                          <span>
                            <span className="text-muted-foreground">{item.canonical_title}</span>
                            {parentTitle ? <span className="block text-xs text-muted-foreground/70">{parentTitle}</span> : null}
                          </span>
                        </span>
                      ) : (
                        <span>
                          {item.canonical_title}
                          {childCount > 0 ? <span className="ml-2 text-xs text-muted-foreground">({childCount})</span> : null}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">{typeLabel(item)}</td>
                    <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{item.slug}</td>
                    <td className="px-4 py-3">
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">
                        {articleCount.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          className="p-1.5 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground transition-colors"
                          type="button"
                          title={`Edit ${item.canonical_title}`}
                          onClick={() => openEdit(item)}
                        >
                          <Pencil className="w-4 h-4" />
                        </button>
                        <button
                          className="p-1.5 rounded-lg text-muted-foreground hover:bg-destructive/10 hover:text-destructive transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                          type="button"
                          disabled={!canDelete}
                          title={deleteTitle}
                          onClick={() => setDeleteTarget(item)}
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>

      {editor ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4" role="dialog" aria-modal="true">
          <div className="w-full max-w-lg rounded-xl border border-border bg-card shadow-xl">
            <div className="flex items-center justify-between border-b border-border px-5 py-4">
              <h2 className="text-base font-semibold text-foreground">{editor.label}</h2>
              <button className="p-1.5 rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground" type="button" onClick={closeEditor}>
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex flex-col gap-4 px-5 py-4">
              {editor.mode === "create" ? (
                <label className="flex flex-col gap-1.5">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Type</span>
                  <select
                    className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                    value={form.type}
                    onChange={(event) => updateType(event.target.value as TaxonomyKind)}
                  >
                    <option value="section">Section</option>
                    <option value="subsection">Subsection / Column</option>
                  </select>
                </label>
              ) : null}

              <label className="flex flex-col gap-1.5">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Name</span>
                <input
                  className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                  value={form.canonicalTitle}
                  onChange={(event) => updateTitle(event.target.value)}
                />
              </label>

              <label className="flex flex-col gap-1.5">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Slug</span>
                <input
                  className="w-full px-3 py-2 rounded-lg border border-border bg-background font-mono text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                  value={form.slug}
                  onChange={(event) => {
                    setSlugTouched(true)
                    setForm((current) => ({ ...current, slug: slugify(event.target.value) }))
                  }}
                />
              </label>

              {form.type === "subsection" ? (
                <label className="flex flex-col gap-1.5">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Parent Section</span>
                  <select
                    className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                    value={form.parentSlug}
                    onChange={(event) => setForm((current) => ({ ...current, parentSlug: event.target.value }))}
                  >
                    <option value="">Choose a section</option>
                    {parentSections.map((section) => (
                      <option key={section.slug} value={section.slug}>
                        {section.canonical_title}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-4">
              <button
                className="px-4 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors"
                type="button"
                disabled={isSaving}
                onClick={closeEditor}
              >
                Cancel
              </button>
              <button
                className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
                type="button"
                disabled={isSaving}
                onClick={() => void saveTaxonomy()}
              >
                {isSaving ? "Saving..." : "Save"}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {deleteTarget ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4" role="dialog" aria-modal="true">
          <div className="w-full max-w-md rounded-xl border border-border bg-card shadow-xl">
            <div className="border-b border-border px-5 py-4">
              <h2 className="text-base font-semibold text-foreground">Delete {typeLabel(deleteTarget)}</h2>
            </div>
            <div className="px-5 py-4 text-sm text-muted-foreground">
              Delete <span className="font-medium text-foreground">{deleteTarget.canonical_title}</span>? This only works for empty items with no articles or child sections.
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-4">
              <button
                className="px-4 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors"
                type="button"
                disabled={isSaving}
                onClick={() => setDeleteTarget(null)}
              >
                Cancel
              </button>
              <button
                className="px-4 py-2 rounded-lg bg-destructive text-destructive-foreground text-sm font-medium hover:bg-destructive/90 transition-colors disabled:opacity-50"
                type="button"
                disabled={isSaving}
                onClick={() => void deleteTaxonomy()}
              >
                {isSaving ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
