import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"

type FooterEntryKind = "link" | "heading" | "spacer"

type FooterEntry = {
  kind: FooterEntryKind
  label: string
  href: string
  new_tab: boolean
}

type FooterColumn = {
  entries: FooterEntry[]
}

const emptyEntry: FooterEntry = { kind: "link", label: "", href: "", new_tab: false }

function normalizeEntry(raw: unknown): FooterEntry {
  const entry = (raw ?? {}) as Partial<FooterEntry>
  const kind: FooterEntryKind =
    entry.kind === "heading" || entry.kind === "spacer" ? entry.kind : "link"
  return {
    kind,
    label: String(entry.label ?? ""),
    href: String(entry.href ?? ""),
    new_tab: Boolean(entry.new_tab),
  }
}

function normalizeColumns(raw: unknown): FooterColumn[] {
  if (!Array.isArray(raw)) return []
  return raw.map((column) => ({
    entries: Array.isArray((column as FooterColumn | undefined)?.entries)
      ? (column as FooterColumn).entries.map(normalizeEntry)
      : [],
  }))
}

/**
 * Editor for the public site's footer menu. The footer is one ordered document
 * rather than a set of independent records, so the whole menu is loaded, edited
 * locally, and saved in a single PATCH.
 */
export default function FooterMenuEditor() {
  const apiFetch = useApiFetch()
  const [columns, setColumns] = useState<FooterColumn[]>([])
  const [saved, setSaved] = useState("")
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function loadFooter() {
      try {
        const res = await apiFetch("/v1/settings/footer")
        if (!res.ok) throw new Error(`Failed to load footer settings (${res.status})`)
        const body = (await res.json()) as { columns?: unknown }
        const loaded = normalizeColumns(body.columns)
        if (!cancelled) {
          setColumns(loaded)
          setSaved(JSON.stringify(loaded))
        }
      } catch (err) {
        if (!cancelled) setMessage(err instanceof Error ? err.message : "Failed to load footer settings")
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void loadFooter()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  function updateColumn(index: number, next: FooterColumn) {
    setColumns((current) => current.map((column, i) => (i === index ? next : column)))
  }

  function updateEntry(columnIndex: number, entryIndex: number, patch: Partial<FooterEntry>) {
    const column = columns[columnIndex]
    updateColumn(columnIndex, {
      entries: column.entries.map((entry, i) => (i === entryIndex ? { ...entry, ...patch } : entry)),
    })
  }

  function moveEntry(columnIndex: number, entryIndex: number, delta: number) {
    const column = columns[columnIndex]
    const target = entryIndex + delta
    if (target < 0 || target >= column.entries.length) return
    const entries = [...column.entries]
    const [moved] = entries.splice(entryIndex, 1)
    entries.splice(target, 0, moved)
    updateColumn(columnIndex, { entries })
  }

  function moveColumn(columnIndex: number, delta: number) {
    const target = columnIndex + delta
    if (target < 0 || target >= columns.length) return
    const next = [...columns]
    const [moved] = next.splice(columnIndex, 1)
    next.splice(target, 0, moved)
    setColumns(next)
  }

  async function save() {
    setSaving(true)
    setMessage(null)
    try {
      const res = await apiFetch("/v1/settings/footer", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ columns }),
      })
      if (!res.ok) throw new Error(`Failed to save footer settings (${res.status})`)
      // The server drops unlabelled entries and empty columns, so render what
      // it stored rather than the draft.
      const body = (await res.json()) as { columns?: unknown }
      const stored = normalizeColumns(body.columns)
      setColumns(stored)
      setSaved(JSON.stringify(stored))
      setMessage("Saved")
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Failed to save footer settings")
    } finally {
      setSaving(false)
    }
  }

  const dirty = JSON.stringify(columns) !== saved

  return (
    <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
      <h2 className="text-lg font-semibold text-foreground">Footer Menu</h2>
      <p className="text-sm text-muted-foreground">
        Link columns shown in the public site footer. Headings are the bold entries; a spacer starts a
        new group inside the same column. Saving an empty menu restores the built-in default.
      </p>

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <div className="flex flex-col gap-4">
          {columns.map((column, columnIndex) => (
            <div key={columnIndex} className="rounded-lg border border-border p-4 flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Column {columnIndex + 1}
                </span>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    aria-label={`Move column ${columnIndex + 1} earlier`}
                    onClick={() => moveColumn(columnIndex, -1)}
                    disabled={columnIndex === 0}
                    className="p-1.5 rounded-md hover:bg-muted disabled:opacity-40"
                  >
                    <ArrowUp className="w-4 h-4" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Move column ${columnIndex + 1} later`}
                    onClick={() => moveColumn(columnIndex, 1)}
                    disabled={columnIndex === columns.length - 1}
                    className="p-1.5 rounded-md hover:bg-muted disabled:opacity-40"
                  >
                    <ArrowDown className="w-4 h-4" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove column ${columnIndex + 1}`}
                    onClick={() => setColumns(columns.filter((_, i) => i !== columnIndex))}
                    className="p-1.5 rounded-md text-destructive hover:bg-destructive/10"
                  >
                    <Trash2 className="w-4 h-4" aria-hidden="true" />
                  </button>
                </div>
              </div>

              {column.entries.map((entry, entryIndex) => (
                <div key={entryIndex} className="flex flex-wrap items-center gap-2">
                  <select
                    aria-label="Entry type"
                    value={entry.kind}
                    onChange={(e) => updateEntry(columnIndex, entryIndex, { kind: e.target.value as FooterEntryKind })}
                    className="px-2 py-2 rounded-lg border border-border bg-background text-sm"
                  >
                    <option value="heading">Heading</option>
                    <option value="link">Link</option>
                    <option value="spacer">Spacer</option>
                  </select>

                  {entry.kind === "spacer" ? (
                    <span className="text-sm text-muted-foreground flex-1">Blank line</span>
                  ) : (
                    <>
                      <input
                        aria-label="Label"
                        value={entry.label}
                        onChange={(e) => updateEntry(columnIndex, entryIndex, { label: e.target.value })}
                        placeholder="Label"
                        className="flex-1 min-w-[8rem] px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      />
                      <input
                        aria-label="Link target"
                        value={entry.href}
                        onChange={(e) => updateEntry(columnIndex, entryIndex, { href: e.target.value })}
                        placeholder="/section or https://…"
                        className="flex-[2] min-w-[12rem] px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      />
                      <label className="flex items-center gap-1.5 text-sm text-muted-foreground select-none">
                        <input
                          type="checkbox"
                          checked={entry.new_tab}
                          onChange={(e) => updateEntry(columnIndex, entryIndex, { new_tab: e.target.checked })}
                          className="h-4 w-4 rounded border-border text-primary focus:ring-2 focus:ring-primary/40"
                        />
                        New tab
                      </label>
                    </>
                  )}

                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      aria-label="Move entry up"
                      onClick={() => moveEntry(columnIndex, entryIndex, -1)}
                      disabled={entryIndex === 0}
                      className="p-1.5 rounded-md hover:bg-muted disabled:opacity-40"
                    >
                      <ArrowUp className="w-4 h-4" aria-hidden="true" />
                    </button>
                    <button
                      type="button"
                      aria-label="Move entry down"
                      onClick={() => moveEntry(columnIndex, entryIndex, 1)}
                      disabled={entryIndex === column.entries.length - 1}
                      className="p-1.5 rounded-md hover:bg-muted disabled:opacity-40"
                    >
                      <ArrowDown className="w-4 h-4" aria-hidden="true" />
                    </button>
                    <button
                      type="button"
                      aria-label="Remove entry"
                      onClick={() =>
                        updateColumn(columnIndex, {
                          entries: column.entries.filter((_, i) => i !== entryIndex),
                        })
                      }
                      className="p-1.5 rounded-md text-destructive hover:bg-destructive/10"
                    >
                      <Trash2 className="w-4 h-4" aria-hidden="true" />
                    </button>
                  </div>
                </div>
              ))}

              <button
                type="button"
                onClick={() => updateColumn(columnIndex, { entries: [...column.entries, { ...emptyEntry }] })}
                className="self-start inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm hover:bg-muted"
              >
                <Plus className="w-4 h-4" aria-hidden="true" />
                Add entry
              </button>
            </div>
          ))}

          <button
            type="button"
            onClick={() => setColumns([...columns, { entries: [{ ...emptyEntry, kind: "heading" }] }])}
            className="self-start inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm hover:bg-muted"
          >
            <Plus className="w-4 h-4" aria-hidden="true" />
            Add column
          </button>
        </div>
      )}

      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => void save()}
          disabled={saving || loading || !dirty}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
        >
          Save Footer
        </button>
        {message && <span className="text-sm text-muted-foreground">{message}</span>}
      </div>
    </div>
  )
}
