import { useEffect, useState } from "react"
import { Globe, Link2, AlertCircle, CheckCircle, RefreshCw, Info } from "lucide-react"
import { useNavigate } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { useSessionAuth } from "../auth/sessionAuthContext"

type SeoIssue = {
  article_id: number
  slug: string
  title: string
  type: "error" | "warning"
  issue: string
}

type SeoAudit = {
  issues: SeoIssue[]
  total_issues: number
  published_count: number
}

type SeoSettings = {
  og_title: string
  og_description: string
  sitemap_url: string
  robots_url: string
}

// Mirrors auditWindowMonths in the backend (database/seo.go). The audit is scoped
// to recent articles so the imported legacy backlog doesn't drown out current issues.
const AUDIT_WINDOW_LABEL = "the last 24 months"

const EMPTY_SETTINGS: SeoSettings = {
  og_title: "",
  og_description: "",
  sitemap_url: "",
  robots_url: "",
}

export default function SeoView() {
  const apiFetch = useApiFetch()
  const navigate = useNavigate()
  const { user } = useSessionAuth()
  const canEdit = user?.role === "admin"

  const [audit, setAudit] = useState<SeoAudit | null>(null)
  const [auditLoading, setAuditLoading] = useState(true)
  const [auditError, setAuditError] = useState<string | null>(null)

  const [settings, setSettings] = useState<SeoSettings>(EMPTY_SETTINGS)
  const [settingsDraft, setSettingsDraft] = useState<SeoSettings>(EMPTY_SETTINGS)
  const [settingsSaving, setSettingsSaving] = useState(false)
  const [settingsMessage, setSettingsMessage] = useState<string | null>(null)

  async function loadAudit() {
    setAuditLoading(true)
    setAuditError(null)
    try {
      const res = await apiFetch("/v1/seo/audit")
      if (!res.ok) throw new Error(`Failed to load SEO audit (${res.status})`)
      setAudit((await res.json()) as SeoAudit)
    } catch (err) {
      setAuditError(err instanceof Error ? err.message : "Failed to load SEO audit")
    } finally {
      setAuditLoading(false)
    }
  }

  useEffect(() => {
    let cancelled = false
    async function load() {
      setAuditLoading(true)
      setAuditError(null)
      try {
        const [auditRes, settingsRes] = await Promise.all([
          apiFetch("/v1/seo/audit"),
          apiFetch("/v1/settings/seo"),
        ])
        if (!auditRes.ok) throw new Error(`Failed to load SEO audit (${auditRes.status})`)
        const auditBody = (await auditRes.json()) as SeoAudit
        if (settingsRes.ok) {
          const settingsBody = (await settingsRes.json()) as SeoSettings
          if (!cancelled) {
            setSettings(settingsBody)
            setSettingsDraft(settingsBody)
          }
        }
        if (!cancelled) setAudit(auditBody)
      } catch (err) {
        if (!cancelled) setAuditError(err instanceof Error ? err.message : "Failed to load SEO audit")
      } finally {
        if (!cancelled) setAuditLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [apiFetch])

  async function saveSettings() {
    setSettingsSaving(true)
    setSettingsMessage(null)
    try {
      const res = await apiFetch("/v1/settings/seo", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(settingsDraft),
      })
      if (!res.ok) throw new Error(`Failed to save SEO settings (${res.status})`)
      const saved = (await res.json()) as SeoSettings
      setSettings(saved)
      setSettingsDraft(saved)
      setSettingsMessage("Saved")
    } catch (err) {
      setSettingsMessage(err instanceof Error ? err.message : "Failed to save SEO settings")
    } finally {
      setSettingsSaving(false)
    }
  }

  const issues = audit?.issues ?? []
  const totalIssues = audit?.total_issues ?? 0
  const errorCount = issues.filter((i) => i.type === "error").length
  const publishedCount = audit?.published_count ?? 0
  const isCapped = totalIssues > issues.length
  const settingsDirty = JSON.stringify(settings) !== JSON.stringify(settingsDraft)

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">SEO</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Search engine optimization overview</p>
        </div>
        <button
          type="button"
          onClick={loadAudit}
          disabled={auditLoading}
          className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm font-medium text-foreground hover:bg-muted transition-colors disabled:opacity-60"
        >
          <RefreshCw className={`w-4 h-4 ${auditLoading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-2 gap-4">
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Issues Found</p>
          <p className={`text-2xl font-bold mt-1 ${totalIssues > 0 ? "text-destructive" : "text-foreground"}`}>
            {auditLoading ? "—" : totalIssues.toLocaleString()}
          </p>
        </div>
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Articles Audited</p>
          <p className="text-2xl font-bold text-foreground mt-1">
            {auditLoading ? "—" : publishedCount.toLocaleString()}
          </p>
          <p className="text-[11px] text-muted-foreground mt-0.5">Published in {AUDIT_WINDOW_LABEL}</p>
        </div>
      </div>

      <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
        <Info className="w-3.5 h-3.5 shrink-0 mt-0.5" />
        <p>
          This audit covers articles published in {AUDIT_WINDOW_LABEL}. Older imported content predates
          structured SEO metadata, so it's excluded to keep the results focused on issues worth fixing.
        </p>
      </div>

      {/* Issues */}
      <div className="flex flex-col gap-4">
        <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
          <AlertCircle className="w-4 h-4 text-destructive" />
          SEO Issues
          {errorCount > 0 && (
            <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-destructive/10 text-destructive">
              {errorCount} {errorCount === 1 ? "error" : "errors"}
            </span>
          )}
        </h2>
        {isCapped && !auditLoading && (
          <p className="text-xs text-muted-foreground -mt-2">
            Showing the {issues.length.toLocaleString()} most recent of {totalIssues.toLocaleString()} issues.
          </p>
        )}
        <div className="flex flex-col gap-2">
          {auditError ? (
            <div className="flex items-center gap-2 p-3 rounded-xl border border-destructive/30 bg-destructive/5">
              <AlertCircle className="w-4 h-4 text-destructive shrink-0" />
              <p className="text-sm text-destructive">{auditError}</p>
            </div>
          ) : auditLoading ? (
            <p className="text-sm text-muted-foreground">Scanning published articles…</p>
          ) : issues.length === 0 ? (
            <div className="flex items-center gap-2 p-3 rounded-xl border border-green-300/50 bg-green-50/50 dark:border-green-700/30 dark:bg-green-900/10">
              <CheckCircle className="w-4 h-4 text-green-600 shrink-0" />
              <p className="text-sm text-muted-foreground">
                All {publishedCount} published articles have valid SEO metadata.
              </p>
            </div>
          ) : (
            issues.map((issue, idx) => (
              <button
                key={`${issue.article_id}-${idx}`}
                type="button"
                onClick={() => navigate(`/articles/${encodeURIComponent(issue.slug)}/edit`)}
                className={`flex items-start gap-3 rounded-xl border p-4 text-left transition-colors ${
                  issue.type === "error"
                    ? "border-destructive/30 bg-destructive/5 hover:bg-destructive/10"
                    : "border-yellow-300/50 bg-yellow-50/50 hover:bg-yellow-100/50 dark:border-yellow-700/30 dark:bg-yellow-900/10 dark:hover:bg-yellow-900/20"
                }`}
              >
                <AlertCircle
                  className={`w-4 h-4 shrink-0 mt-0.5 ${issue.type === "error" ? "text-destructive" : "text-yellow-500"}`}
                />
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-foreground truncate">{issue.title || issue.slug}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">{issue.issue}</p>
                </div>
                <span
                  className={`text-xs font-semibold px-2 py-0.5 rounded-full shrink-0 ${
                    issue.type === "error"
                      ? "bg-destructive/10 text-destructive"
                      : "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400"
                  }`}
                >
                  {issue.type}
                </span>
              </button>
            ))
          )}
        </div>
      </div>

      {/* Site-wide SEO settings */}
      <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4">
        <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
          <Globe className="w-4 h-4" />
          Default Social / Open Graph Settings
        </h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Default OG Title</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition disabled:opacity-60"
              value={settingsDraft.og_title}
              onChange={(e) => setSettingsDraft((s) => ({ ...s, og_title: e.target.value }))}
              disabled={!canEdit}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Default OG Description</label>
            <input
              className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition disabled:opacity-60"
              value={settingsDraft.og_description}
              onChange={(e) => setSettingsDraft((s) => ({ ...s, og_description: e.target.value }))}
              disabled={!canEdit}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Sitemap URL</label>
            <div className="flex items-center gap-2">
              <input
                className="flex-1 px-3 py-2 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition disabled:opacity-60"
                value={settingsDraft.sitemap_url}
                onChange={(e) => setSettingsDraft((s) => ({ ...s, sitemap_url: e.target.value }))}
                disabled={!canEdit}
              />
              <a
                href={settings.sitemap_url || undefined}
                target="_blank"
                rel="noreferrer"
                className="p-2 rounded-lg border border-border text-muted-foreground hover:bg-muted transition-colors"
                title="Open sitemap"
              >
                <Link2 className="w-4 h-4" />
              </a>
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Robots.txt</label>
            <div className="flex items-center gap-2">
              <input
                className="flex-1 px-3 py-2 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition disabled:opacity-60"
                value={settingsDraft.robots_url}
                onChange={(e) => setSettingsDraft((s) => ({ ...s, robots_url: e.target.value }))}
                disabled={!canEdit}
              />
              <a
                href={settings.robots_url || undefined}
                target="_blank"
                rel="noreferrer"
                className="p-2 rounded-lg border border-border text-muted-foreground hover:bg-muted transition-colors"
                title="Open robots.txt"
              >
                <Link2 className="w-4 h-4" />
              </a>
            </div>
          </div>
        </div>
        {canEdit && (
          <div className="flex justify-end items-center gap-3">
            {settingsMessage && <span className="text-sm text-muted-foreground">{settingsMessage}</span>}
            <button
              type="button"
              onClick={saveSettings}
              disabled={settingsSaving || !settingsDirty}
              className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-60"
            >
              Save Changes
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
