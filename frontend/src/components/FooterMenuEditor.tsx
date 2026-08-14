import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ExternalLink,
  Link2,
  Minus,
  Plus,
  Trash2,
  Type,
} from "lucide-react"
import { useEffect, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"
import SettingsSection from "./SettingsSection"

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

const kindOptions: { kind: FooterEntryKind; label: string; icon: typeof Type }[] = [
  { kind: "heading", label: "Heading", icon: Type },
  { kind: "link", label: "Link", icon: Link2 },
  { kind: "spacer", label: "Spacer", icon: Minus },
]

const inputClass =
  "w-full px-2.5 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
const iconButtonClass = "p-1 rounded-md hover:bg-muted disabled:opacity-30 disabled:hover:bg-transparent"

async function readErrorMessage(res: Response, fallback: string) {
  try {
    const body = await res.json() as { error?: string }
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

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
 *
 * Columns are laid out side by side so the editor reads like the footer it
 * produces; per-entry controls stay hidden until the entry is hovered or
 * focused, which keeps a six-column menu from becoming a wall of buttons.
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
        if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load footer settings (${res.status})`))
        const body = (await res.json()) as { columns?: unknown }
        const loaded = normalizeColumns(body.columns)
        if (!cancelled) {
          setColumns(loaded)
          setSaved(JSON.stringify(loaded))
        }
      } catch (err) {
        if (!cancelled) setMessage(err instanceof Error ? err.message : "Could not load footer settings.")
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

  function addEntry(columnIndex: number, kind: FooterEntryKind) {
    const column = columns[columnIndex]
    updateColumn(columnIndex, { entries: [...column.entries, { ...emptyEntry, kind }] })
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
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not save footer settings (${res.status})`))
      // The server drops unlabelled entries and empty columns, so render what
      // it stored rather than the draft.
      const body = (await res.json()) as { columns?: unknown }
      const stored = normalizeColumns(body.columns)
      setColumns(stored)
      setSaved(JSON.stringify(stored))
      setMessage("Saved")
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not save footer settings.")
    } finally {
      setSaving(false)
    }
  }

  const dirty = JSON.stringify(columns) !== saved

  return (
    <SettingsSection
      title="Footer"
      storageKey="footer"
      dirty={dirty}
      summary={
        loading
          ? "Loading..."
          : `${columns.length} column${columns.length === 1 ? "" : "s"}`
      }
      description="Links shown in the public site footer. Columns appear left to right; headings are bold entries; spacers split groups inside a column."
    >
      {loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : columns.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-8 flex flex-col items-center gap-3 text-center">
          <p className="text-sm text-muted-foreground max-w-sm">
            The footer menu is empty. Add a column, or save to restore the default footer.
          </p>
          <button
            type="button"
            onClick={() => setColumns([{ entries: [{ ...emptyEntry, kind: "heading" }] }])}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm hover:bg-muted"
          >
            <Plus className="w-4 h-4" aria-hidden="true" />
            Add column
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-start">
          {columns.map((column, columnIndex) => (
            <div
              key={columnIndex}
              className="rounded-lg border border-border bg-background/40 flex flex-col divide-y divide-border"
            >
              <div className="flex items-center justify-between gap-2 px-3 py-2">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Column {columnIndex + 1}
                </span>
                <div className="flex items-center gap-0.5">
                  <button
                    type="button"
                    aria-label={`Move column ${columnIndex + 1} earlier`}
                    title="Move earlier"
                    onClick={() => moveColumn(columnIndex, -1)}
                    disabled={columnIndex === 0}
                    className={iconButtonClass}
                  >
                    <ArrowLeft className="w-4 h-4" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Move column ${columnIndex + 1} later`}
                    title="Move later"
                    onClick={() => moveColumn(columnIndex, 1)}
                    disabled={columnIndex === columns.length - 1}
                    className={iconButtonClass}
                  >
                    <ArrowRight className="w-4 h-4" aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove column ${columnIndex + 1}`}
                    title="Remove column"
                    onClick={() => setColumns(columns.filter((_, i) => i !== columnIndex))}
                    className="p-1 rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Trash2 className="w-4 h-4" aria-hidden="true" />
                  </button>
                </div>
              </div>

              <div className="flex flex-col divide-y divide-border/60">
                {column.entries.map((entry, entryIndex) => (
                  <div key={entryIndex} className="group/entry px-3 py-2 flex flex-col gap-1.5 hover:bg-muted/40">
                    <div className="flex items-center justify-between gap-2">
                      <div className="inline-flex rounded-md border border-border overflow-hidden shrink-0">
                        {kindOptions.map(({ kind, label, icon: Icon }) => (
                          <button
                            key={kind}
                            type="button"
                            title={label}
                            aria-label={label}
                            aria-pressed={entry.kind === kind}
                            onClick={() => updateEntry(columnIndex, entryIndex, { kind })}
                            className={`px-1.5 py-1 ${
                              entry.kind === kind
                                ? "bg-muted text-foreground"
                                : "text-muted-foreground/50 hover:bg-muted hover:text-foreground"
                            }`}
                          >
                            <Icon className="w-3.5 h-3.5" aria-hidden="true" />
                          </button>
                        ))}
                      </div>

                      <div className="ml-auto flex items-center gap-0.5">
                        {/* Stays visible while on, so "opens in a new tab" is legible without hovering. */}
                        {entry.kind !== "spacer" && (
                          <button
                            type="button"
                            title="Open in new tab"
                            aria-label="Open in new tab"
                            aria-pressed={entry.new_tab}
                            onClick={() => updateEntry(columnIndex, entryIndex, { new_tab: !entry.new_tab })}
                            className={`p-1 rounded-md ${
                              entry.new_tab
                                ? "bg-primary/10 text-primary"
                                : "text-muted-foreground hover:bg-muted opacity-0 group-hover/entry:opacity-100 focus-visible:opacity-100 transition-opacity"
                            }`}
                          >
                            <ExternalLink className="w-4 h-4" aria-hidden="true" />
                          </button>
                        )}
                      </div>

                      <div className="flex items-center gap-0.5 opacity-0 group-hover/entry:opacity-100 focus-within:opacity-100 transition-opacity">
                        <button
                          type="button"
                          aria-label="Move entry up"
                          title="Move up"
                          onClick={() => moveEntry(columnIndex, entryIndex, -1)}
                          disabled={entryIndex === 0}
                          className={iconButtonClass}
                        >
                          <ArrowUp className="w-4 h-4" aria-hidden="true" />
                        </button>
                        <button
                          type="button"
                          aria-label="Move entry down"
                          title="Move down"
                          onClick={() => moveEntry(columnIndex, entryIndex, 1)}
                          disabled={entryIndex === column.entries.length - 1}
                          className={iconButtonClass}
                        >
                          <ArrowDown className="w-4 h-4" aria-hidden="true" />
                        </button>
                        <button
                          type="button"
                          aria-label="Remove entry"
                          title="Remove entry"
                          onClick={() =>
                            updateColumn(columnIndex, {
                              entries: column.entries.filter((_, i) => i !== entryIndex),
                            })
                          }
                          className="p-1 rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                        >
                          <Trash2 className="w-4 h-4" aria-hidden="true" />
                        </button>
                      </div>
                    </div>

                    {entry.kind === "spacer" ? (
                      <div className="flex items-center gap-2 py-0.5">
                        <span className="flex-1 border-t border-dashed border-border" aria-hidden="true" />
                        <span className="text-xs text-muted-foreground">Blank line</span>
                        <span className="flex-1 border-t border-dashed border-border" aria-hidden="true" />
                      </div>
                    ) : (
                      <>
                        <input
                          aria-label="Label"
                          value={entry.label}
                          onChange={(e) => updateEntry(columnIndex, entryIndex, { label: e.target.value })}
                          placeholder="Label"
                          className={`${inputClass} ${entry.kind === "heading" ? "font-semibold" : ""}`}
                        />
                        <input
                          aria-label="Link target"
                          value={entry.href}
                          onChange={(e) => updateEntry(columnIndex, entryIndex, { href: e.target.value })}
                          placeholder="/section or https://..."
                          className={`${inputClass} text-muted-foreground`}
                        />
                      </>
                    )}
                  </div>
                ))}
              </div>

              <div className="px-3 py-2 flex flex-wrap items-center gap-1.5">
                {kindOptions.map(({ kind, label, icon: Icon }) => (
                  <button
                    key={kind}
                    type="button"
                    onClick={() => addEntry(columnIndex, kind)}
                    className="inline-flex items-center gap-1 px-2 py-1 rounded-md border border-border text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                  >
                    <Icon className="w-3.5 h-3.5" aria-hidden="true" />
                    {label}
                  </button>
                ))}
              </div>
            </div>
          ))}

          <button
            type="button"
            onClick={() => setColumns([...columns, { entries: [{ ...emptyEntry, kind: "heading" }] }])}
            className="rounded-lg border border-dashed border-border px-3 py-6 flex items-center justify-center gap-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
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
          Save footer
        </button>
        {dirty && !saving && <span className="text-sm text-muted-foreground">Unsaved changes</span>}
        {message && <span className="text-sm text-muted-foreground">{message}</span>}
      </div>
    </SettingsSection>
  )
}
