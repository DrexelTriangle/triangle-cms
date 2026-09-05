import { readErrorMessage } from "../lib/apiError"
import { useCallback, useEffect, useMemo, useState } from "react"
import { Search, FileText, Tag, Settings, UserPlus, Users, RefreshCcw, Newspaper, ListTodo, Image, Upload, Trash2, MessageSquare } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type ActivityEvent = {
  id: string
  user: string
  action: string
  target: string
  date: string
}

type ActivityResponse = {
  events: ActivityEvent[]
  total_count: number
}

const ACTION_META: Record<string, { label: string; icon: React.ElementType; color: string }> = {
  article_created: { label: "Created article", icon: FileText, color: "text-sky-600 bg-sky-100 dark:bg-sky-900/30 dark:text-sky-400" },
  article_published: { label: "Published article", icon: Newspaper, color: "text-green-600 bg-green-100 dark:bg-green-900/30 dark:text-green-400" },
  article_updated: { label: "Updated article", icon: FileText, color: "text-blue-600 bg-blue-100 dark:bg-blue-900/30 dark:text-blue-400" },
  article_deleted: { label: "Deleted article", icon: FileText, color: "text-red-600 bg-red-100 dark:bg-red-900/30 dark:text-red-400" },
  article_restored: { label: "Restored article", icon: RefreshCcw, color: "text-emerald-600 bg-emerald-100 dark:bg-emerald-900/30 dark:text-emerald-400" },
  author_created: { label: "Created author", icon: Users, color: "text-teal-600 bg-teal-100 dark:bg-teal-900/30 dark:text-teal-400" },
  author_updated: { label: "Updated author", icon: Users, color: "text-cyan-600 bg-cyan-100 dark:bg-cyan-900/30 dark:text-cyan-400" },
  author_deleted: { label: "Deleted author", icon: Users, color: "text-rose-600 bg-rose-100 dark:bg-rose-900/30 dark:text-rose-400" },
  author_restored: { label: "Restored author", icon: RefreshCcw, color: "text-emerald-600 bg-emerald-100 dark:bg-emerald-900/30 dark:text-emerald-400" },
  user_role_updated: { label: "Updated user role", icon: UserPlus, color: "text-teal-600 bg-teal-100 dark:bg-teal-900/30 dark:text-teal-400" },
  settings_changed: { label: "Changed settings", icon: Settings, color: "text-muted-foreground bg-muted" },
  tag_created: { label: "Created tag", icon: Tag, color: "text-pink-600 bg-pink-100 dark:bg-pink-900/30 dark:text-pink-400" },
  tag_updated: { label: "Updated tag", icon: Tag, color: "text-fuchsia-600 bg-fuchsia-100 dark:bg-fuchsia-900/30 dark:text-fuchsia-400" },
  tag_deleted: { label: "Deleted tag", icon: Tag, color: "text-rose-600 bg-rose-100 dark:bg-rose-900/30 dark:text-rose-400" },
  taxonomy_created: { label: "Created taxonomy", icon: ListTodo, color: "text-purple-600 bg-purple-100 dark:bg-purple-900/30 dark:text-purple-400" },
  taxonomy_updated: { label: "Updated taxonomy", icon: ListTodo, color: "text-indigo-600 bg-indigo-100 dark:bg-indigo-900/30 dark:text-indigo-400" },
  taxonomy_deleted: { label: "Deleted taxonomy", icon: ListTodo, color: "text-red-600 bg-red-100 dark:bg-red-900/30 dark:text-red-400" },
  poll_updated: { label: "Updated poll", icon: ListTodo, color: "text-amber-600 bg-amber-100 dark:bg-amber-900/30 dark:text-amber-400" },
  developing_story_added: { label: "Added story", icon: Newspaper, color: "text-orange-600 bg-orange-100 dark:bg-orange-900/30 dark:text-orange-400" },
  developing_story_deleted: { label: "Deleted story", icon: Newspaper, color: "text-red-600 bg-red-100 dark:bg-red-900/30 dark:text-red-400" },
  media_uploaded: { label: "Uploaded media", icon: Upload, color: "text-violet-600 bg-violet-100 dark:bg-violet-900/30 dark:text-violet-400" },
  media_updated: { label: "Updated media", icon: Image, color: "text-blue-600 bg-blue-100 dark:bg-blue-900/30 dark:text-blue-400" },
  media_deleted: { label: "Deleted media", icon: Trash2, color: "text-red-600 bg-red-100 dark:bg-red-900/30 dark:text-red-400" },
  media_index_started: { label: "Reindexed media", icon: RefreshCcw, color: "text-amber-600 bg-amber-100 dark:bg-amber-900/30 dark:text-amber-400" },
  comment_status_updated: { label: "Moderated comment", icon: MessageSquare, color: "text-sky-600 bg-sky-100 dark:bg-sky-900/30 dark:text-sky-400" },
  comment_deleted: { label: "Deleted comment", icon: MessageSquare, color: "text-rose-600 bg-rose-100 dark:bg-rose-900/30 dark:text-rose-400" },
}

/**
 * Label for an action with no entry above. This used to be a fixed "Activity
 * event", which made every unmapped action render identically — an upload and
 * a delete of the same file were the same row but for the timestamp. Deriving
 * the label from the action key keeps them distinguishable, so forgetting to
 * add a mapping degrades the wording rather than losing the event's meaning.
 */
function fallbackMeta(action: string) {
  const words = action.replace(/[_-]+/g, " ").trim()
  return {
    label: words ? words.charAt(0).toUpperCase() + words.slice(1) : "Activity event",
    icon: ListTodo,
    color: "text-muted-foreground bg-muted",
  }
}

function actionMeta(action: string) {
  return ACTION_META[action] ?? fallbackMeta(action)
}

const AVATAR_COLORS = [
  "bg-blue-500", "bg-violet-500", "bg-green-500", "bg-orange-500",
  "bg-rose-500", "bg-teal-500", "bg-indigo-500", "bg-amber-500",
]

const TOP_CONTRIBUTOR_LIMIT = 5

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

function formatDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

export default function ActivityView() {
  const apiFetch = useApiFetch()
  const [search, setSearch] = useState("")
  const [events, setEvents] = useState<ActivityEvent[]>([])
  const [totalCount, setTotalCount] = useState(0)
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch("/v1/activity?limit=200")
      .then(async (r) => {
        if (!r.ok) throw new Error(await readErrorMessage(r, `Could not load activity (${r.status})`))
        return r.json() as Promise<ActivityResponse>
      })
      .then((data) => {
        setEvents(data.events ?? [])
        setTotalCount(data.total_count ?? data.events?.length ?? 0)
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Could not load activity."))
      .finally(() => setIsLoading(false))
  }, [apiFetch])

  // First-appearance order. This is what pins a person to an avatar color, so
  // it deliberately stays unsorted — reordering it would recolor the feed.
  const users = useMemo(() => [...new Set(events.map((event) => event.user || "System"))], [events])

  const avatarColor = useCallback(
    (user: string) => AVATAR_COLORS[users.indexOf(user) % AVATAR_COLORS.length],
    [users],
  )

  const filtered = useMemo(() => events.filter((event) => {
    const meta = actionMeta(event.action)
    const query = search.toLowerCase()
    return (
      (event.user || "System").toLowerCase().includes(query) ||
      event.target.toLowerCase().includes(query) ||
      meta.label.toLowerCase().includes(query)
    )
  }), [events, search])

  // Counted from the events themselves rather than by walking ACTION_META, so
  // an action with no mapping still shows up here instead of vanishing from
  // the breakdown entirely. Busiest first.
  const actionEntries = useMemo(() => {
    const counts = new Map<string, number>()
    for (const event of events) {
      counts.set(event.action, (counts.get(event.action) ?? 0) + 1)
    }
    return [...counts.entries()]
      .map(([key, count]) => ({ key, meta: actionMeta(key), count }))
      .sort((a, b) => b.count - a.count || a.meta.label.localeCompare(b.meta.label))
  }, [events])

  // Busiest first and capped, so the card ranks people instead of just
  // mirroring the order names happened to show up in the feed.
  const topContributors = useMemo(() => {
    const counts = new Map<string, number>()
    for (const event of events) {
      const user = event.user || "System"
      counts.set(user, (counts.get(user) ?? 0) + 1)
    }
    return [...counts.entries()]
      .map(([user, count]) => ({ user, count }))
      .sort((a, b) => b.count - a.count || a.user.localeCompare(b.user))
      .slice(0, TOP_CONTRIBUTOR_LIMIT)
  }, [events])

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Activity</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${totalCount} recent events`}
          </p>
        </div>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_240px] gap-6 items-start">
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
            <input
              className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              placeholder="Search activity"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            {isLoading ? (
              <div className="rounded-lg border border-border bg-card px-4 py-8 text-center text-sm text-muted-foreground">
                Loading activity...
              </div>
            ) : filtered.length === 0 ? (
              <div className="rounded-lg border border-border bg-card px-4 py-8 text-center text-sm text-muted-foreground">
                {search ? "No activity matches the current search." : "No activity recorded yet."}
              </div>
            ) : (
              filtered.map((event) => {
                const meta = actionMeta(event.action)
                const Icon = meta.icon
                const user = event.user || "System"
                return (
                  <div key={event.id} className="flex gap-4 rounded-lg border border-border bg-card px-4 py-3 hover:bg-muted/20 transition-colors">
                    <div className={`w-8 h-8 rounded-full flex items-center justify-center shrink-0 ${avatarColor(user)}`}>
                      <span className="text-white text-xs font-bold">{initials(user)}</span>
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex flex-wrap items-center gap-1.5 text-sm">
                        <span className="font-semibold text-foreground">{user}</span>
                        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${meta.color}`}>
                          <Icon className="w-3 h-3" />
                          {meta.label}
                        </span>
                      </div>
                      <p className="text-sm text-muted-foreground truncate mt-0.5">{event.target}</p>
                    </div>
                    <span className="text-xs text-muted-foreground shrink-0 pt-0.5">{formatDate(event.date)}</span>
                  </div>
                )
              })
            )}
          </div>
        </div>

        <div className="flex flex-col gap-4">
          <div className="rounded-lg border border-border bg-card p-4">
            <h3 className="text-sm font-semibold text-foreground mb-3">Most Active Users</h3>
            <div className="flex flex-col gap-2">
              {topContributors.length === 0 ? (
                <span className="text-xs text-muted-foreground">No contributors yet.</span>
              ) : (
                topContributors.map(({ user, count }) => (
                  <div key={user} className="flex items-center gap-2">
                    <div className={`w-6 h-6 rounded-full flex items-center justify-center shrink-0 ${avatarColor(user)}`}>
                      <span className="text-white text-[10px] font-bold">{initials(user)}</span>
                    </div>
                    <span className="text-sm text-foreground flex-1 truncate">{user.split(" ")[0]}</span>
                    <span className="text-xs font-semibold text-muted-foreground">{count}</span>
                  </div>
                ))
              )}
            </div>
          </div>

          <div className="rounded-lg border border-border bg-card p-4">
            <h3 className="text-sm font-semibold text-foreground mb-3">Action Counts</h3>
            <div className="flex flex-col gap-1.5">
              {actionEntries.length === 0 ? (
                <span className="text-xs text-muted-foreground">No actions recorded.</span>
              ) : (
                actionEntries.map(({ key, meta, count }) => {
                  const Icon = meta.icon
                  return (
                    <div key={key} className="flex items-center gap-2">
                      <span className={`inline-flex items-center justify-center w-5 h-5 rounded ${meta.color}`}>
                        <Icon className="w-3 h-3" />
                      </span>
                      <span className="text-xs text-muted-foreground flex-1">{meta.label}</span>
                      <span className="text-xs font-semibold text-foreground">{count}</span>
                    </div>
                  )
                })
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
