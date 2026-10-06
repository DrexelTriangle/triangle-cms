import { Pencil, Plus, Send, Trash2 } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import CampaignEditor from "../components/newsletter/CampaignEditor"
import ListsPanel from "../components/newsletter/ListsPanel"
import SendCampaignDialog from "../components/newsletter/SendCampaignDialog"
import SubscribersTab from "../components/newsletter/SubscribersTab"
import { formatDate, listNames, readErrorMessage, type Campaign, type CampaignStatus, type NewsletterStats } from "../components/newsletter/types"
import { useApiFetch } from "../hooks/useApiFetch"
import { useCurrentUserRole } from "../hooks/useCurrentUserRole"

type Tab = "campaigns" | "subscribers"

const STATUS_STYLES: Record<CampaignStatus, string> = {
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400",
  sent: "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400",
}

// Only numbers the server actually has. There is no open-rate or reach tile:
// nothing tracks opens, and nothing has been sent from here yet.
function StatTile({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{label}</p>
      <p className="mt-1 text-2xl font-bold text-foreground">{value.toLocaleString("en-US")}</p>
    </div>
  )
}

export default function NewsletterView() {
  const apiFetch = useApiFetch()
  const { isAdmin } = useCurrentUserRole()
  const [tab, setTab] = useState<Tab>("campaigns")
  const [stats, setStats] = useState<NewsletterStats | null>(null)
  const [campaigns, setCampaigns] = useState<Campaign[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [editing, setEditing] = useState<Campaign | "new" | null>(null)
  const [sending, setSending] = useState<Campaign | null>(null)

  const loadStats = useCallback(async () => {
    const res = await apiFetch("/v1/newsletter/stats")
    if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load the newsletter (${res.status})`))
    setStats((await res.json()) as NewsletterStats)
  }, [apiFetch])

  const loadCampaigns = useCallback(async () => {
    const res = await apiFetch("/v1/newsletter/campaigns?status=all&limit=100")
    if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load campaigns (${res.status})`))
    const body = (await res.json()) as { campaigns?: Campaign[] }
    setCampaigns(body.campaigns ?? [])
  }, [apiFetch])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      await Promise.all([loadStats(), loadCampaigns()])
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load the newsletter.")
    } finally {
      setLoading(false)
    }
  }, [loadCampaigns, loadStats])

  useEffect(() => {
    void load()
  }, [load])

  const refreshStats = useCallback(() => {
    loadStats().catch((err: unknown) => setActionError(err instanceof Error ? err.message : "Could not refresh stats."))
  }, [loadStats])

  async function openEditor(campaign: Campaign) {
    setActionError(null)
    try {
      // The list endpoint omits bodies; fetch the full draft.
      const res = await apiFetch(`/v1/newsletter/campaigns/${campaign.id}`)
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not open the campaign (${res.status})`))
      setEditing((await res.json()) as Campaign)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Could not open the campaign.")
    }
  }

  async function deleteCampaign(campaign: Campaign) {
    if (!window.confirm(`Delete the draft "${campaign.subject}"?`)) return
    setActionError(null)
    try {
      const res = await apiFetch(`/v1/newsletter/campaigns/${campaign.id}`, { method: "DELETE" })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not delete the campaign (${res.status})`))
      await load()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Could not delete the campaign.")
    }
  }

  const lists = stats?.lists ?? []

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Newsletter</h1>
          <p className="mt-0.5 text-sm text-muted-foreground">Draft campaigns and manage subscribers. Sending still happens in WordPress.</p>
        </div>
        {!error && !loading && (
          <div className="flex flex-col items-end gap-1">
            <button
              className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
              disabled={lists.length === 0}
              onClick={() => setEditing("new")}
              type="button"
            >
              <Plus className="h-4 w-4" />
              New campaign
            </button>
            {lists.length === 0 && <p className="text-xs text-muted-foreground">Create a list first</p>}
          </div>
        )}
      </div>

      {error ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive" role="alert">{error}</div>
      ) : loading || !stats ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <>
          <section aria-label="Newsletter stats" className="grid grid-cols-2 gap-4 md:grid-cols-4">
            <StatTile label="Subscribed" value={stats.subscribers.subscribed ?? 0} />
            <StatTile label="Unsubscribed" value={stats.subscribers.unsubscribed ?? 0} />
            <StatTile label="Drafts" value={stats.campaigns.draft ?? 0} />
            <StatTile label="Sent" value={stats.campaigns.sent ?? 0} />
          </section>

          {actionError && (
            <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">{actionError}</div>
          )}

          <div className="grid gap-6 lg:grid-cols-[1fr_18rem]">
            <div className="flex flex-col gap-4">
              <div className="flex gap-1 border-b border-border">
                {(["campaigns", "subscribers"] as Tab[]).map((t) => (
                  <button
                    className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors ${tab === t ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground"}`}
                    key={t}
                    onClick={() => setTab(t)}
                    type="button"
                  >
                    {t === "campaigns" ? "Campaigns" : "Subscribers"}
                  </button>
                ))}
              </div>

              {tab === "subscribers" ? (
                <SubscribersTab isAdmin={isAdmin} lists={lists} onChanged={refreshStats} />
              ) : campaigns.length === 0 ? (
                <p className="text-sm text-muted-foreground">No campaigns yet</p>
              ) : (
                <div className="overflow-x-auto rounded-xl border border-border bg-card">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-border bg-muted/40 text-left text-muted-foreground">
                        <th className="px-4 py-3 font-semibold" scope="col">Subject</th>
                        <th className="hidden px-4 py-3 font-semibold md:table-cell" scope="col">Lists</th>
                        <th className="px-4 py-3 font-semibold" scope="col">Status</th>
                        <th className="hidden px-4 py-3 font-semibold md:table-cell" scope="col">Date</th>
                        <th className="px-4 py-3 text-right font-semibold" scope="col">Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {campaigns.map((c) => (
                        <tr className="border-b border-border last:border-0 hover:bg-muted/30" key={c.id}>
                          <td className="px-4 py-3">
                            <p className="max-w-[300px] truncate font-medium text-foreground">{c.subject}</p>
                            {c.preview_text && <p className="mt-0.5 max-w-[300px] truncate text-xs text-muted-foreground">{c.preview_text}</p>}
                          </td>
                          <td className="hidden px-4 py-3 text-muted-foreground md:table-cell">{listNames(c.list_ids ?? [], lists).join(", ") || "—"}</td>
                          <td className="px-4 py-3">
                            <span className={`rounded-full px-2 py-0.5 text-xs font-medium capitalize ${STATUS_STYLES[c.status]}`}>{c.status}</span>
                            {c.status === "sent" && c.recipient_count !== undefined && (
                              <span className="ml-2 text-xs text-muted-foreground">to {c.recipient_count.toLocaleString("en-US")}</span>
                            )}
                          </td>
                          <td className="hidden px-4 py-3 text-muted-foreground md:table-cell">{formatDate(c.status === "sent" ? c.sent_at : c.updated_at)}</td>
                          <td className="px-4 py-3">
                            {c.status === "draft" && (
                              <div className="flex items-center justify-end gap-1">
                                <button aria-label={`Edit ${c.subject}`} className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => void openEditor(c)} type="button">
                                  <Pencil className="h-4 w-4" />
                                </button>
                                <button aria-label={`Send ${c.subject}`} className="rounded-lg p-1.5 text-muted-foreground hover:bg-primary/10 hover:text-primary" onClick={() => setSending(c)} type="button">
                                  <Send className="h-4 w-4" />
                                </button>
                                {isAdmin && (
                                  <button aria-label={`Delete ${c.subject}`} className="rounded-lg p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive" onClick={() => void deleteCampaign(c)} type="button">
                                    <Trash2 className="h-4 w-4" />
                                  </button>
                                )}
                              </div>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
            <ListsPanel isAdmin={isAdmin} lists={lists} onChanged={refreshStats} />
          </div>
        </>
      )}

      {editing && (
        <CampaignEditor
          campaign={editing === "new" ? null : editing}
          lists={lists}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
        />
      )}
      {sending && <SendCampaignDialog campaign={sending} lists={lists} onClose={() => setSending(null)} />}
    </div>
  )
}
