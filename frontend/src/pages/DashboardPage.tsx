import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import { publicSiteUrl } from "../auth/urls"
import {
  FileText,
  CheckCircle2,
  PenLine,
  Users,
  Plus,
  ExternalLink,
  Zap,
  Layers,
  Send,
  Server,
} from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { articleStatusChipClass } from "../lib/articleStatus"
import { readErrorMessage } from "../lib/apiError"

interface RecentArticle {
  id: number
  title: string
  slug: string
  status: "published" | "draft" | "scheduled"
  authors: { name: string }[]
  categories: { name: string }[]
  published_date: string | null
}

const editArticlePath = (article: Pick<RecentArticle, "id" | "slug">) =>
  `/articles/${encodeURIComponent(String(article.id))}/${encodeURIComponent(article.slug)}/edit`

interface ApiStats {
  totalArticles: number | null
  publishedArticles: number | null
  draftArticles: number | null
  totalAuthors: number | null
  totalSections: number | null
  loading: boolean
}

// Scheduled articles use future-relative dates.
function timeAgo(dateStr: string | null) {
  if (!dateStr) return "—"
  const target = new Date(dateStr).getTime()
  if (Number.isNaN(target)) return "—"
  const diff = Date.now() - target
  const ahead = diff < 0
  const mins = Math.floor(Math.abs(diff) / 60000)
  if (mins < 1) return "just now"
  const label =
    mins < 60
      ? `${mins}m`
      : mins < 1440
        ? `${Math.floor(mins / 60)}h`
        : `${Math.floor(mins / 1440)}d`
  return ahead ? `in ${label}` : `${label} ago`
}

function formatStatusLabel(status: string) {
  if (!status) return "—"
  return status.charAt(0).toUpperCase() + status.slice(1).toLowerCase()
}

function toCanonicalSlug(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
}

export default function DashboardPage() {
  const navigate = useNavigate()
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

  const createQuickDraft = async () => {
    const title = draftTitle.trim()
    if (!title || isSavingDraft) return

    setIsSavingDraft(true)
    setDraftError(null)

    const slug = toCanonicalSlug(title) || "draft"
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
        throw new Error(await readErrorMessage(response, `Save failed (${response.status})`))
      }

      const created = (await response.json().catch(() => null)) as { id?: number | string; slug?: string } | null
      const createdSlug = created?.slug || slug

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

      if (created?.id !== undefined && created.id !== null) {
        navigate(`/articles/${encodeURIComponent(String(created.id))}/${encodeURIComponent(createdSlug)}/edit`)
      } else {
        navigate(`/articles/${encodeURIComponent(createdSlug)}/edit`)
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unable to save draft."
      setDraftError(message)
    } finally {
      setIsSavingDraft(false)
    }
  }

  return (
    <div className="p-6 space-y-6 w-full">

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">Dashboard</h1>
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

      <div className="grid grid-cols-2 md:grid-cols-4 xl:grid-cols-6 gap-3">
        {[
          {
            label: "Articles",
            value: stats.totalArticles != null ? stats.totalArticles.toLocaleString() : "—",
            icon: FileText,
            color: "text-primary",
            bg: "bg-primary/10",
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
          },
          {
            label: "Authors",
            value: stats.totalAuthors != null ? stats.totalAuthors.toLocaleString() : "—",
            icon: Users,
            color: "text-violet-500",
            bg: "bg-violet-50",
          },
          {
            label: "Sections",
            value: stats.totalSections != null ? stats.totalSections.toLocaleString() : "—",
            icon: Layers,
            color: "text-sky-500",
            bg: "bg-sky-50",
          },
          {
            label: "API Status",
            value: apiHealth === "checking" ? "..." : apiHealth === "ok" ? "Healthy" : "Error",
            icon: Server,
            color: apiHealth === "ok" ? "text-success" : apiHealth === "error" ? "text-destructive" : "text-muted-foreground",
            bg: apiHealth === "ok" ? "bg-success/10" : apiHealth === "error" ? "bg-destructive/10" : "bg-muted",
          },
        ].map((card) => {
          const Icon = card.icon
          return (
            <Card key={card.label}>
              <CardContent className="p-4">
                <div className="flex items-center justify-between mb-3">
                  <p className="text-[11px] font-semibold uppercase tracking-normal text-muted-foreground truncate">
                    {card.label}
                  </p>
                  <span className={`flex items-center justify-center w-7 h-7 rounded-lg shrink-0 ${card.bg}`}>
                    <Icon className={`w-3.5 h-3.5 ${card.color}`} />
                  </span>
                </div>
                <p className="text-2xl font-extrabold tracking-normal">{card.value}</p>
              </CardContent>
            </Card>
          )
        })}
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-3 gap-5">

        <div className="xl:col-span-2 space-y-5">

          <Card>
            <CardHeader className="pb-2 pt-4 px-5">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-bold flex items-center gap-2">
                  <FileText className="w-4 h-4 text-primary" />
                  Recent Articles
                </CardTitle>
                <Button variant="ghost" size="sm" className="text-primary text-xs h-7 px-2" onClick={() => navigate("/articles")}>
                  Manage
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
                        onClick={() => navigate(editArticlePath(article))}
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
                          <span className={articleStatusChipClass(article.status)}>
                            {formatStatusLabel(article.status)}
                          </span>
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

        <div className="space-y-5">

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
                <label className="text-xs font-semibold text-muted-foreground mb-1 block">Notes</label>
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
                {isSavingDraft ? "Saving..." : "Save draft"}
              </Button>
              {draftError ? (
                <p className="text-xs text-destructive">{draftError}</p>
              ) : null}
            </CardContent>
          </Card>

        </div>
      </div>
    </div>
  )
}
