import { useState } from "react"
import { Search, Plus, Send, Trash2, Eye, Clock, CheckCircle } from "lucide-react"

type CampaignStatus = "draft" | "scheduled" | "sent"

const CAMPAIGNS = [
  { id: 1, subject: "The Triangle Weekly Digest — April 28, 2025", preview: "This week: Engineering building approved, basketball season recap, and your co-op spotlight...", status: "sent" as CampaignStatus, recipients: 4821, opens: 1923, date: "2025-04-28" },
  { id: 2, subject: "The Triangle Weekly Digest — April 21, 2025", preview: "Spring events, new student center groundbreaking, and an interview with the new provost...", status: "sent" as CampaignStatus, recipients: 4791, opens: 1876, date: "2025-04-21" },
  { id: 3, subject: "The Triangle Weekly Digest — April 14, 2025", preview: "Campus construction update, arts week preview, and top stories from last week...", status: "sent" as CampaignStatus, recipients: 4754, opens: 1802, date: "2025-04-14" },
  { id: 4, subject: "The Triangle Weekly Digest — May 5, 2025", preview: "Finals week coverage, end of year awards, and summer co-op previews...", status: "scheduled" as CampaignStatus, recipients: 0, opens: 0, date: "2025-05-05" },
  { id: 5, subject: "Special Edition: Commencement 2025", preview: "Everything you need to know about graduation weekend, speaker announcement, and alumni stories...", status: "draft" as CampaignStatus, recipients: 0, opens: 0, date: "" },
]

const SUBSCRIBERS = [
  { id: 1, email: "jsmith@drexel.edu", name: "John Smith", date: "2024-09-03" },
  { id: 2, email: "mgarcia@drexel.edu", name: "Maria Garcia", date: "2024-09-05" },
  { id: 3, email: "t.kowalski@gmail.com", name: "Tyler Kowalski", date: "2024-10-12" },
  { id: 4, email: "rkim2025@drexel.edu", name: "Rachel Kim", date: "2024-10-20" },
  { id: 5, email: "dev.p@gmail.com", name: "Dev P", date: "2025-01-08" },
]

const STATUS_STYLES: Record<CampaignStatus, string> = {
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400",
  sent: "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400",
}

const StatusIcon = ({ status }: { status: CampaignStatus }) => {
  if (status === "sent") return <CheckCircle className="w-3 h-3" />
  if (status === "scheduled") return <Clock className="w-3 h-3" />
  return <Eye className="w-3 h-3" />
}

export default function NewsletterView() {
  const [tab, setTab] = useState<"campaigns" | "subscribers">("campaigns")
  const [search, setSearch] = useState("")

  const filteredCampaigns = CAMPAIGNS.filter((c) =>
    c.subject.toLowerCase().includes(search.toLowerCase())
  )
  const filteredSubscribers = SUBSCRIBERS.filter((s) =>
    s.name.toLowerCase().includes(search.toLowerCase()) ||
    s.email.toLowerCase().includes(search.toLowerCase())
  )

  const totalSent = CAMPAIGNS.filter((c) => c.status === "sent").reduce((sum, c) => sum + c.recipients, 0)
  const avgOpenRate = Math.round(CAMPAIGNS.filter((c) => c.status === "sent" && c.recipients > 0).reduce((sum, c) => sum + c.opens / c.recipients, 0) / CAMPAIGNS.filter((c) => c.status === "sent").length * 100)

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Newsletter</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{SUBSCRIBERS.length.toLocaleString()} subscribers · {avgOpenRate}% avg open rate</p>
        </div>
        <button className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
          <Plus className="w-4 h-4" />
          New Campaign
        </button>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[
          { label: "Total Subscribers", value: SUBSCRIBERS.length.toLocaleString() },
          { label: "Campaigns Sent", value: CAMPAIGNS.filter((c) => c.status === "sent").length.toString() },
          { label: "Avg Open Rate", value: `${avgOpenRate}%` },
          { label: "Recipients Reached", value: totalSent.toLocaleString() },
        ].map((stat) => (
          <div key={stat.label} className="rounded-xl border border-border bg-card p-4">
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{stat.label}</p>
            <p className="text-2xl font-bold text-foreground mt-1">{stat.value}</p>
          </div>
        ))}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {(["campaigns", "subscribers"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium capitalize transition-colors border-b-2 -mb-px ${tab === t ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}
          >
            {t}
          </button>
        ))}
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          placeholder={tab === "campaigns" ? "Search campaigns..." : "Search subscribers..."}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      {tab === "campaigns" && (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/40">
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Subject</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Status</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Recipients</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Opens</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Date</th>
                <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredCampaigns.map((c) => (
                <tr key={c.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <p className="font-medium text-foreground truncate max-w-[300px]">{c.subject}</p>
                    <p className="text-xs text-muted-foreground truncate max-w-[300px] mt-0.5">{c.preview}</p>
                  </td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium capitalize ${STATUS_STYLES[c.status]}`}>
                      <StatusIcon status={c.status} />
                      {c.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{c.recipients > 0 ? c.recipients.toLocaleString() : "—"}</td>
                  <td className="px-4 py-3 hidden lg:table-cell">
                    {c.opens > 0 ? (
                      <span className="text-muted-foreground">{c.opens.toLocaleString()} <span className="text-xs">({Math.round(c.opens / c.recipients * 100)}%)</span></span>
                    ) : "—"}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{c.date || "—"}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      {c.status === "draft" && (
                        <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Send">
                          <Send className="w-4 h-4" />
                        </button>
                      )}
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete">
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {tab === "subscribers" && (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/40">
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Email</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Subscribed</th>
                <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredSubscribers.map((s) => (
                <tr key={s.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3 font-medium text-foreground">{s.name}</td>
                  <td className="px-4 py-3 text-muted-foreground">{s.email}</td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{s.date}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end">
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Remove">
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
