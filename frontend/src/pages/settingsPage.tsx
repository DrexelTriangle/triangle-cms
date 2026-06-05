import { LogOut } from "lucide-react"
import { useEffect, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"
import { useNavigate } from "react-router-dom"
import { useSessionAuth } from "../auth/sessionAuthContext"

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{label}</label>
      <div className="text-sm text-foreground">{children}</div>
    </div>
  )
}

export default function SettingsPage() {
  const { user, logout } = useSessionAuth()
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const name = user?.name ?? "—"
  const email = user?.email ?? "—"
  const username = user?.email ?? "—"
  const groups: string[] = []
  const initials = name.split(" ").map((n: string) => n[0]).join("").slice(0, 2).toUpperCase()
  const [siteTitle, setSiteTitle] = useState("")
  const [siteTitleDraft, setSiteTitleDraft] = useState("")
  const [siteTitleSaving, setSiteTitleSaving] = useState(false)
  const [siteTitleMessage, setSiteTitleMessage] = useState<string | null>(null)
  const [taxonomyRebuildRunning, setTaxonomyRebuildRunning] = useState(false)
  const [taxonomyRebuildMessage, setTaxonomyRebuildMessage] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function loadSiteSettings() {
      try {
        const res = await apiFetch("/v1/settings/site")
        if (!res.ok) throw new Error(`Failed to load site settings (${res.status})`)
        const body = (await res.json()) as { site_title?: string }
        const title = String(body.site_title ?? "").trim()
        if (!cancelled) {
          setSiteTitle(title)
          setSiteTitleDraft(title)
        }
      } catch (err) {
        if (!cancelled) {
          setSiteTitleMessage(err instanceof Error ? err.message : "Failed to load site settings")
        }
      }
    }
    loadSiteSettings()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  async function saveSiteTitle() {
    const nextTitle = siteTitleDraft.trim()
    if (!nextTitle) {
      setSiteTitleMessage("Site title cannot be empty")
      return
    }

    setSiteTitleSaving(true)
    setSiteTitleMessage(null)
    try {
      const res = await apiFetch("/v1/settings/site", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ site_title: nextTitle }),
      })
      if (!res.ok) throw new Error(`Failed to save site settings (${res.status})`)
      setSiteTitle(nextTitle)
      setSiteTitleDraft(nextTitle)
      setSiteTitleMessage("Saved")
    } catch (err) {
      setSiteTitleMessage(err instanceof Error ? err.message : "Failed to save site settings")
    } finally {
      setSiteTitleSaving(false)
    }
  }

  async function rebuildTaxonomyCounts() {
    setTaxonomyRebuildRunning(true)
    setTaxonomyRebuildMessage(null)
    try {
      const res = await apiFetch("/v1/settings/taxonomy/rebuild", {
        method: "POST",
      })
      if (!res.ok) throw new Error(`Failed to rebuild taxonomy counts (${res.status})`)
      setTaxonomyRebuildMessage("Taxonomy article counts rebuilt.")
    } catch (err) {
      setTaxonomyRebuildMessage(err instanceof Error ? err.message : "Failed to rebuild taxonomy counts")
    } finally {
      setTaxonomyRebuildRunning(false)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Your account information</p>
        </div>
        <button
          type="button"
          onClick={async () => {
            await logout()
            navigate("/login", { replace: true })
          }}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-destructive/40 text-destructive text-sm font-medium hover:bg-destructive/10 transition-colors"
        >
          <LogOut className="w-4 h-4" />
          Sign out
        </button>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-6">
        <div className="flex items-center gap-4">
          <div className="w-16 h-16 rounded-full bg-primary flex items-center justify-center text-white text-xl font-bold shrink-0">
            {initials}
          </div>
          <div>
            <p className="text-base font-semibold text-foreground">{name}</p>
            <p className="text-sm text-muted-foreground">{email}</p>
          </div>
        </div>

        <div className="border-t border-border pt-6 grid grid-cols-1 md:grid-cols-2 gap-6">
          <Field label="Full Name">{name}</Field>
          <Field label="Username">{username}</Field>
          <Field label="Email">{email}</Field>
          <Field label="Groups">
            {groups.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {groups.map((g) => (
                  <span key={g} className="px-2.5 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">
                    {g}
                  </span>
                ))}
              </div>
            ) : "—"}
          </Field>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-foreground">Site</h2>
        <p className="text-sm text-muted-foreground">Public site title used by the frontend.</p>
        <div className="flex flex-col gap-2 max-w-xl">
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Site Title</label>
          <input
            value={siteTitleDraft}
            onChange={(e) => setSiteTitleDraft(e.target.value)}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
            placeholder="The Triangle"
          />
        </div>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={saveSiteTitle}
            disabled={siteTitleSaving || siteTitleDraft.trim() === "" || siteTitleDraft.trim() === siteTitle}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Save Site Title
          </button>
          {siteTitleMessage && <span className="text-sm text-muted-foreground">{siteTitleMessage}</span>}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-4">
        <h2 className="text-lg font-semibold text-foreground">Taxonomy</h2>
        <p className="text-sm text-muted-foreground">
          Recalculate taxonomy article counts from current article category assignments.
        </p>
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={rebuildTaxonomyCounts}
            disabled={taxonomyRebuildRunning}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
          >
            Rebuild Taxonomy Counts
          </button>
          {taxonomyRebuildMessage && <span className="text-sm text-muted-foreground">{taxonomyRebuildMessage}</span>}
        </div>
      </div>
    </div>
  )
}
