import { useEffect, useId, useMemo, useState, type ReactNode } from "react"
import { AlertTriangle, ExternalLink } from "lucide-react"
import { formatRate, sentCampaigns } from "../../lib/newsletter/stats"
import type { Newsletter, NewsletterApi, NewsletterCampaignStats, NewsletterCampaignSummary } from "../../lib/newsletter/types"

// The Stats tab: one sent campaign's numbers, as the WordPress plugin's
// reports gave them, plus a comparison with the campaigns before it.
// Anything the backend doesn't know (campaigns sent before detailed tracking)
// arrives as null and is shown as unavailable, never as 0.

const formatDate = (value: string | null) =>
  value ? new Date(value).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" }) : "—"

const formatDateTime = (value: string | null) =>
  value
    ? new Date(value).toLocaleString("en-US", {
        month: "short",
        day: "numeric",
        year: "numeric",
        hour: "numeric",
        minute: "2-digit",
      })
    : "—"

const count = (n: number) => n.toLocaleString()

function Unknown() {
  return (
    <span aria-label="Not available" title="Not available">
      —
    </span>
  )
}

function Rate({ rate }: { rate: number | null }) {
  const text = formatRate(rate)
  return text == null ? <Unknown /> : <>{text}</>
}

function Metric({ label, value, detail }: { label: string; value: ReactNode; detail?: ReactNode }) {
  return (
    <div role="group" aria-label={label} className="rounded-xl border border-border bg-card p-4">
      <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{label}</p>
      <p className="text-2xl font-bold text-foreground mt-1">{value}</p>
      {detail ? <p className="text-xs text-muted-foreground mt-1">{detail}</p> : null}
    </div>
  )
}

const ofSent = (rate: number | null) => {
  const text = formatRate(rate)
  return text == null ? null : `${text} of sent`
}

function CoverageBadge({ detailed }: { detailed: boolean }) {
  return detailed ? (
    <span className="inline-flex items-center rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
      Detailed tracking
    </span>
  ) : (
    <span className="inline-flex items-center rounded-full bg-yellow-100 px-2 py-0.5 text-xs font-medium text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400">
      Partial tracking
    </span>
  )
}

const th = "text-left px-4 py-3 font-semibold text-muted-foreground whitespace-nowrap"
const thNum = "text-right px-4 py-3 font-semibold text-muted-foreground whitespace-nowrap"
const td = "px-4 py-3"
const tdNum = "px-4 py-3 text-right tabular-nums whitespace-nowrap"

function TableSection({ title, children }: { title: string; children: (headingId: string) => ReactNode }) {
  const headingId = useId()
  return (
    <section className="flex flex-col gap-2">
      <h3 id={headingId} className="text-sm font-semibold text-foreground">
        {title}
      </h3>
      <div className="rounded-xl border border-border bg-card overflow-x-auto">{children(headingId)}</div>
    </section>
  )
}

function CountAndRate({ n, rate }: { n: number; rate: number | null }) {
  const text = formatRate(rate)
  return (
    <>
      {count(n)}
      {text ? <span className="text-xs text-muted-foreground"> ({text})</span> : null}
    </>
  )
}

function RecentTable({
  rows,
  selectedId,
  onSelect,
}: {
  rows: NewsletterCampaignSummary[]
  selectedId: number
  onSelect: (id: number) => void
}) {
  return (
    <TableSection title="Recent campaigns">
      {(headingId) => (
        <table aria-labelledby={headingId} className="w-full min-w-[760px] text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className={th} scope="col">Campaign</th>
              <th className={th} scope="col">Sent</th>
              <th className={thNum} scope="col">Recipients</th>
              <th className={thNum} scope="col">Opens</th>
              <th className={thNum} scope="col">Clickers</th>
              <th className={thNum} scope="col">Click-to-open</th>
              <th className={thNum} scope="col">Clicks</th>
              <th className={thNum} scope="col">Unsubscribes</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const current = r.id === selectedId
              return (
                <tr
                  key={r.id}
                  aria-current={current ? "true" : undefined}
                  className={`border-b border-border last:border-0 ${current ? "bg-primary/5" : "hover:bg-muted/30"}`}
                >
                  <td className={td}>
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => onSelect(r.id)}
                        className={`text-left truncate max-w-[260px] hover:text-primary ${current ? "font-semibold text-primary" : "font-medium text-foreground"}`}
                      >
                        {r.subject}
                      </button>
                      {r.detailed_tracking ? null : (
                        <span className="shrink-0 rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground">
                          Partial
                        </span>
                      )}
                    </div>
                  </td>
                  <td className={`${td} whitespace-nowrap text-muted-foreground`}>{formatDate(r.sent_at)}</td>
                  <td className={tdNum}>{count(r.recipients.sent)}</td>
                  <td className={tdNum}>
                    <CountAndRate n={r.unique_opens} rate={r.open_rate} />
                  </td>
                  <td className={tdNum}>
                    <CountAndRate n={r.unique_clickers} rate={r.click_rate} />
                  </td>
                  <td className={tdNum}>
                    <Rate rate={r.click_to_open_rate} />
                  </td>
                  <td className={tdNum}>{count(r.total_clicks)}</td>
                  <td className={tdNum}>
                    {r.unsubscribes == null ? <Unknown /> : <CountAndRate n={r.unsubscribes} rate={r.unsubscribe_rate} />}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </TableSection>
  )
}

function StatsBody({ stats, onSelect }: { stats: NewsletterCampaignStats; onSelect: (id: number) => void }) {
  const r = stats.recipients
  const links = [...stats.links].sort((a, b) => b.total_clicks - a.total_clicks || a.id - b.id)
  const sendErrorsDetail = [r.skipped > 0 ? `${count(r.skipped)} skipped` : null, r.in_flight > 0 ? `${count(r.in_flight)} still sending` : null]
    .filter(Boolean)
    .join(" · ")

  return (
    <>
      <div className="flex flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-foreground">{stats.subject}</h2>
          <CoverageBadge detailed={stats.detailed_tracking} />
        </div>
        <p className="text-sm text-muted-foreground">
          {stats.status === "sending" ? "Still sending" : `Sent ${formatDateTime(stats.sent_at)}`}
        </p>
      </div>

      {stats.detailed_tracking ? null : (
        <p className="flex gap-2 rounded-lg border border-yellow-300 bg-yellow-50 px-4 py-3 text-sm text-yellow-800 dark:border-yellow-900 dark:bg-yellow-900/20 dark:text-yellow-300">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>
            Partial tracking: this campaign was sent before per-link and unsubscribe tracking, so its unsubscribes and
            each link's unique clickers are unavailable. Opens, clickers and clicks are complete.
          </span>
        </p>
      )}

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <Metric label="Recipients" value={count(r.sent)} detail={`of ${count(r.queued)} queued`} />
        <Metric label="Send errors" value={count(r.failed)} detail={sendErrorsDetail || null} />
        <Metric label="Unique opens" value={count(stats.unique_opens)} detail={ofSent(stats.open_rate)} />
        <Metric label="Unique clickers" value={count(stats.unique_clickers)} detail={ofSent(stats.click_rate)} />
        <Metric label="Click-to-open" value={<Rate rate={stats.click_to_open_rate} />} detail="of openers clicked" />
        <Metric label="Total clicks" value={count(stats.total_clicks)} detail="every click, repeats included" />
        {stats.unsubscribes == null ? (
          <Metric label="Unsubscribes" value="Unavailable" detail="Not tracked for this campaign" />
        ) : (
          <Metric label="Unsubscribes" value={count(stats.unsubscribes)} detail={ofSent(stats.unsubscribe_rate)} />
        )}
      </div>

      <TableSection title="Links">
        {(headingId) =>
          links.length === 0 ? (
            <p className="p-6 text-center text-sm text-muted-foreground">No tracked links</p>
          ) : (
            <table aria-labelledby={headingId} className="w-full min-w-[520px] text-sm">
              <thead>
                <tr className="border-b border-border bg-muted/40">
                  <th className={th} scope="col">URL</th>
                  <th className={thNum} scope="col">Total clicks</th>
                  <th className={thNum} scope="col">Unique clickers</th>
                </tr>
              </thead>
              <tbody>
                {links.map((l) => (
                  <tr key={l.id} className="border-b border-border last:border-0 hover:bg-muted/30">
                    <td className={td}>
                      <a
                        href={l.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        title={l.url}
                        className="inline-flex max-w-[min(56vw,640px)] items-center gap-1 text-foreground hover:text-primary"
                      >
                        <span className="truncate">{l.url.replace(/^https?:\/\//, "")}</span>
                        <ExternalLink className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />
                      </a>
                    </td>
                    <td className={tdNum}>{count(l.total_clicks)}</td>
                    <td className={`${tdNum} ${l.unique_clickers == null ? "text-muted-foreground" : ""}`}>
                      {l.unique_clickers == null ? "Unavailable" : count(l.unique_clickers)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </TableSection>

      {stats.errors.length > 0 ? (
        <TableSection title="Send errors">
          {(headingId) => (
            <table aria-labelledby={headingId} className="w-full min-w-[420px] text-sm">
              <thead>
                <tr className="border-b border-border bg-muted/40">
                  <th className={th} scope="col">Error</th>
                  <th className={thNum} scope="col">Copies</th>
                </tr>
              </thead>
              <tbody>
                {stats.errors.map((e) => (
                  <tr key={e.message} className="border-b border-border last:border-0">
                    <td className={`${td} break-words text-foreground`}>{e.message}</td>
                    <td className={tdNum}>{count(e.count)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </TableSection>
      ) : null}

      <RecentTable rows={stats.recent} selectedId={stats.id} onSelect={onSelect} />
    </>
  )
}

type Loaded = { id: number; stats?: NewsletterCampaignStats; error?: string }

export default function CampaignStats({ api, campaigns }: { api: NewsletterApi; campaigns: Newsletter[] }) {
  const sent = useMemo(() => sentCampaigns(campaigns), [campaigns])
  const [chosenId, setChosenId] = useState<number | null>(null)
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const selectId = useId()

  // The newest campaign until an editor picks one (or theirs disappears).
  const selectedId = sent.some((n) => n.id === chosenId) ? chosenId : (sent[0]?.id ?? null)

  useEffect(() => {
    if (selectedId == null) return
    // An answer for a campaign no longer selected is dropped.
    let current = true
    api.campaignStats(selectedId).then(
      (stats) => {
        if (current) setLoaded({ id: selectedId, stats })
      },
      (err: unknown) => {
        if (current) setLoaded({ id: selectedId, error: err instanceof Error ? err.message : "Could not load campaign stats." })
      },
    )
    return () => {
      current = false
    }
  }, [api, selectedId])

  if (selectedId == null) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed border-border bg-card p-10 text-center">
        <p className="font-medium text-foreground">No sent campaigns yet</p>
        <p className="max-w-sm text-sm text-muted-foreground">Stats appear here once a campaign has gone out.</p>
      </div>
    )
  }

  const result = loaded?.id === selectedId ? loaded : null

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1.5 sm:max-w-md">
        <label htmlFor={selectId} className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
          Campaign
        </label>
        <select
          id={selectId}
          className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary"
          value={selectedId}
          onChange={(e) => setChosenId(Number(e.target.value))}
        >
          {sent.map((n) => (
            <option key={n.id} value={n.id}>
              {n.subject} — {formatDate(n.sent_at)}
            </option>
          ))}
        </select>
      </div>

      {result?.error ? (
        <p role="alert" className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {result.error}
        </p>
      ) : result?.stats ? (
        <StatsBody stats={result.stats} onSelect={setChosenId} />
      ) : (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground" role="status">
          Loading stats...
        </div>
      )}
    </div>
  )
}
