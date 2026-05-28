import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import {
  FileText,
  CheckCircle2,
  PenLine,
  Users,
  TrendingUp,
  Plus,
  ArrowUpRight,
  ArrowDownRight,
  ExternalLink,
  Activity,
  Clock,
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
  totalAuthors: number | null
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

export default function DashboardPage() {
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const [recentArticles, setRecentArticles] = useState<RecentArticle[]>([])
  const [draftTitle, setDraftTitle] = useState("")
  const [draftContent, setDraftContent] = useState("")
  const [apiHealth, setApiHealth] = useState<"ok" | "error" | "checking">("checking")
  const [stats, setStats] = useState<ApiStats>({ totalArticles: null, totalAuthors: null, loading: true })

  useEffect(() => {
    apiFetch("/v1/articles?limit=10")
      .then((r) => r.json())
      .then((d) => {
        setRecentArticles(d.articles ?? [])
        setStats((s) => ({ ...s, totalArticles: d.pagination?.totalCount ?? null, loading: false }))
      })
      .catch(() => setStats((s) => ({ ...s, loading: false })))

    apiFetch("/v1/authors?limit=1")
      .then((r) => r.json())
      .then((d: unknown) => {
        if (Array.isArray(d)) setStats((s) => ({ ...s, totalAuthors: d.length }))
      })
      .catch(() => {})

    apiFetch("/v1/articles?limit=1")
      .then((r) => (r.ok ? setApiHealth("ok") : setApiHealth("error")))
      .catch(() => setApiHealth("error"))
  }, [apiFetch])

  const published = recentArticles.filter((a) => a.status === "published")
  const drafts = recentArticles.filter((a) => a.status === "draft")

  return (
    <div className="p-6 space-y-6 w-full">
      {/* Page header */}
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-widest text-primary mb-1">
            COMMAND CENTER
          </p>
          <h1 className="text-3xl font-extrabold tracking-tight">{getHour()}, Juste.</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Here's what's happening at The Triangle today.
          </p>
        </div>
        <div className="flex gap-2.5 shrink-0">
          <Button variant="outline" size="sm" className="gap-1.5">
            <ExternalLink className="w-3.5 h-3.5" />
            View site
          </Button>
          <Button size="sm" className="gap-1.5" onClick={() => navigate("/articles")}>
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
            delta: "+12 this week",
            up: true,
          },
          {
            label: "Published",
            value: published.length,
            icon: CheckCircle2,
            color: "text-success",
            bg: "bg-success/10",
            delta: "in this view",
            up: true,
          },
          {
            label: "Drafts",
            value: drafts.length,
            icon: PenLine,
            color: "text-amber-500",
            bg: "bg-amber-50",
            delta: "need review",
            up: false,
          },
          {
            label: "Authors",
            value: stats.totalAuthors != null ? stats.totalAuthors.toLocaleString() : "—",
            icon: Users,
            color: "text-violet-500",
            bg: "bg-violet-50",
            delta: "active",
            up: true,
          },
          {
            label: "Sections",
            value: "6",
            icon: Layers,
            color: "text-sky-500",
            bg: "bg-sky-50",
            delta: "editorial",
            up: true,
          },
          {
            label: "API Status",
            value: apiHealth === "checking" ? "…" : apiHealth === "ok" ? "Healthy" : "Error",
            icon: Server,
            color: apiHealth === "ok" ? "text-success" : apiHealth === "error" ? "text-destructive" : "text-muted-foreground",
            bg: apiHealth === "ok" ? "bg-success/10" : apiHealth === "error" ? "bg-destructive/10" : "bg-muted",
            delta: "live",
            up: apiHealth === "ok",
          },
        ].map((card) => {
          const Icon = card.icon
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
                <p className={`text-xs mt-1 flex items-center gap-0.5 font-medium ${card.up ? "text-success" : "text-amber-500"}`}>
                  {card.up ? <ArrowUpRight className="w-3 h-3" /> : <ArrowDownRight className="w-3 h-3" />}
                  {card.delta}
                </p>
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

        {/* RIGHT: Quick Draft + Quick Actions + Tip */}
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
                disabled={!draftTitle.trim()}
                onClick={() => navigate("/articles")}
              >
                <Send className="w-3.5 h-3.5" />
                Save Draft
              </Button>
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
                { label: "Write new article", icon: PenLine, path: "/articles", badge: null },
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

          {/* Publishing Tip */}
          <Card className="bg-sidebar text-sidebar-foreground border-0 overflow-hidden">
            <CardContent className="p-5">
              <div className="flex items-center gap-2 mb-2.5">
                <Clock className="w-4 h-4 text-primary" />
                <p className="text-[10px] font-bold uppercase tracking-widest text-sidebar-muted">Publishing Tip</p>
              </div>
              <p className="text-sm text-white font-semibold leading-snug mb-1">
                Articles published 9–11am get 2× more engagement.
              </p>
              <p className="text-xs text-sidebar-muted">Schedule your next piece for morning.</p>
            </CardContent>
          </Card>

          {/* System info */}
          <Card>
            <CardHeader className="pb-2 pt-4 px-5">
              <CardTitle className="text-sm font-bold flex items-center gap-2">
                <Server className="w-4 h-4 text-primary" />
                System
              </CardTitle>
            </CardHeader>
            <CardContent className="px-5 pb-4 space-y-2 text-sm">
              {[
                { label: "CMS Version", value: "Delta 1.0" },
                { label: "Backend", value: "Go 1.25" },
                { label: "Database", value: "MariaDB 11.7" },
                { label: "API", value: apiHealth === "ok" ? "✓ Healthy" : apiHealth === "error" ? "✗ Unreachable" : "Checking…" },
              ].map(({ label, value }) => (
                <div key={label} className="flex items-center justify-between">
                  <span className="text-muted-foreground text-xs">{label}</span>
                  <span className="text-xs font-semibold">{value}</span>
                </div>
              ))}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  )
}
