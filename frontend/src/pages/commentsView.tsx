import { useCallback, useEffect, useMemo, useState } from "react"
import { Check, ChevronFirst, ChevronLast, ChevronLeft, ChevronRight, ExternalLink, MessageSquare, RotateCcw, Search, Trash2, X } from "lucide-react"
import { Link } from "react-router-dom"
import { publicSiteUrl } from "../auth/urls"
import { useApiFetch } from "../hooks/useApiFetch"

type CommentStatus = "pending" | "approved" | "spam" | "trash"
type StatusFilter = "all" | CommentStatus

type Comment = {
  id: number
  article_id: number
  article_title: string
  article_slug: string
  parent_id: number
  author_name: string
  author_email?: string
  author_url?: string
  content: string
  created_at?: string
  created_at_gmt?: string
  status: CommentStatus
  type: string
}

type CommentsResponse = {
  comments?: Comment[]
  pagination?: {
    page?: number
    limit?: number
    offset?: number
    has_more?: boolean
    hasMore?: boolean
    total_count?: number
    totalCount?: number
  }
  counts?: Partial<Record<StatusFilter, number>>
}

const PAGE_SIZE = 25
const STATUS_FILTERS: StatusFilter[] = ["all", "pending", "approved", "spam", "trash"]

const STATUS_STYLES: Record<CommentStatus, string> = {
  pending: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300",
  approved: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300",
  spam: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300",
  trash: "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300",
}

function formatDate(value?: string) {
  if (!value) return "-"
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return "-"
  return parsed.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}

function initials(name: string) {
  const letters = name
    .trim()
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
  return (letters || "?").slice(0, 2).toUpperCase()
}

async function readErrorMessage(response: Response, fallback: string) {
  try {
    const body = await response.json() as { error?: string }
    return body.error ?? fallback
  } catch {
    return fallback
  }
}

function sanitizeCommentHtml(raw: string) {
  if (typeof document === "undefined") {
    return raw
  }

  const template = document.createElement("template")
  template.innerHTML = raw
  const allowedTags = new Set(["A", "BR", "P", "STRONG", "B", "EM", "I", "UL", "OL", "LI", "BLOCKQUOTE", "CODE", "PRE"])

  const walk = (node: Node) => {
    const children = Array.from(node.childNodes)
    for (const child of children) {
      if (child.nodeType !== Node.ELEMENT_NODE) {
        continue
      }

      const element = child as HTMLElement
      if (!allowedTags.has(element.tagName)) {
        element.replaceWith(...Array.from(element.childNodes))
        continue
      }

      for (const attr of Array.from(element.attributes)) {
        if (element.tagName === "A" && attr.name === "href") {
          const href = attr.value.trim()
          const isSafeHref = href.startsWith("http://") || href.startsWith("https://") || href.startsWith("mailto:")
          if (isSafeHref) continue
        }
        element.removeAttribute(attr.name)
      }

      if (element.tagName === "A") {
        element.setAttribute("target", "_blank")
        element.setAttribute("rel", "noreferrer noopener")
      }

      walk(element)
    }
  }

  walk(template.content)
  return template.innerHTML
}

function CommentBody({ content }: { content: string }) {
  const html = useMemo(() => sanitizeCommentHtml(content), [content])

  return (
    <div
      className="mt-3 text-sm leading-6 text-foreground whitespace-pre-wrap break-words [&_a]:text-primary [&_a]:underline [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

export default function CommentsView() {
  const apiFetch = useApiFetch()
  const [comments, setComments] = useState<Comment[]>([])
  const [search, setSearch] = useState("")
  const [debouncedSearch, setDebouncedSearch] = useState("")
  // Opens on approved rather than all: "all" mixes spam and trash into the
  // first thing an editor sees. Pending comments stay discoverable through the
  // count on the Comments nav item.
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("approved")
  const [page, setPage] = useState(0)
  const [totalCount, setTotalCount] = useState(0)
  const [counts, setCounts] = useState<Record<StatusFilter, number>>({
    all: 0,
    pending: 0,
    approved: 0,
    spam: 0,
    trash: 0,
  })
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [busyCommentId, setBusyCommentId] = useState<number | null>(null)
  const [selectedCommentIds, setSelectedCommentIds] = useState<Set<number>>(() => new Set())

  const loadComments = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    const params = new URLSearchParams({
      limit: String(PAGE_SIZE),
      page: String(page + 1),
    })
    if (statusFilter !== "all") {
      params.set("status", statusFilter)
    }
    if (debouncedSearch.trim()) {
      params.set("search", debouncedSearch.trim())
    }

    try {
      const response = await apiFetch(`/v1/comments?${params.toString()}`)
      if (!response.ok) {
        throw new Error(await readErrorMessage(response, `Could not load comments (${response.status})`))
      }
      const body = await response.json() as CommentsResponse
      const nextComments = body.comments ?? []
      setComments(nextComments)
      setSelectedCommentIds(new Set())
      setTotalCount(body.pagination?.total_count ?? body.pagination?.totalCount ?? nextComments.length)
      setCounts({
        all: body.counts?.all ?? nextComments.length,
        pending: body.counts?.pending ?? 0,
        approved: body.counts?.approved ?? 0,
        spam: body.counts?.spam ?? 0,
        trash: body.counts?.trash ?? 0,
      })
    } catch (err) {
      setComments([])
      setTotalCount(0)
      setError(err instanceof Error ? err.message : "Unable to load comments.")
    } finally {
      setIsLoading(false)
    }
  }, [apiFetch, debouncedSearch, page, statusFilter])

  useEffect(() => {
    const handle = window.setTimeout(() => setDebouncedSearch(search), 250)
    return () => window.clearTimeout(handle)
  }, [search])

  useEffect(() => {
    void loadComments()
  }, [loadComments])

  useEffect(() => {
    setPage(0)
  }, [debouncedSearch, statusFilter])

  const totalPages = Math.max(1, Math.ceil(Math.max(totalCount, comments.length) / PAGE_SIZE))
  const siteUrl = useMemo(() => publicSiteUrl(), [])
  const selectedCount = selectedCommentIds.size
  const visibleCommentIds = useMemo(() => comments.map((comment) => comment.id), [comments])
  const allVisibleSelected = visibleCommentIds.length > 0 && visibleCommentIds.every((id) => selectedCommentIds.has(id))

  const toggleCommentSelection = (commentId: number) => {
    setSelectedCommentIds((prev) => {
      const next = new Set(prev)
      if (next.has(commentId)) {
        next.delete(commentId)
      } else {
        next.add(commentId)
      }
      return next
    })
  }

  const toggleVisibleSelection = () => {
    setSelectedCommentIds((prev) => {
      if (allVisibleSelected) {
        return new Set([...prev].filter((id) => !visibleCommentIds.includes(id)))
      }
      return new Set([...prev, ...visibleCommentIds])
    })
  }

  const updateStatus = async (comment: Comment, status: CommentStatus) => {
    if (busyCommentId !== null) return
    setBusyCommentId(comment.id)
    setActionError(null)
    try {
      const response = await apiFetch(`/v1/comments/${comment.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response, `Update failed (${response.status})`))
      }
      await loadComments()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Unable to update comment.")
    } finally {
      setBusyCommentId(null)
    }
  }

  const updateSelectedStatus = async (status: CommentStatus) => {
    if (busyCommentId !== null || selectedCommentIds.size === 0) return
    const ids = [...selectedCommentIds]
    setBusyCommentId(-1)
    setActionError(null)
    try {
      const responses = await Promise.all(ids.map((id) => apiFetch(`/v1/comments/${id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      })))
      const failed = responses.find((response) => !response.ok)
      if (failed) {
        throw new Error(await readErrorMessage(failed, `Bulk update failed (${failed.status})`))
      }
      await loadComments()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Unable to update selected comments.")
    } finally {
      setBusyCommentId(null)
    }
  }

  const deleteComment = async (comment: Comment) => {
    if (busyCommentId !== null) return
    const shouldDelete = window.confirm(`Delete trashed comment from ${comment.author_name || "Anonymous"} forever?`)
    if (!shouldDelete) return

    setBusyCommentId(comment.id)
    setActionError(null)
    try {
      const response = await apiFetch(`/v1/comments/${comment.id}`, { method: "DELETE" })
      if (!response.ok) {
        throw new Error(await readErrorMessage(response, `Delete failed (${response.status})`))
      }
      await loadComments()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Unable to delete comment.")
    } finally {
      setBusyCommentId(null)
    }
  }

  const deleteSelectedComments = async () => {
    if (busyCommentId !== null || selectedCommentIds.size === 0) return
    const shouldDelete = window.confirm(`Delete ${selectedCommentIds.size} trashed comment${selectedCommentIds.size === 1 ? "" : "s"} forever?`)
    if (!shouldDelete) return

    const ids = [...selectedCommentIds]
    setBusyCommentId(-1)
    setActionError(null)
    try {
      const responses = await Promise.all(ids.map((id) => apiFetch(`/v1/comments/${id}`, { method: "DELETE" })))
      const failed = responses.find((response) => !response.ok)
      if (failed) {
        throw new Error(await readErrorMessage(failed, `Bulk delete failed (${failed.status})`))
      }
      await loadComments()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Unable to delete selected comments.")
    } finally {
      setBusyCommentId(null)
    }
  }

  const tabClass = (active: boolean) =>
    `px-3 py-1.5 rounded-lg text-sm font-medium border transition-colors ${
      active
        ? "bg-primary text-primary-foreground border-primary"
        : "bg-background text-muted-foreground border-border hover:text-foreground hover:bg-muted"
    }`

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-bold text-foreground">Comments</h1>
        <p className="text-sm text-muted-foreground">
          {isLoading ? "Loading..." : `${totalCount.toLocaleString()} comments${counts.pending ? `, ${counts.pending.toLocaleString()} pending` : ""}`}
        </p>
      </div>

      <div className="flex gap-3 flex-wrap items-center">
        <div className="relative flex-1 min-w-[240px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
          <input
            aria-label="Search comments"
            className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            placeholder="Search comments, authors, emails, or articles"
            type="search"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              setPage(0)
            }}
          />
        </div>
        <div className="flex items-center gap-1 flex-wrap">
          {STATUS_FILTERS.map((status) => (
            <button
              key={status}
              type="button"
              onClick={() => setStatusFilter(status)}
              className={tabClass(statusFilter === status)}
            >
              <span className="capitalize">{status}</span>
              <span className="ml-1 opacity-70">({(counts[status] ?? 0).toLocaleString()})</span>
            </button>
          ))}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3 py-2">
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          <input
            checked={allVisibleSelected}
            className="h-4 w-4 rounded border-border"
            disabled={comments.length === 0 || isLoading}
            onChange={toggleVisibleSelection}
            type="checkbox"
          />
          <span>{selectedCount ? `${selectedCount.toLocaleString()} selected` : "Select visible"}</span>
        </label>
        <div className="h-5 w-px bg-border" />
        <button
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:text-emerald-700 hover:bg-emerald-50 disabled:opacity-40 dark:hover:bg-emerald-900/20"
          disabled={selectedCount === 0 || busyCommentId !== null}
          onClick={() => void updateSelectedStatus("approved")}
          type="button"
        >
          <Check className="h-4 w-4" />
          Approve
        </button>
        <button
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground hover:bg-muted disabled:opacity-40"
          disabled={selectedCount === 0 || busyCommentId !== null}
          onClick={() => void updateSelectedStatus("pending")}
          type="button"
        >
          <RotateCcw className="h-4 w-4" />
          Pending
        </button>
        <button
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:text-red-700 hover:bg-red-50 disabled:opacity-40 dark:hover:bg-red-900/20"
          disabled={selectedCount === 0 || busyCommentId !== null}
          onClick={() => void updateSelectedStatus("spam")}
          type="button"
        >
          <X className="h-4 w-4" />
          Spam
        </button>
        <button
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:text-destructive hover:bg-destructive/10 disabled:opacity-40"
          disabled={selectedCount === 0 || busyCommentId !== null}
          onClick={() => void updateSelectedStatus("trash")}
          type="button"
        >
          <Trash2 className="h-4 w-4" />
          Trash
        </button>
        {statusFilter === "trash" ? (
          <button
            className="inline-flex items-center gap-1.5 rounded-lg border border-destructive/30 px-2.5 py-1.5 text-sm text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-40"
            disabled={selectedCount === 0 || busyCommentId !== null}
            onClick={() => void deleteSelectedComments()}
            type="button"
          >
            <Trash2 className="h-4 w-4" />
            Delete forever
          </button>
        ) : null}
      </div>

      {error ? (
        <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      ) : null}
      {actionError ? (
        <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          {actionError}
        </div>
      ) : null}

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        {isLoading ? (
          <div className="p-12 text-center text-sm text-muted-foreground">Loading comments...</div>
        ) : comments.length === 0 ? (
          <div className="p-12 text-center text-muted-foreground">
            <MessageSquare className="w-8 h-8 mx-auto mb-2 opacity-30" />
            <p>No comments found.</p>
          </div>
        ) : (
          <div className="divide-y divide-border">
            {comments.map((comment) => {
              const actionsDisabled = busyCommentId !== null
              const hasMappedArticle = comment.article_slug && comment.article_id > 0
              const articlePath = hasMappedArticle ? `/articles/${encodeURIComponent(comment.article_slug)}/edit` : ""
              const publicPath = comment.article_slug ? `${siteUrl}/article/${comment.article_slug}` : ""

              return (
                <article key={comment.id} className="grid gap-4 p-4 md:grid-cols-[minmax(0,1fr)_auto] hover:bg-muted/30 transition-colors">
                  <div className="flex gap-3 min-w-0">
                    <input
                      aria-label={`Select comment from ${comment.author_name || "Anonymous"}`}
                      checked={selectedCommentIds.has(comment.id)}
                      className="mt-3 h-4 w-4 rounded border-border shrink-0"
                      disabled={actionsDisabled}
                      onChange={() => toggleCommentSelection(comment.id)}
                      type="checkbox"
                    />
                    <div className="w-10 h-10 rounded-full bg-primary/10 text-primary flex items-center justify-center text-sm font-bold shrink-0">
                      {initials(comment.author_name)}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-semibold text-sm text-foreground">{comment.author_name || "Anonymous"}</span>
                        {comment.author_email ? <span className="text-xs text-muted-foreground">{comment.author_email}</span> : null}
                        <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase tracking-wide ${STATUS_STYLES[comment.status] ?? STATUS_STYLES.pending}`}>
                          {comment.status || "pending"}
                        </span>
                        {comment.parent_id > 0 ? <span className="text-xs text-muted-foreground">Reply to #{comment.parent_id}</span> : null}
                      </div>

                      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        {hasMappedArticle ? (
                          <Link className="font-medium text-foreground hover:text-primary" to={articlePath}>
                            {comment.article_title || `Article #${comment.article_id}`}
                          </Link>
                        ) : (
                          <span className="font-medium text-muted-foreground">Unmapped article</span>
                        )}
                        <span>{formatDate(comment.created_at_gmt ?? comment.created_at)}</span>
                        {publicPath ? (
                          <a className="inline-flex items-center gap-1 hover:text-primary" href={publicPath} rel="noreferrer" target="_blank">
                            View
                            <ExternalLink className="w-3 h-3" />
                          </a>
                        ) : null}
                      </div>

                      <CommentBody content={comment.content} />
                    </div>
                  </div>

                  <div className="flex md:flex-col gap-1 shrink-0 md:items-end">
                    {comment.status !== "approved" ? (
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-emerald-700 hover:bg-emerald-50 dark:hover:bg-emerald-900/20 transition-colors disabled:opacity-40"
                        disabled={actionsDisabled}
                        onClick={() => void updateStatus(comment, "approved")}
                        title="Approve"
                        type="button"
                      >
                        <Check className="w-4 h-4" />
                      </button>
                    ) : null}
                    {comment.status !== "pending" ? (
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground hover:bg-muted transition-colors disabled:opacity-40"
                        disabled={actionsDisabled}
                        onClick={() => void updateStatus(comment, "pending")}
                        title={comment.status === "trash" ? "Restore to pending" : "Set pending"}
                        type="button"
                      >
                        <RotateCcw className="w-4 h-4" />
                      </button>
                    ) : null}
                    {comment.status !== "spam" ? (
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors disabled:opacity-40"
                        disabled={actionsDisabled}
                        onClick={() => void updateStatus(comment, "spam")}
                        title="Mark as spam"
                        type="button"
                      >
                        <X className="w-4 h-4" />
                      </button>
                    ) : null}
                    {comment.status !== "trash" ? (
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-40"
                        disabled={actionsDisabled}
                        onClick={() => void updateStatus(comment, "trash")}
                        title="Move to trash"
                        type="button"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    ) : (
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-40"
                        disabled={actionsDisabled}
                        onClick={() => void deleteComment(comment)}
                        title="Delete forever"
                        type="button"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    )}
                  </div>
                </article>
              )
            })}
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Page {page + 1} of {totalPages}
        </p>
        <div className="flex items-center gap-1">
          <button aria-label="First page" className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground disabled:opacity-40" disabled={page === 0} onClick={() => setPage(0)} type="button">
            <ChevronFirst className="w-4 h-4" />
          </button>
          <button aria-label="Previous page" className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground disabled:opacity-40" disabled={page === 0} onClick={() => setPage((p) => Math.max(0, p - 1))} type="button">
            <ChevronLeft className="w-4 h-4" />
          </button>
          <button aria-label="Next page" className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground disabled:opacity-40" disabled={page >= totalPages - 1} onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))} type="button">
            <ChevronRight className="w-4 h-4" />
          </button>
          <button aria-label="Last page" className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground disabled:opacity-40" disabled={page >= totalPages - 1} onClick={() => setPage(totalPages - 1)} type="button">
            <ChevronLast className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
  )
}
