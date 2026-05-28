import { useState } from "react"
import { Search, Check, X, Trash2, MessageSquare } from "lucide-react"

type CommentStatus = "pending" | "approved" | "spam"

const COMMENTS = [
  { id: 1, author: "John Smith", email: "john.smith@gmail.com", article: "Drexel Announces New Engineering Building", excerpt: "This is great news for the engineering department! I remember when they first proposed this...", date: "2025-04-28", status: "pending" as CommentStatus },
  { id: 2, author: "Maria Garcia", email: "mgarcia@drexel.edu", article: "Dragon Basketball Falls to Temple 78–72", excerpt: "Tough loss, but I think we showed a lot of heart. The second half comeback was exciting.", date: "2025-04-27", status: "approved" as CommentStatus },
  { id: 3, author: "Anonymous", email: "anon@mailtemp.com", article: "Opinion: The University Should Lower Tuition", excerpt: "Buy cheap online degrees at degreesforsale.net — limited time offer!!!", date: "2025-04-27", status: "spam" as CommentStatus },
  { id: 4, author: "Tyler Kowalski", email: "tkowalski@drexel.edu", article: "New Campus Health Center Opens Its Doors", excerpt: "Finally! The walk to Hahnemann was ridiculous. Hopefully the hours are better too.", date: "2025-04-26", status: "approved" as CommentStatus },
  { id: 5, author: "Rachel Kim", email: "rkim2025@drexel.edu", article: "Co-op Spotlight: Students at NASA", excerpt: "I applied to this co-op last cycle! Would love to know more about how they got the placement.", date: "2025-04-26", status: "pending" as CommentStatus },
  { id: 6, author: "Dev P", email: "devp@gmail.com", article: "Philadelphia Orchestra Partners with Drexel", excerpt: "Such an underrated partnership. The performances last semester were incredible.", date: "2025-04-25", status: "approved" as CommentStatus },
  { id: 7, author: "Sarah M", email: "sarah.m@hotmail.com", article: "Dining Hall Adds New Vegan Options", excerpt: "As a vegan student I'm so happy about this! The old options were really limited.", date: "2025-04-24", status: "pending" as CommentStatus },
  { id: 8, author: "XYZ Bot", email: "xyz@spambot.ru", article: "Drexel Ranks Among Top Research Universities", excerpt: "Click here to win $500 gift card!!! Limited time free prize!!!", date: "2025-04-23", status: "spam" as CommentStatus },
]

const STATUS_STYLES: Record<CommentStatus, string> = {
  pending: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400",
  approved: "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400",
  spam: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
}

export default function CommentsView() {
  const [comments, setComments] = useState(COMMENTS)
  const [search, setSearch] = useState("")
  const [statusFilter, setStatusFilter] = useState<CommentStatus | "all">("all")

  const filtered = comments.filter((c) => {
    const matchSearch =
      c.author.toLowerCase().includes(search.toLowerCase()) ||
      c.article.toLowerCase().includes(search.toLowerCase()) ||
      c.excerpt.toLowerCase().includes(search.toLowerCase())
    const matchStatus = statusFilter === "all" || c.status === statusFilter
    return matchSearch && matchStatus
  })

  const approve = (id: number) => setComments((prev) => prev.map((c) => c.id === id ? { ...c, status: "approved" as CommentStatus } : c))
  const spam = (id: number) => setComments((prev) => prev.map((c) => c.id === id ? { ...c, status: "spam" as CommentStatus } : c))
  const remove = (id: number) => setComments((prev) => prev.filter((c) => c.id !== id))

  const counts = {
    all: comments.length,
    pending: comments.filter((c) => c.status === "pending").length,
    approved: comments.filter((c) => c.status === "approved").length,
    spam: comments.filter((c) => c.status === "spam").length,
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Comments</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{counts.pending} pending moderation</p>
        </div>
      </div>

      <div className="flex gap-3 flex-wrap items-center">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
          <input
            className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            placeholder="Search comments..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="flex items-center gap-1">
          {(["all", "pending", "approved", "spam"] as const).map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setStatusFilter(s)}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium capitalize transition-colors ${statusFilter === s ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/80"}`}
            >
              {s} {counts[s] > 0 && <span className="ml-1 opacity-70">({counts[s]})</span>}
            </button>
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-3">
        {filtered.length === 0 ? (
          <div className="rounded-xl border border-border bg-card p-12 text-center text-muted-foreground">
            <MessageSquare className="w-8 h-8 mx-auto mb-2 opacity-30" />
            <p>No comments found.</p>
          </div>
        ) : (
          filtered.map((comment) => (
            <div key={comment.id} className={`rounded-xl border bg-card p-4 flex gap-4 ${comment.status === "pending" ? "border-yellow-300/50 dark:border-yellow-700/30" : "border-border"}`}>
              <div className="w-9 h-9 rounded-full bg-muted flex items-center justify-center text-sm font-bold text-muted-foreground shrink-0">
                {comment.author[0].toUpperCase()}
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex flex-wrap items-center gap-2 mb-1">
                  <span className="font-semibold text-sm text-foreground">{comment.author}</span>
                  <span className="text-xs text-muted-foreground">{comment.email}</span>
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase tracking-wide ${STATUS_STYLES[comment.status]}`}>
                    {comment.status}
                  </span>
                  <span className="text-xs text-muted-foreground ml-auto">{comment.date}</span>
                </div>
                <p className="text-xs text-muted-foreground mb-1">
                  On: <span className="text-foreground font-medium">{comment.article}</span>
                </p>
                <p className="text-sm text-foreground line-clamp-2">{comment.excerpt}</p>
              </div>
              <div className="flex flex-col gap-1 shrink-0">
                {comment.status !== "approved" && (
                  <button onClick={() => approve(comment.id)} className="p-1.5 rounded-lg text-muted-foreground hover:text-green-600 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors" title="Approve" type="button">
                    <Check className="w-4 h-4" />
                  </button>
                )}
                {comment.status !== "spam" && (
                  <button onClick={() => spam(comment.id)} className="p-1.5 rounded-lg text-muted-foreground hover:text-orange-500 hover:bg-orange-50 dark:hover:bg-orange-900/20 transition-colors" title="Mark spam" type="button">
                    <X className="w-4 h-4" />
                  </button>
                )}
                <button onClick={() => remove(comment.id)} className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" title="Delete" type="button">
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
