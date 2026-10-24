import { useCallback, useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Search, Plus, Trash2, Eye, Clock, CheckCircle, Copy, Pencil, Send } from "lucide-react"
import TemplateDialog from "../components/newsletter/TemplateDialog"
import { useNewsletterApi } from "../lib/newsletter/api"
import { NEWSLETTER_NAME, renderNewsletterEmail } from "../lib/newsletter/template"
import type { Newsletter, NewsletterInput, NewsletterStats, NewsletterStatus, NewsletterSubscriber } from "../lib/newsletter/types"

const STATUS_STYLES: Record<NewsletterStatus, string> = {
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400",
  sending: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
  sent: "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400",
}

const StatusIcon = ({ status }: { status: NewsletterStatus }) => {
  if (status === "sent") return <CheckCircle className="w-3 h-3" />
  if (status === "sending") return <Send className="w-3 h-3" />
  if (status === "scheduled") return <Clock className="w-3 h-3" />
  return <Eye className="w-3 h-3" />
}

const formatDate = (value: string | null) =>
  value ? new Date(value).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" }) : "—"

// The date that matters for each state: when it went, when it will go, or when
// it was last touched.
const rowDate = (n: Newsletter) =>
  n.status === "sent" ? formatDate(n.sent_at) : n.status === "scheduled" ? formatDate(n.scheduled_for) : formatDate(n.updated_at)

// The preview text, or failing that the start of the body, for the list's
// second line.
const previewOf = (n: Newsletter) => {
  if (n.preheader.trim()) return n.preheader
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${n.body_html}`, "text/html")
  return doc.body.textContent?.trim().slice(0, 160) ?? ""
}

function CountWithRate({ count, of }: { count: number; of: number }) {
  if (count <= 0 || of <= 0) return <>—</>
  return (
    <span className="text-muted-foreground">
      {count.toLocaleString()} <span className="text-xs">({Math.round((count / of) * 100)}%)</span>
    </span>
  )
}

export default function NewsletterView() {
  const navigate = useNavigate()
  const api = useNewsletterApi()
  const [tab, setTab] = useState<"campaigns" | "subscribers">("campaigns")
  const [search, setSearch] = useState("")
  const [newsletters, setNewsletters] = useState<Newsletter[]>([])
  const [subscribers, setSubscribers] = useState<NewsletterSubscriber[]>([])
  const [stats, setStats] = useState<NewsletterStats | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [templateOpen, setTemplateOpen] = useState(false)

  const load = useCallback(async () => {
    setError(null)
    try {
      const [list, subs, totals] = await Promise.all([api.list(), api.listSubscribers(), api.stats()])
      setNewsletters(list)
      setSubscribers(subs)
      setStats(totals)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load newsletters.")
    } finally {
      setIsLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const create = async (input: NewsletterInput) => {
    const created = await api.create(input)
    navigate(`/newsletter/${created.id}/edit`)
  }

  const duplicate = async (n: Newsletter) => {
    try {
      const fields = {
        subject: `Copy of ${n.subject}`,
        preheader: n.preheader,
        sponsor: n.sponsor,
        body_html: n.body_html,
      }
      // Re-rendered rather than copied: the stored email carries the old subject.
      const created = await api.create({ ...fields, email_html: renderNewsletterEmail(fields) })
      navigate(`/newsletter/${created.id}/edit`)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not duplicate the newsletter.")
    }
  }

  const remove = async (n: Newsletter) => {
    if (!window.confirm(`Delete "${n.subject}"? This can't be undone.`)) return
    try {
      await api.remove(n.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete the newsletter.")
    }
  }

  const removeSubscriber = async (s: NewsletterSubscriber) => {
    if (!window.confirm(`Remove ${s.email} from the list?`)) return
    try {
      await api.removeSubscriber(s.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not remove the subscriber.")
    }
  }

  const query = search.toLowerCase()
  const filteredNewsletters = newsletters.filter((n) => n.subject.toLowerCase().includes(query))
  const filteredSubscribers = subscribers.filter(
    (s) => s.name.toLowerCase().includes(query) || s.email.toLowerCase().includes(query),
  )

  const percent = (rate: number | null | undefined) => (rate == null ? "—" : `${Math.round(rate * 100)}%`)
  const openRate = percent(stats?.avg_open_rate)
  // Ads run for weeks, so the newest issue with a banner is the one to offer.
  const previousSponsor = newsletters.find((n) => n.sponsor?.image_url)?.sponsor ?? null

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Newsletter</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {NEWSLETTER_NAME} · {(stats?.subscriber_count ?? 0).toLocaleString()} subscribers · {openRate} avg open rate
          </p>
        </div>
        <button
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
          onClick={() => setTemplateOpen(true)}
          type="button"
        >
          <Plus className="w-4 h-4" />
          New Campaign
        </button>
      </div>

      {api.backend === "mock" ? (
        <p className="rounded-lg border border-yellow-300 bg-yellow-50 px-4 py-3 text-sm text-yellow-800 dark:border-yellow-900 dark:bg-yellow-900/20 dark:text-yellow-300">
          Preview build: campaigns are saved in this browser only, and subscribers and stats are sample data until the
          newsletter backend is connected.
        </p>
      ) : null}

      {error ? (
        <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">{error}</p>
      ) : null}

      {/* Stats */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-4">
        {[
          { label: "Total Subscribers", value: (stats?.subscriber_count ?? 0).toLocaleString() },
          { label: "Campaigns Sent", value: (stats?.sent_count ?? 0).toString() },
          { label: "Avg Open Rate", value: openRate },
          { label: "Avg Click Rate", value: percent(stats?.avg_click_rate) },
          { label: "Recipients Reached", value: (stats?.recipients_reached ?? 0).toLocaleString() },
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
          placeholder={tab === "campaigns" ? "Search campaigns" : "Search subscribers"}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      {tab === "campaigns" && (
        isLoading ? (
          <div className="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground">Loading campaigns...</div>
        ) : newsletters.length === 0 ? (
          <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border bg-card p-10 text-center">
            <p className="font-medium text-foreground">No campaigns yet</p>
            <p className="max-w-sm text-sm text-muted-foreground">
              Start the next {NEWSLETTER_NAME} issue from the template. It fills in this week's stories and the latest comic.
            </p>
            <button
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors"
              onClick={() => setTemplateOpen(true)}
              type="button"
            >
              <Plus className="w-4 h-4" />
              Create from template
            </button>
          </div>
        ) : (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/40">
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Subject</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Status</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Recipients</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Opens</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Clicks</th>
                <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Date</th>
                <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredNewsletters.map((n) => (
                <tr key={n.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <button
                      className="block text-left"
                      onClick={() => navigate(`/newsletter/${n.id}/edit`)}
                      type="button"
                    >
                      <span className="block font-medium text-foreground truncate max-w-[300px] hover:text-primary">{n.subject}</span>
                      <span className="block text-xs text-muted-foreground truncate max-w-[300px] mt-0.5">{previewOf(n)}</span>
                    </button>
                  </td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium capitalize ${STATUS_STYLES[n.status]}`}>
                      <StatusIcon status={n.status} />
                      {n.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{n.recipient_count > 0 ? n.recipient_count.toLocaleString() : "—"}</td>
                  <td className="px-4 py-3 hidden lg:table-cell">
                    <CountWithRate count={n.open_count} of={n.recipient_count} />
                  </td>
                  <td className="px-4 py-3 hidden lg:table-cell">
                    <CountWithRate count={n.click_count} of={n.recipient_count} />
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{rowDate(n)}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                        onClick={() => navigate(`/newsletter/${n.id}/edit`)}
                        title={n.status === "sent" ? "View" : "Edit"}
                        type="button"
                      >
                        {n.status === "sent" ? <Eye className="w-4 h-4" /> : <Pencil className="w-4 h-4" />}
                      </button>
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                        onClick={() => void duplicate(n)}
                        title="Duplicate"
                        type="button"
                      >
                        <Copy className="w-4 h-4" />
                      </button>
                      {n.status !== "sent" && n.status !== "sending" ? (
                        <button
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                          onClick={() => void remove(n)}
                          title="Delete"
                          type="button"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      ) : null}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        )
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
                  <td className="px-4 py-3 text-muted-foreground hidden md:table-cell">{formatDate(s.subscribed_at)}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end">
                      <button
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                        onClick={() => void removeSubscriber(s)}
                        title="Remove"
                        type="button"
                      >
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

      {templateOpen ? (
        <TemplateDialog onClose={() => setTemplateOpen(false)} onCreate={create} previousSponsor={previousSponsor} />
      ) : null}
    </div>
  )
}
