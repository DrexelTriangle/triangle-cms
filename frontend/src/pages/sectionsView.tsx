import { useCallback, useEffect, useMemo, useState } from "react"
import { AlertTriangle, Columns3, Eye, EyeOff, Pencil, Plus, RefreshCw, Search, Trash2, X } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type TaxonomyKind = "section" | "subsection"

type TaxonomyItem = {
  id: number
  type: string
  slug: string
  canonical_title: string
  parent_slug?: string | null
  article_count: number
  category_aliases?: string[] | null
  is_visible?: boolean
}

type SectionRow = {
  item: TaxonomyItem
  parentTitle: string | null
  childCount: number
  isChild: boolean
  isLast: boolean
  // How far from a section this row sits: 0 for a section, 1 for a subsection,
  // 2 for a subsection of a subsection. The tree is three levels deep now (A&E
  // > Food > Beer Reviews), so indentation cannot be a boolean any more.
  depth: number
}

// How deep the tree may go, counting the section. Mirrors MaxTaxonomyDepth on
// the server, which is the side that enforces it -- this copy only decides which
// rows the parent picker offers, so that the editor does not present a choice
// the save would reject.
const MAX_DEPTH = 3

type FormState = {
  type: TaxonomyKind
  canonicalTitle: string
  slug: string
  parentSlug: string
  // One alias per line in the textarea; articles are matched on the whole
  // category name, so this is how a section whose slug differs from its
  // category ("entertainment" vs "Arts & Entertainment") finds its articles.
  categoryAliases: string
  isVisible: boolean
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
  categoryAliases: "",
  isVisible: true,
}

// What hiding actually does, said in full, because two thirds of it is what it
// does NOT do: a hidden item keeps its page and keeps feeding its section.
const visibilityHint =
  "Hidden items keep their own page and their articles still appear on the parent section, they just lose their link in the section's subsection list."

// Article matching is exact, so a slug that is not the category name resolves
// to nothing and the section page renders empty -- which reads as "no articles
// yet" rather than as a misconfiguration. Say what it usually means.
const emptyCountHint =
  "No articles match this item. If it should have articles, add the exact category name they are filed under under Category Names."

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
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const [showHidden, setShowHidden] = useState(true)

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

  const childrenByParent = useMemo(() => {
    const grouped = new Map<string, TaxonomyItem[]>()
    for (const child of items.filter((item) => item.type === "subsection")) {
      const parentSlug = child.parent_slug ?? ""
      if (!grouped.has(parentSlug)) {
        grouped.set(parentSlug, [])
      }
      grouped.get(parentSlug)?.push(child)
    }
    for (const group of grouped.values()) {
      group.sort((left, right) => left.canonical_title.localeCompare(right.canonical_title))
    }
    return grouped
  }, [items])

  const rows = useMemo(() => {
    const nextRows: SectionRow[] = []
    const rendered = new Set<number>()

    // Recursive, because the tree is three levels: a section, its subsections,
    // and theirs. The rendered set doubles as the cycle guard -- this walks
    // server data, and a row that somehow parented an ancestor would otherwise
    // loop forever and hang the screen rather than showing a broken tree.
    const pushSubtree = (item: TaxonomyItem, parentTitle: string | null, depth: number, isLast: boolean) => {
      if (rendered.has(item.id)) return
      rendered.add(item.id)
      const children = childrenByParent.get(item.slug) ?? []
      nextRows.push({
        item,
        parentTitle,
        childCount: children.length,
        isChild: depth > 0,
        isLast,
        depth,
      })
      children.forEach((child, index) => {
        pushSubtree(child, item.canonical_title, depth + 1, index === children.length - 1)
      })
    }

    for (const section of parentSections) {
      pushSubtree(section, null, 0, false)
    }

    // Anything the walk did not reach hangs under a parent that no longer
    // exists. Still listed, and named as orphaned, because an invisible row is
    // one nobody can fix.
    for (const item of items) {
      if (item.type !== "subsection" || rendered.has(item.id)) continue
      nextRows.push({
        item,
        parentTitle: item.parent_slug ? `Unknown parent: ${item.parent_slug}` : "No parent",
        childCount: (childrenByParent.get(item.slug) ?? []).length,
        isChild: true,
        isLast: true,
        depth: 1,
      })
    }

    return nextRows
  }, [items, parentSections, childrenByParent])

  // Depth of the chain ABOVE a slug, so the parent picker can drop anything that
  // has no room left beneath it.
  const depthOf = useCallback((slug: string) => {
    const bySlug = new Map(items.map((item) => [item.slug, item]))
    let depth = 1
    let current = bySlug.get(slug)
    const seen = new Set<string>()
    while (current && current.type === "subsection" && current.parent_slug) {
      if (seen.has(current.slug)) break
      seen.add(current.slug)
      depth += 1
      current = bySlug.get(current.parent_slug)
    }
    return depth
  }, [items])

  // Everything a subsection may hang under: the sections, plus the subsections
  // still shallow enough to take a child. Editing a row excludes itself and its
  // own descendants, since neither can be its parent.
  const parentChoices = useMemo(() => {
    const editingSlug = editor?.mode === "edit" ? editor.item.slug : null

    const forbidden = new Set<string>()
    if (editingSlug) {
      const queue = [editingSlug]
      while (queue.length > 0) {
        const slug = queue.shift() as string
        if (forbidden.has(slug)) continue
        forbidden.add(slug)
        for (const child of childrenByParent.get(slug) ?? []) {
          queue.push(child.slug)
        }
      }
    }

    // The moved row brings its own subtree along, so the room it needs is its
    // own height, not one level.
    const movedHeight = editingSlug
      ? (() => {
          const heightOf = (slug: string): number => {
            const children = childrenByParent.get(slug) ?? []
            return children.length === 0 ? 0 : 1 + Math.max(...children.map((child) => heightOf(child.slug)))
          }
          return heightOf(editingSlug)
        })()
      : 0

    return items
      .filter((item) => item.type === "section" || item.type === "subsection")
      .filter((item) => !forbidden.has(item.slug))
      .filter((item) => depthOf(item.slug) + 1 + movedHeight <= MAX_DEPTH)
      .sort((left, right) => {
        const leftDepth = depthOf(left.slug)
        const rightDepth = depthOf(right.slug)
        if (leftDepth !== rightDepth) return leftDepth - rightDepth
        return left.canonical_title.localeCompare(right.canonical_title)
      })
  }, [items, childrenByParent, depthOf, editor])

  const filtered = useMemo(() => {
    const query = search.toLowerCase().trim()
    // A hidden parent still has to show when its children are on screen, or the
    // tree loses its root and the indented rows dangle.
    const visibleRows = showHidden
      ? rows
      : rows.filter(({ item, childCount }) => item.is_visible !== false || childCount > 0)
    if (!query) return visibleRows
    return visibleRows.filter(({ item, parentTitle }) =>
      item.canonical_title.toLowerCase().includes(query)
      || item.slug.toLowerCase().includes(query)
      || typeLabel(item).toLowerCase().includes(query)
      || (parentTitle ?? "").toLowerCase().includes(query),
    )
  }, [rows, search, showHidden])

  const hiddenCount = useMemo(
    () => items.filter((item) => item.is_visible === false).length,
    [items],
  )

  const openCreate = (type: TaxonomyKind, parentSlug = "") => {
    // Any row that can still take a child, not just a section: "Add subsection"
    // from Food has to preselect Food.
    const parentExists = parentSlug && parentChoices.some((choice) => choice.slug === parentSlug)
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
      categoryAliases: (item.category_aliases ?? []).join("\n"),
      isVisible: item.is_visible !== false,
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
    const categoryAliases = form.categoryAliases
      .split("\n")
      .map((alias) => alias.trim())
      .filter((alias) => alias.length > 0)

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
        category_aliases: categoryAliases,
        is_visible: form.isVisible,
      }

      const response = editor.mode === "create"
        ? await apiFetch("/v1/taxonomy", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        })
        // The PUT is addressed by the item's CURRENT type and slug, while the
        // body carries what it should become -- that is how a section is
        // converted into a subsection.
        : await apiFetch(`/v1/taxonomy/${encodeURIComponent(editor.item.type)}/${encodeURIComponent(editor.item.slug)}`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
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

  // Visibility is the one field worth changing without opening the editor: it
  // is how the desk curates the subsection strip, which is a lot of small
  // yes/no decisions across 90-odd rows.
  //
  // category_aliases is deliberately absent from the body -- omitting it leaves
  // the stored value alone, and a toggle must not be able to wipe the matching
  // rules that keep a section's page full.
  const toggleVisibility = async (item: TaxonomyItem) => {
    const nextVisible = item.is_visible === false
    setTogglingId(item.id)
    setError(null)
    try {
      const response = await apiFetch(`/v1/taxonomy/${encodeURIComponent(item.type)}/${encodeURIComponent(item.slug)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type: item.type,
          slug: item.slug,
          canonical_title: item.canonical_title,
          parent_slug: item.parent_slug ?? null,
          is_visible: nextVisible,
        }),
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      setItems((current) => current.map((row) => (
        row.id === item.id ? { ...row, is_visible: nextVisible } : row
      )))
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to change visibility")
    } finally {
      setTogglingId(null)
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
            {isLoading
              ? "Loading..."
              : `${parentSections.length} sections, ${items.filter((item) => item.type === "subsection").length} subsections${hiddenCount > 0 ? `, ${hiddenCount} hidden` : ""}`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm font-medium hover:bg-muted/40 transition-colors"
            type="button"
            title={showHidden ? "Show only the items linked on section pages" : "Show every item, including hidden ones"}
            onClick={() => setShowHidden((current) => !current)}
          >
            {showHidden ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
            {showHidden ? "Hide hidden" : "Show hidden"}
          </button>
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
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Linked</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>Loading sections...</td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>No sections found.</td>
              </tr>
            ) : (
              filtered.map(({ item, parentTitle, childCount, isChild, isLast, depth }) => {
                const articleCount = item.article_count ?? 0
                const isVisible = item.is_visible !== false
                const canDelete = articleCount === 0 && childCount === 0
                const deleteTitle = articleCount > 0
                  ? "Cannot delete while articles use this item."
                  : childCount > 0
                    ? "Cannot delete while subsections use this item."
                    : `Delete ${item.canonical_title}`

                return (
                  <tr key={`${item.type}-${item.slug}-${item.id}`} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                    <td className="px-4 py-3 font-medium text-foreground">
                      {isChild ? (
                        <span className="flex items-start gap-1.5">
                          {/* One blank rail per level above the elbow, so a
                              grandchild reads as sitting under its subsection
                              rather than beside it. */}
                          {Array.from({ length: depth - 1 }, (_, level) => (
                            <span key={level} className="w-4 shrink-0" aria-hidden="true" />
                          ))}
                          <span className="flex flex-col items-center w-4 shrink-0 mt-0.5">
                            <span className="w-px h-2 bg-border" />
                            <span className="w-3 h-px bg-border" />
                            {!isLast && <span className="w-px flex-1 bg-transparent" />}
                          </span>
                          <span>
                            <span className="text-muted-foreground">{item.canonical_title}</span>
                            {childCount > 0 ? <span className="ml-2 text-xs text-muted-foreground">({childCount})</span> : null}
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
                      <span
                        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${
                          articleCount === 0
                            ? "bg-amber-500/10 text-amber-600 dark:text-amber-500"
                            : "bg-primary/10 text-primary"
                        }`}
                        title={articleCount === 0 ? emptyCountHint : undefined}
                      >
                        {articleCount === 0 ? <AlertTriangle className="w-3 h-3" /> : null}
                        {articleCount.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <button
                        className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium transition-colors disabled:opacity-50 ${
                          isVisible
                            ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-500 hover:bg-emerald-500/20"
                            : "bg-muted text-muted-foreground hover:bg-muted/70"
                        }`}
                        type="button"
                        disabled={togglingId === item.id}
                        title={`${isVisible ? "Hide" : "Show"} ${item.canonical_title}. ${visibilityHint}`}
                        onClick={() => void toggleVisibility(item)}
                      >
                        {isVisible ? <Eye className="w-3 h-3" /> : <EyeOff className="w-3 h-3" />}
                        {isVisible ? "Linked" : "Hidden"}
                      </button>
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
                {editor.mode === "edit" && form.type !== editor.item.type ? (
                  <span className="text-xs text-amber-600 dark:text-amber-500">
                    Changing the type moves this item&rsquo;s page between /sections and
                    /subsections, so existing links to it will break. A section has to have
                    no subsections of its own before it can become one.
                  </span>
                ) : null}
              </label>

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
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Parent</span>
                  <select
                    className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                    value={form.parentSlug}
                    onChange={(event) => setForm((current) => ({ ...current, parentSlug: event.target.value }))}
                  >
                    <option value="">Choose a parent</option>
                    {parentChoices.map((choice) => (
                      <option key={`${choice.type}-${choice.slug}`} value={choice.slug}>
                        {/* Indented with figure spaces so a subsection reads as
                            sitting under its section in a plain <select>, which
                            cannot nest beyond one optgroup level. */}
                        {"  ".repeat(depthOf(choice.slug) - 1)}{choice.canonical_title}
                      </option>
                    ))}
                  </select>
                  <span className="text-xs text-muted-foreground">
                    A subsection can sit under a section or under another subsection &mdash;
                    Beer Reviews sits under Food, which sits under Arts &amp; Entertainment.
                    Anything already {MAX_DEPTH} levels deep is left out, since nothing can hang below it.
                  </span>
                </label>
              ) : null}

              <label className="flex flex-col gap-1.5">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Category Names</span>
                <textarea
                  className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/40"
                  rows={3}
                  placeholder="Arts &amp; Entertainment"
                  value={form.categoryAliases}
                  onChange={(event) => setForm((current) => ({ ...current, categoryAliases: event.target.value }))}
                />
                <span className="text-xs text-muted-foreground">
                  One per line. Articles are matched on the exact category name, so add one
                  here whenever the name on the articles differs from the name above &mdash;
                  the Entertainment section holds articles filed under &ldquo;Arts &amp; Entertainment&rdquo;.
                  Leave empty when they match.
                </span>
              </label>

              <label className="flex items-start gap-2.5">
                <input
                  className="mt-0.5 h-4 w-4 rounded border-border accent-primary"
                  type="checkbox"
                  checked={form.isVisible}
                  onChange={(event) => setForm((current) => ({ ...current, isVisible: event.target.checked }))}
                />
                <span className="flex flex-col gap-0.5">
                  <span className="text-sm text-foreground">Link this item on its section page</span>
                  <span className="text-xs text-muted-foreground">{visibilityHint}</span>
                </span>
              </label>
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
