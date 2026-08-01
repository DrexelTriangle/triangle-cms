import { ChevronRight } from "lucide-react"
import { useId, useState, type ReactNode } from "react"

type SettingsSectionProps = {
  title: string
  /** Shown only while expanded, so a collapsed section stays one compact row. */
  description?: ReactNode
  /** Short right-aligned hint (e.g. "6 columns") that keeps the collapsed row informative. */
  summary?: ReactNode
  /** Flags unsaved edits in the header so collapsing can't hide them. */
  dirty?: boolean
  /** Distinguishes this section's remembered open state; must be stable. */
  storageKey: string
  defaultOpen?: boolean
  children: ReactNode
}

const storagePrefix = "cms.settings.section."

function readOpen(storageKey: string, fallback: boolean): boolean {
  try {
    const stored = localStorage.getItem(storagePrefix + storageKey)
    return stored === null ? fallback : stored === "1"
  } catch {
    // Private-mode / disabled storage: fall back to the default rather than break the page.
    return fallback
  }
}

/**
 * Collapsible card for one settings group. The settings page is a stack of
 * independent groups, most of which are only touched occasionally, so each one
 * collapses to a single row and remembers that choice across visits.
 *
 * The body is hidden with `hidden` rather than unmounted: sections own loaded
 * state and in-progress edits, and remounting would refetch and discard them.
 */
export default function SettingsSection({
  title,
  description,
  summary,
  dirty = false,
  storageKey,
  defaultOpen = false,
  children,
}: SettingsSectionProps) {
  const [open, setOpen] = useState(() => readOpen(storageKey, defaultOpen))
  const bodyId = useId()

  function toggle() {
    setOpen((current) => {
      const next = !current
      try {
        localStorage.setItem(storagePrefix + storageKey, next ? "1" : "0")
      } catch {
        // Remembering the state is a nicety; ignore storage failures.
      }
      return next
    })
  }

  return (
    <section className="rounded-xl border border-border bg-card">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        aria-controls={bodyId}
        className="w-full flex items-center gap-3 px-6 py-4 text-left rounded-xl hover:bg-muted/30"
      >
        <ChevronRight
          className={`w-4 h-4 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
          {open && description && (
            <p className="text-sm text-muted-foreground mt-0.5 max-w-3xl">{description}</p>
          )}
        </div>
        {dirty && (
          <span className="shrink-0 px-2 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">
            Unsaved
          </span>
        )}
        {summary && <span className="shrink-0 text-xs text-muted-foreground">{summary}</span>}
      </button>
      {/* `flex` would override the `hidden` attribute's display:none, so the
          class list has to carry the collapse itself. */}
      <div
        id={bodyId}
        hidden={!open}
        className={`border-t border-border px-6 py-5 flex-col gap-4 ${open ? "flex" : "hidden"}`}
      >
        {children}
      </div>
    </section>
  )
}
