import { useDashboardData, type RecentArticle } from "../hooks/useDashboardData"
import { slugify } from "../lib/slugify"
import { clearArticleListCache } from "../lib/articleCache"
import { useState } from "react"
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

const editArticlePath = (article: Pick<RecentArticle, "id" | "slug">) =>
  `/articles/${encodeURIComponent(String(article.id))}/${encodeURIComponent(article.slug)}/edit`

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

export default function DashboardPage() {
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const { recentArticles, stats, apiHealth } = useDashboardData()
  const [draftTitle, setDraftTitle] = useState("")
  const [draftContent, setDraftContent] = useState("")
  const [isSavingDraft, setIsSavingDraft] = useState(false)
  const [draftError, setDraftError] = useState<string | null>(null)

  const createQuickDraft = async () => {
    const title = draftTitle.trim()
    if (!title || isSavingDraft) return

    setIsSavingDraft(true)
    setDraftError(null)

    const slug = slugify(title) || "draft"
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

      clearArticleListCache("all")

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
