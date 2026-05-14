import { useState } from "react"
import { Search, FileText, Image, Tag, Users, Settings, MessageSquare, UserPlus } from "lucide-react"

type ActionType = "article_published" | "article_updated" | "article_deleted" | "media_uploaded" | "user_added" | "comment_approved" | "settings_changed" | "tag_created"

const ACTIVITY = [
  { id: 1, user: "Paulie Loscalzo", action: "article_published" as ActionType, target: "Dragon Baseball Wins Conference Championship", date: "2025-04-29 14:32" },
  { id: 2, user: "Sarah Goldstein", action: "article_updated" as ActionType, target: "Opinion: University Parking Policy Needs Reform", date: "2025-04-29 13:15" },
  { id: 3, user: "Zarina Morgan", action: "article_published" as ActionType, target: "New Student Center Breaks Ground on Race Street", date: "2025-04-29 11:48" },
  { id: 4, user: "Juste Mugisha", action: "user_added" as ActionType, target: "Alex Rivera (Subscriber)", date: "2025-04-29 10:20" },
  { id: 5, user: "Marcus Chen", action: "media_uploaded" as ActionType, target: "campus-aerial-2025.jpg", date: "2025-04-28 16:55" },
  { id: 6, user: "Sarah Goldstein", action: "comment_approved" as ActionType, target: "Comment by Tyler Kowalski on 'New Campus Health Center Opens'", date: "2025-04-28 15:30" },
  { id: 7, user: "Sanjana Bandi", action: "article_published" as ActionType, target: "Co-op Spotlight: Students at NASA Goddard", date: "2025-04-28 14:12" },
  { id: 8, user: "Juste Mugisha", action: "settings_changed" as ActionType, target: "Site tagline updated", date: "2025-04-28 11:05" },
  { id: 9, user: "Nina Feinberg", action: "article_updated" as ActionType, target: "Philadelphia Orchestra Partners with Drexel Music", date: "2025-04-28 10:30" },
  { id: 10, user: "Coco Li", action: "tag_created" as ActionType, target: "sustainability", date: "2025-04-27 16:40" },
  { id: 11, user: "Erik Heyman-Meltzer", action: "article_published" as ActionType, target: "Dining Hall Expands Vegan and Halal Options", date: "2025-04-27 13:22" },
  { id: 12, user: "Marcus Chen", action: "media_uploaded" as ActionType, target: "basketball-game-4.jpg", date: "2025-04-27 12:10" },
  { id: 13, user: "Sarah Goldstein", action: "article_deleted" as ActionType, target: "Draft: Interview with Dean [UNPUBLISHED]", date: "2025-04-27 09:45" },
  { id: 14, user: "Jenna Pedorenko", action: "article_published" as ActionType, target: "Drexel Research Team Develops New Water Filter", date: "2025-04-26 15:00" },
  { id: 15, user: "Ava Buckingham", action: "media_uploaded" as ActionType, target: "student-event-april.jpg", date: "2025-04-26 11:30" },
  { id: 16, user: "Sophie Fang", action: "article_updated" as ActionType, target: "Arts & Entertainment Preview: Spring Gallery Show", date: "2025-04-25 14:00" },
  { id: 17, user: "Paulie Loscalzo", action: "article_published" as ActionType, target: "Drexel Men's Soccer Eyes Playoff Spot", date: "2025-04-25 12:30" },
  { id: 18, user: "Juste Mugisha", action: "user_added" as ActionType, target: "Jordan Lee (Contributor)", date: "2025-04-24 10:15" },
  { id: 19, user: "Zarina Morgan", action: "tag_created" as ActionType, target: "co-op", date: "2025-04-24 09:00" },
  { id: 20, user: "Sarah Goldstein", action: "comment_approved" as ActionType, target: "Comment by Rachel Kim on 'Co-op Spotlight'", date: "2025-04-23 16:45" },
]

const ACTION_META: Record<ActionType, { label: string; icon: React.ElementType; color: string }> = {
  article_published: { label: "Published article", icon: FileText, color: "text-green-600 bg-green-100 dark:bg-green-900/30 dark:text-green-400" },
  article_updated: { label: "Updated article", icon: FileText, color: "text-blue-600 bg-blue-100 dark:bg-blue-900/30 dark:text-blue-400" },
  article_deleted: { label: "Deleted article", icon: FileText, color: "text-red-600 bg-red-100 dark:bg-red-900/30 dark:text-red-400" },
  media_uploaded: { label: "Uploaded media", icon: Image, color: "text-violet-600 bg-violet-100 dark:bg-violet-900/30 dark:text-violet-400" },
  user_added: { label: "Added user", icon: UserPlus, color: "text-teal-600 bg-teal-100 dark:bg-teal-900/30 dark:text-teal-400" },
  comment_approved: { label: "Approved comment", icon: MessageSquare, color: "text-orange-600 bg-orange-100 dark:bg-orange-900/30 dark:text-orange-400" },
  settings_changed: { label: "Changed settings", icon: Settings, color: "text-muted-foreground bg-muted" },
  tag_created: { label: "Created tag", icon: Tag, color: "text-pink-600 bg-pink-100 dark:bg-pink-900/30 dark:text-pink-400" },
}

const AVATAR_COLORS = [
  "bg-blue-500", "bg-violet-500", "bg-green-500", "bg-orange-500",
  "bg-rose-500", "bg-teal-500", "bg-indigo-500", "bg-amber-500",
]

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

export default function ActivityView() {
  const [search, setSearch] = useState("")

  const users = [...new Set(ACTIVITY.map((a) => a.user))]

  const filtered = ACTIVITY.filter((a) =>
    a.user.toLowerCase().includes(search.toLowerCase()) ||
    a.target.toLowerCase().includes(search.toLowerCase()) ||
    ACTION_META[a.action].label.toLowerCase().includes(search.toLowerCase())
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Activity Log</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{ACTIVITY.length} recent events</p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_240px] gap-6 items-start">
        <div className="flex flex-col gap-4">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
            <input
              className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
              placeholder="Search activity..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            {filtered.map((event, i) => {
              const meta = ACTION_META[event.action]
              const Icon = meta.icon
              const userIdx = users.indexOf(event.user)
              return (
                <div key={event.id} className="flex gap-4 rounded-xl border border-border bg-card px-4 py-3 hover:bg-muted/20 transition-colors">
                  <div className={`w-8 h-8 rounded-full flex items-center justify-center shrink-0 ${AVATAR_COLORS[userIdx % AVATAR_COLORS.length]}`}>
                    <span className="text-white text-xs font-bold">{initials(event.user)}</span>
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex flex-wrap items-center gap-1.5 text-sm">
                      <span className="font-semibold text-foreground">{event.user}</span>
                      <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${meta.color}`}>
                        <Icon className="w-3 h-3" />
                        {meta.label}
                      </span>
                    </div>
                    <p className="text-sm text-muted-foreground truncate mt-0.5">{event.target}</p>
                  </div>
                  <span className="text-xs text-muted-foreground shrink-0 pt-0.5">{event.date}</span>
                </div>
              )
            })}
          </div>
        </div>

        {/* Summary sidebar */}
        <div className="flex flex-col gap-4">
          <div className="rounded-xl border border-border bg-card p-4">
            <h3 className="text-sm font-semibold text-foreground mb-3">Top Contributors</h3>
            <div className="flex flex-col gap-2">
              {users.map((user, i) => {
                const count = ACTIVITY.filter((a) => a.user === user).length
                return (
                  <div key={user} className="flex items-center gap-2">
                    <div className={`w-6 h-6 rounded-full flex items-center justify-center shrink-0 ${AVATAR_COLORS[i % AVATAR_COLORS.length]}`}>
                      <span className="text-white text-[10px] font-bold">{initials(user)}</span>
                    </div>
                    <span className="text-sm text-foreground flex-1 truncate">{user.split(" ")[0]}</span>
                    <span className="text-xs font-semibold text-muted-foreground">{count}</span>
                  </div>
                )
              })}
            </div>
          </div>

          <div className="rounded-xl border border-border bg-card p-4">
            <h3 className="text-sm font-semibold text-foreground mb-3">Actions Breakdown</h3>
            <div className="flex flex-col gap-1.5">
              {(Object.entries(ACTION_META) as [ActionType, typeof ACTION_META[ActionType]][]).map(([key, meta]) => {
                const count = ACTIVITY.filter((a) => a.action === key).length
                if (count === 0) return null
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
              })}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
