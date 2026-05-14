import { useState } from "react"
import { Mail, Trash2, Eye, CheckCircle, Clock, AlertCircle } from "lucide-react"

type Submission = {
  id: number
  name: string
  email: string
  subject: string
  message: string
  date: string
  status: "unread" | "read" | "replied"
}

const dummy: Submission[] = [
  { id: 1, name: "Jordan Watts", email: "jordan@example.com", subject: "Story tip: City Council meeting", message: "I attended last night's city council meeting and there was a heated discussion about the new development on 30th street that I think deserves coverage.", date: "May 7, 2026", status: "unread" },
  { id: 2, name: "Priya Sharma", email: "priya.sharma@drexel.edu", subject: "Correction request", message: "Your article from last week about the science department incorrectly listed my name. Could you please fix the byline?", date: "May 6, 2026", status: "unread" },
  { id: 3, name: "Tom Renaldo", email: "trenaldo@gmail.com", subject: "Advertising inquiry", message: "I run a local restaurant near campus and am interested in advertising rates. Please send over your media kit.", date: "May 5, 2026", status: "read" },
  { id: 4, name: "Camille Frost", email: "camille@example.com", subject: "Op-Ed submission", message: "I'd like to submit an op-ed about the recent changes to Drexel's co-op program. Where should I send it?", date: "May 4, 2026", status: "replied" },
  { id: 5, name: "Marcus Lee", email: "mlee@drexel.edu", subject: "Photo credit missing", message: "A photo I took was used in your sports section without credit. Can you update the caption?", date: "May 3, 2026", status: "replied" },
  { id: 6, name: "Anonymous", email: "anon@proton.me", subject: "Tip: Financial irregularities", message: "I have information about irregularities in the student government budget. I'd like to speak with an investigative reporter confidentially.", date: "May 2, 2026", status: "read" },
]

const STATUS_CONFIG = {
  unread: { label: "Unread", icon: AlertCircle, style: "bg-blue-100 text-blue-700" },
  read: { label: "Read", icon: Clock, style: "bg-gray-100 text-gray-600" },
  replied: { label: "Replied", icon: CheckCircle, style: "bg-green-100 text-green-700" },
}

export default function ContactView() {
  const [submissions, setSubmissions] = useState(dummy)
  const [selected, setSelected] = useState<Submission | null>(null)
  const [filter, setFilter] = useState<"all" | Submission["status"]>("all")

  const filtered = submissions.filter((s) => filter === "all" || s.status === filter)
  const unreadCount = submissions.filter((s) => s.status === "unread").length

  const markRead = (id: number) => {
    setSubmissions((prev) => prev.map((s) => s.id === id && s.status === "unread" ? { ...s, status: "read" } : s))
  }

  return (
    <div className="p-6 max-w-6xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-xl font-bold text-foreground">Contact Submissions</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {unreadCount > 0 ? <span className="text-blue-600 font-medium">{unreadCount} unread</span> : "All read"} · {submissions.length} total
          </p>
        </div>
      </div>

      <div className="flex gap-2 mb-4">
        {(["all", "unread", "read", "replied"] as const).map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium capitalize transition-colors ${filter === f ? "bg-primary text-white" : "bg-muted text-muted-foreground hover:text-foreground"}`}
          >
            {f}
          </button>
        ))}
      </div>

      <div className="flex gap-4 h-[560px]">
        {/* List */}
        <div className="w-80 shrink-0 bg-card border border-border rounded-xl overflow-hidden flex flex-col">
          <div className="overflow-y-auto flex-1 divide-y divide-border">
            {filtered.map((s) => (
              <button
                key={s.id}
                onClick={() => { setSelected(s); markRead(s.id) }}
                className={`w-full text-left px-4 py-3 hover:bg-muted/40 transition-colors ${selected?.id === s.id ? "bg-primary/5 border-l-2 border-primary" : ""}`}
              >
                <div className="flex items-start justify-between gap-2">
                  <p className={`text-sm truncate ${s.status === "unread" ? "font-semibold text-foreground" : "font-medium text-foreground"}`}>{s.name}</p>
                  <span className={`shrink-0 text-[10px] px-1.5 py-0.5 rounded-full font-medium ${STATUS_CONFIG[s.status].style}`}>{STATUS_CONFIG[s.status].label}</span>
                </div>
                <p className="text-xs text-muted-foreground truncate mt-0.5">{s.subject}</p>
                <p className="text-xs text-muted-foreground mt-0.5">{s.date}</p>
              </button>
            ))}
          </div>
        </div>

        {/* Detail */}
        {selected ? (
          <div className="flex-1 bg-card border border-border rounded-xl p-6 overflow-y-auto">
            <div className="flex items-start justify-between mb-6">
              <div>
                <h2 className="text-lg font-semibold text-foreground">{selected.subject}</h2>
                <div className="flex items-center gap-3 mt-1 text-sm text-muted-foreground">
                  <span>{selected.name}</span>
                  <span>·</span>
                  <a href={`mailto:${selected.email}`} className="text-primary hover:underline">{selected.email}</a>
                  <span>·</span>
                  <span>{selected.date}</span>
                </div>
              </div>
              <button
                onClick={() => setSubmissions((prev) => prev.filter((s) => s.id !== selected.id)) || setSelected(null)}
                className="p-2 rounded-lg hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
            <p className="text-sm text-foreground leading-relaxed whitespace-pre-wrap">{selected.message}</p>
            <div className="mt-8 flex gap-2">
              <a href={`mailto:${selected.email}?subject=Re: ${selected.subject}`} className="flex items-center gap-2 px-4 py-2 bg-primary text-white rounded-lg text-sm font-medium hover:bg-primary/90 transition-colors">
                <Mail className="w-4 h-4" />
                Reply via Email
              </a>
            </div>
          </div>
        ) : (
          <div className="flex-1 bg-card border border-border rounded-xl flex items-center justify-center text-muted-foreground text-sm">
            <div className="text-center">
              <Eye className="w-8 h-8 mx-auto mb-2 opacity-30" />
              <p>Select a submission to view</p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
