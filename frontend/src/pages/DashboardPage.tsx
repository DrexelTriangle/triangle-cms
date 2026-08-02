import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { publicSiteUrl } from "../auth/urls"
import { useSessionAuth } from "../auth/sessionAuthContext"
import {
  FileText,
  CheckCircle2,
  PenLine,
  Users,
  TrendingUp,
  Plus,
  ArrowUpRight,
  ArrowDownRight,
  AlertCircle,
  ExternalLink,
  Activity,
  Zap,
  Tag,
  Layers,
  Send,
  Server,
} from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"

interface RecentArticle {
  id: number
  title: string
  slug: string
  status: "published" | "draft"
  authors: { name: string }[]
  categories: { name: string }[]
  published_date: string | null
}

interface ApiStats {
  totalArticles: number | null
  publishedArticles: number | null
  draftArticles: number | null
  totalAuthors: number | null
  totalSections: number | null
  loading: boolean
}

function getHour() {
  const h = new Date().getHours()
  if (h < 12) return "Good morning"
  if (h < 18) return "Good afternoon"
  return "Good evening"
}

function timeAgo(dateStr: string | null) {
  if (!dateStr) return "—"
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

function toCanonicalSlug(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

function delay(ms: number) {
  return new Promise((resolve) => {
    setTimeout(resolve, ms)
  })
}

async function slugExists(apiFetch: (url: string, init?: RequestInit) => Promise<Response>, slug: string) {
  const response = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}`)
  return response.ok
}

export default function DashboardPage() {
  const navigate = useNavigate()
  const { user } = useSessionAuth()
  const apiFetch = useApiFetch()
  const [recentArticles, setRecentArticles] = useState<RecentArticle[]>([])
  const [draftTitle, setDraftTitle] = useState("")
  const [draftContent, setDraftContent] = useState("")
  const [isSavingDraft, setIsSavingDraft] = useState(false)
  const [draftError, setDraftError] = useState<string | null>(null)
  const [apiHealth, setApiHealth] = useState<"ok" | "error" | "checking">("checking")
  const [stats, setStats] = useState<ApiStats>({ totalArticles: null, publishedArticles: null, draftArticles: null, totalAuthors: null, totalSections: null, loading: true })

  useEffect(() => {
    apiFetch("/v1/articles?limit=10")
      .then((r) => r.json())
      .then((d) => {
        setRecentArticles(d.articles ?? [])
        setStats((s) => ({
          ...s,
          totalArticles: d.pagination?.total_count ?? d.pagination?.totalCount ?? null,
          loading: false,
        }))
      })
      .catch(() => setStats((s) => ({ ...s, loading: false })))

    apiFetch("/v1/articles?status=published&limit=1")
      .then((r) => r.json())
      .then((d) => {
        setStats((s) => ({
          ...s,
          publishedArticles: d.pagination?.total_count ?? d.pagination?.totalCount ?? null,
        }))
      })
      .catch(() => {})

    apiFetch("/v1/articles?status=draft&limit=1")
      .then((r) => r.json())
      .then((d) => {
        setStats((s) => ({
          ...s,
          draftArticles: d.pagination?.total_count ?? d.pagination?.totalCount ?? null,
        }))
      })
      .catch(() => {})

    apiFetch("/v1/authors?limit=10000")
      .then((r) => r.json())
      .then((d) => {
        setStats((s) => ({
          ...s,
          totalAuthors: Array.isArray(d) ? d.length : (d.pagination?.total_count ?? d.pagination?.totalCount ?? null),
        }))
      })
      .catch(() => {})

    apiFetch("/v1/taxonomy?type=section")
      .then(async (r) => {
        if (r.ok) return r.json()
        const fallback = await apiFetch("/v1/taxonomy")
        if (!fallback.ok) throw new Error("taxonomy unavailable")
        return fallback.json()
      })
      .then(async (d) => {
        let totalSections = Array.isArray(d)
          ? d.filter((item) => item?.type === "section").length || d.length
          : null
        if (totalSections == null || totalSections === 0) {
          const homepageRes = await apiFetch("/v1/homepage")
          if (homepageRes.ok) {
            const homepage = await homepageRes.json()
            const sectionKeys = ["news", "opinion", "sports", "entertainment", "candp", "columns"]
            totalSections = sectionKeys.filter((key) => Array.isArray(homepage?.[key])).length
          }
        }
        setStats((s) => ({
          ...s,
          totalSections,
        }))
      })
      .catch(async () => {
        try {
          const homepageRes = await apiFetch("/v1/homepage")
          if (!homepageRes.ok) return
          const homepage = await homepageRes.json()
          const sectionKeys = ["news", "opinion", "sports", "entertainment", "candp", "columns"]
          const totalSections = sectionKeys.filter((key) => Array.isArray(homepage?.[key])).length
          setStats((s) => ({ ...s, totalSections }))
        } catch {
          // Keep placeholder when both sources are unavailable.
        }
      })

    apiFetch("/v1/articles?limit=1")
      .then((r) => (r.ok ? setApiHealth("ok") : setApiHealth("error")))
      .catch(() => setApiHealth("error"))
  }, [apiFetch])
  const displayName = String(user?.name ?? user?.email ?? "Editor")

  const createQuickDraft = async () => {
    const title = draftTitle.trim()
    if (!title || isSavingDraft) return

    setIsSavingDraft(true)
    setDraftError(null)

    const baseSlug = toCanonicalSlug(title) || "draft"
    let slug = baseSlug
    try {
      for (let attempt = 0; attempt < 20; attempt += 1) {
        const candidate = attempt === 0 ? baseSlug : `${baseSlug}-${attempt + 1}`
        // Prefer the cleanest slug first, and only suffix if it's already taken.
        // If lookup errors, we still try creating with this candidate.
        const exists = await slugExists(apiFetch, candidate).catch(() => false)
        if (!exists) {
          slug = candidate
          break
        }
      }
      if (!slug) {
        slug = `${baseSlug}-${Date.now().toString(36)}`
      }
    } catch {
      slug = `${baseSlug}-${Date.now().toString(36)}`
    }
    const payload = {
      title,
      slug,
      content: draftContent.trim(),
      categories: ["Uncategorized"],
      photo_url: "",
      is_featured: false,
      status: "draft",
      comment_status: "open",
      authors: [],
    }

    try {
      const response = await apiFetch("/v1/articles", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      })
      if (!response.ok) {
        throw new Error(`Save failed (${response.status})`)
      }

      let confirmed = false
      for (let attempt = 0; attempt < 5; attempt += 1) {
        const verifyResponse = await apiFetch(`/v1/articles/${encodeURIComponent(slug)}`)
        if (verifyResponse.ok) {
          confirmed = true
          break
        }
        await delay(200)
      }
      if (!confirmed) {
        throw new Error("Draft created but not yet available. Please try again.")
      }

      if (typeof window !== "undefined") {
        const keysToDelete: string[] = []
        for (let i = 0; i < window.sessionStorage.length; i += 1) {
          const key = window.sessionStorage.key(i)
          if (key?.startsWith("articleView:")) {
            keysToDelete.push(key)
          }
        }
        for (const key of keysToDelete) {
          window.sessionStorage.removeItem(key)
        }
      }

      navigate(`/articles/${encodeURIComponent(slug)}/edit`)
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to save draft."
      setDraftError(message)
    } finally {
      setIsSavingDraft(false)
    }
  }

  return (
    <div className="p-6 space-y-6 w-full">
      {/* Page header */}
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-widest text-primary mb-1">
            COMMAND CENTER
          </p>
          <h1 className="text-3xl font-extrabold tracking-tight">{getHour()}, {displayName}.</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Here's what's happening at The Triangle today.
          </p>
        </div>
        <div className="flex gap-2.5 shrink-0">
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={() => window.open(publicSiteUrl(), "_blank", "noopener,noreferrer")}
          >
            <ExternalLink className="w-3.5 h-3.5" />
            View site
          </Button>
          <Button size="sm" className="gap-1.5" onClick={() => navigate("/articles/new")}>
            <Plus className="w-3.5 h-3.5" />
            New article
          </Button>
        </div>
      </div>

      {/* Stat strip */}
      <div className="grid grid-cols-2 md:grid-cols-4 xl:grid-cols-6 gap-3">
        {[
          {
            label: "Total Articles",
            value: stats.totalArticles != null ? stats.totalArticles.toLocaleString() : "—",
            icon: FileText,
            color: "text-primary",
            bg: "bg-primary/10",
            delta: "",
            up: true,
          },
          {
            label: "Published",
            value:
              stats.publishedArticles != null
                ? stats.publishedArticles.toLocaleString()
                : "—",
            icon: CheckCircle2,
            color: "text-success",
            bg: "bg-success/10",
            delta: "",
            up: true,
          },
          {
            label: "Drafts",
            value:
              stats.draftArticles != null
                ? stats.draftArticles.toLocaleString()
                : "—",
            icon: PenLine,
            color: "text-amber-500",
            bg: "bg-amber-50",
            delta: "need review",
            up: (stats.draftArticles ?? 0) === 0,
            deltaIcon: AlertCircle,
          },
          {
            label: "Authors",
            value: stats.totalAuthors != null ? stats.totalAuthors.toLocaleString() : "—",
            icon: Users,
            color: "text-violet-500",
            bg: "bg-violet-50",
            delta: "",
            up: true,
          },
          {
            label: "Sections",
            value: stats.totalSections != null ? stats.totalSections.toLocaleString() : "—",
            icon: Layers,
            color: "text-sky-500",
            bg: "bg-sky-50",
            delta: "",
            up: true,
          },
          {
            label: "API Status",
            value: apiHealth === "checking" ? "…" : apiHealth === "ok" ? "Healthy" : "Error",
            icon: Server,
            color: apiHealth === "ok" ? "text-success" : apiHealth === "error" ? "text-destructive" : "text-muted-foreground",
            bg: apiHealth === "ok" ? "bg-success/10" : apiHealth === "error" ? "bg-destructive/10" : "bg-muted",
            delta: apiHealth === "ok" ? "up" : "down",
            up: apiHealth === "ok",
          },
        ].map((card) => {
          const Icon = card.icon
          const DeltaIcon = card.deltaIcon
          return (
            <Card key={card.label} className="hover:shadow-md transition-shadow">
              <CardContent className="p-4">
                <div className="flex items-center justify-between mb-3">
                  <p className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground truncate">
                    {card.label}
                  </p>
                  <span className={`flex items-center justify-center w-7 h-7 rounded-lg shrink-0 ${card.bg}`}>
                    <Icon className={`w-3.5 h-3.5 ${card.color}`} />
                  </span>
                </div>
                <p className="text-2xl font-extrabold tracking-tight">{card.value}</p>
                {card.delta ? (
                  <p className={`text-xs mt-1 flex items-center gap-0.5 font-medium ${card.up ? "text-success" : "text-destructive"}`}>
                    {DeltaIcon ? <DeltaIcon className="w-3 h-3" /> : card.up ? <ArrowUpRight className="w-3 h-3" /> : <ArrowDownRight className="w-3 h-3" />}
                    {card.delta}
                  </p>
                ) : null}
              </CardContent>
            </Card>
          )
        })}
      </div>

      {/* Main 3-column grid */}
      <div className="grid grid-cols-1 xl:grid-cols-3 gap-5">

        {/* LEFT: At a Glance + Activity */}
        <div className="xl:col-span-2 space-y-5">



          {/* All recent articles full table */}
          <Card>
            <CardHeader className="pb-2 pt-4 px-5">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-bold flex items-center gap-2">
                  <FileText className="w-4 h-4 text-primary" />
                  All Recent Articles
                </CardTitle>
                <Button variant="ghost" size="sm" className="text-primary text-xs h-7 px-2" onClick={() => navigate("/articles")}>
                  Manage →
                </Button>
              </div>
            </CardHeader>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border bg-muted/40">
                      <th className="text-left px-5 py-2.5 text-xs font-semibold text-muted-foreground">Title</th>
                      <th className="text-left px-3 py-2.5 text-xs font-semibold text-muted-foreground hidden sm:table-cell">Author</th>
                      <th className="text-left px-3 py-2.5 text-xs font-semibold text-muted-foreground hidden md:table-cell">Category</th>
                      <th className="text-left px-3 py-2.5 text-xs font-semibold text-muted-foreground">Status</th>
                      <th className="text-right px-5 py-2.5 text-xs font-semibold text-muted-foreground hidden lg:table-cell">Date</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {recentArticles.map((article) => (
                      <tr
                        key={article.id}
                        className="hover:bg-muted/40 cursor-pointer transition-colors group"
                        onClick={() => navigate(`/articles/${article.slug}/edit`)}
                      >
                        <td className="px-5 py-2.5 font-medium group-hover:text-primary transition-colors max-w-[240px] truncate">
                          {article.title}
                        </td>
                        <td className="px-3 py-2.5 text-muted-foreground hidden sm:table-cell">
                          {article.authors[0]?.name ?? "—"}
                        </td>
                        <td className="px-3 py-2.5 text-muted-foreground hidden md:table-cell">
                          {article.categories[0]?.name ?? "—"}
                        </td>
                        <td className="px-3 py-2.5">
                          <Badge variant={article.status === "published" ? "success" : "secondary"} className="text-[11px]">
                            {article.status}
                          </Badge>
                        </td>
                        <td className="px-5 py-2.5 text-xs text-muted-foreground text-right hidden lg:table-cell">
                          {timeAgo(article.published_date)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>
        </div>

        {/* RIGHT: Quick Draft + Quick Actions */}
        <div className="space-y-5">

          {/* Quick Draft */}
          <Card>
            <CardHeader className="pb-2 pt-4 px-5">
              <CardTitle className="text-sm font-bold flex items-center gap-2">
                <Zap className="w-4 h-4 text-primary" />
                Quick Draft
              </CardTitle>
            </CardHeader>
            <CardContent className="px-5 pb-4 space-y-3">
              <div>
                <label className="text-xs font-semibold text-muted-foreground mb-1 block">Title</label>
                <input
                  placeholder="Article headline..."
                  value={draftTitle}
                  onChange={(e) => setDraftTitle(e.target.value)}
                  className="w-full h-9 px-3 text-sm rounded-lg border border-input bg-background placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>
              <div>
                <label className="text-xs font-semibold text-muted-foreground mb-1 block">What's on your mind?</label>
                <textarea
                  placeholder="Start writing..."
                  value={draftContent}
                  onChange={(e) => setDraftContent(e.target.value)}
                  rows={4}
                  className="w-full px-3 py-2 text-sm rounded-lg border border-input bg-background placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring resize-none"
                />
              </div>
              <Button
                size="sm"
                className="w-full gap-1.5"
                disabled={!draftTitle.trim() || isSavingDraft}
                onClick={() => void createQuickDraft()}
              >
                <Send className="w-3.5 h-3.5" />
                {isSavingDraft ? "Saving..." : "Save Draft"}
              </Button>
              {draftError ? (
                <p className="text-xs text-destructive">{draftError}</p>
              ) : null}
            </CardContent>
          </Card>

          {/* Quick Actions */}
          <Card>
            <CardHeader className="pb-2 pt-4 px-5">
              <CardTitle className="text-sm font-bold flex items-center gap-2">
                <Activity className="w-4 h-4 text-primary" />
                Quick Actions
              </CardTitle>
            </CardHeader>
            <CardContent className="px-3 pb-3 space-y-1">
              {[
                { label: "Write new article", icon: PenLine, path: "/articles/new", badge: null },
                { label: "Manage authors", icon: Users, path: "/authors", badge: null },
                { label: "Developing stories", icon: TrendingUp, path: "/developing-stories", badge: "Live" },
                { label: "Upload media", icon: FileText, path: "/media", badge: null },
                { label: "Browse sections", icon: Layers, path: "/sections", badge: null },
                { label: "SEO overview", icon: Tag, path: "/seo", badge: null },
              ].map(({ label, icon: Icon, path, badge }) => (
                <button
                  key={label}
                  type="button"
                  onClick={() => navigate(path)}
                  className="w-full flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium hover:bg-muted transition-colors text-left"
                >
                  <span className="flex items-center justify-center w-7 h-7 rounded-lg bg-primary/10 shrink-0">
                    <Icon className="w-3.5 h-3.5 text-primary" />
                  </span>
                  <span className="flex-1">{label}</span>
                  {badge && (
                    <span className="text-[10px] font-bold px-1.5 py-0.5 rounded-full bg-success/10 text-success">
                      {badge}
                    </span>
                  )}
                </button>
              ))}
            </CardContent>
          </Card>

        </div>
      </div>
    </div>
  )
}
