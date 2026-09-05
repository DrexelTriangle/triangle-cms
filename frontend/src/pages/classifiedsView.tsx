import { readErrorMessage } from "../lib/apiError"
import { Check, Clipboard, Mail, MessageSquare, Trash2, X } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"
import { useCurrentUserRole } from "../hooks/useCurrentUserRole"

type ClassifiedStatus = "pending" | "approved" | "rejected"
type StatusFilter = "all" | ClassifiedStatus

type Classified = {
  id: number
  name: string
  email: string
  label: string
  message: string
  end_date: string
  status: ClassifiedStatus
  decided_at?: string
  decided_by?: string
  decided_via?: string
  created_at?: string
}

type ManageResponse = {
  classifieds?: Classified[]
  counts?: Partial<Record<StatusFilter, number>>
  slack_configured?: boolean
}

const STATUS_FILTERS: StatusFilter[] = ["all", "pending", "approved", "rejected"]

const STATUS_STYLES: Record<ClassifiedStatus, string> = {
  pending: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300",
  approved: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300",
  rejected: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300",
}

function formatDate(value?: string) {
  if (!value) return "—"
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return "—"
  return parsed.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}

function formatEndDate(value: string) {
  if (!value) return "No end date"
  const parsed = new Date(`${value}T00:00:00`)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })
}

// A classified stops showing on the public site the day after its end date,
// which is worth surfacing here: approving an expired one does nothing visible.
function isExpired(value: string) {
  if (!value) return false
  const parsed = new Date(`${value}T23:59:59`)
  return !Number.isNaN(parsed.getTime()) && parsed.getTime() < Date.now()
}

export default function ClassifiedsView() {
  const apiFetch = useApiFetch()
  const { isAdmin } = useCurrentUserRole()
  const [filter, setFilter] = useState<StatusFilter>("pending")
  const [items, setItems] = useState<Classified[]>([])
  const [counts, setCounts] = useState<Partial<Record<StatusFilter, number>>>({})
  // Assume Slack works until told otherwise, so a failed load does not flash a
  // scary banner that has nothing to do with what went wrong.
  const [slackConfigured, setSlackConfigured] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await apiFetch(`/v1/classifieds/manage?status=${filter}&limit=100`)
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load classifieds (${res.status})`))
      const body = (await res.json()) as ManageResponse
      setItems(body.classifieds ?? [])
      setCounts(body.counts ?? {})
      setSlackConfigured(body.slack_configured !== false)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load classifieds.")
    } finally {
      setLoading(false)
    }
  }, [apiFetch, filter])

  useEffect(() => {
    void load()
  }, [load])

  async function setStatus(id: number, status: ClassifiedStatus) {
    setBusyId(id)
    setError(null)
    try {
      const res = await apiFetch(`/v1/classifieds/${id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not update classified (${res.status})`))
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not update classified.")
    } finally {
      setBusyId(null)
    }
  }

  async function remove(id: number) {
    if (!window.confirm("Delete this classified permanently?")) return
    setBusyId(id)
    setError(null)
    try {
      const res = await apiFetch(`/v1/classifieds/${id}`, { method: "DELETE" })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not delete classified (${res.status})`))
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete classified.")
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div>
        <h1 className="text-2xl font-bold text-foreground">Classifieds</h1>
      </div>

      {!slackConfigured && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          Slack approvals are unavailable.
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {STATUS_FILTERS.map((status) => (
          <button
            key={status}
            type="button"
            onClick={() => setFilter(status)}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium capitalize transition-colors ${
              filter === status
                ? "bg-primary text-primary-foreground"
                : "border border-border hover:bg-muted"
            }`}
          >
            {status}
            {counts[status] !== undefined && (
              <span className="ml-1.5 opacity-70">{counts[status]}</span>
            )}
          </button>
        ))}
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing here.</p>
      ) : (
        <div className="flex flex-col gap-3">
          {items.map((item) => (
            <div key={item.id} className="rounded-lg border border-border bg-card p-4 flex flex-col gap-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={`px-2 py-0.5 rounded-full text-xs font-medium capitalize ${STATUS_STYLES[item.status]}`}>
                    {item.status}
                  </span>
                  {item.label && (
                    <span className="px-2 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">
                      {item.label}
                    </span>
                  )}
                  <span className={`text-xs ${isExpired(item.end_date) ? "text-destructive" : "text-muted-foreground"}`}>
                    {isExpired(item.end_date) ? `Expired ${formatEndDate(item.end_date)}` : `Runs until ${formatEndDate(item.end_date)}`}
                  </span>
                </div>
                <span className="text-xs text-muted-foreground">Submitted {formatDate(item.created_at)}</span>
              </div>

              <p className="text-sm text-foreground whitespace-pre-wrap">{item.message}</p>

              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
                  <span className="font-medium text-foreground">{item.name}</span>
                  <a href={`mailto:${item.email}`} className="inline-flex items-center gap-1.5 hover:text-foreground">
                    <Mail className="w-3.5 h-3.5" aria-hidden="true" />
                    {item.email}
                  </a>
                  <button
                    type="button"
                    onClick={() => void navigator.clipboard?.writeText(item.email)}
                    className="inline-flex items-center gap-1.5 hover:text-foreground"
                    aria-label={`Copy ${item.email}`}
                  >
                    <Clipboard className="w-3.5 h-3.5" aria-hidden="true" />
                    Copy
                  </button>
                  {item.decided_by && (
                    <span className="inline-flex items-center gap-1.5">
                      {item.decided_via === "slack" && <MessageSquare className="w-3.5 h-3.5" aria-hidden="true" />}
                      {item.status} by {item.decided_by} {formatDate(item.decided_at)}
                    </span>
                  )}
                </div>

                <div className="flex items-center gap-2">
                  {item.status !== "approved" && (
                    <button
                      type="button"
                      onClick={() => void setStatus(item.id, "approved")}
                      disabled={busyId === item.id}
                      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
                    >
                      <Check className="w-4 h-4" aria-hidden="true" />
                      Approve
                    </button>
                  )}
                  {item.status !== "rejected" && (
                    <button
                      type="button"
                      onClick={() => void setStatus(item.id, "rejected")}
                      disabled={busyId === item.id}
                      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm font-medium hover:bg-muted disabled:opacity-60"
                    >
                      <X className="w-4 h-4" aria-hidden="true" />
                      Reject
                    </button>
                  )}
                  {isAdmin && (
                    <button
                      type="button"
                      onClick={() => void remove(item.id)}
                      disabled={busyId === item.id}
                      className="p-1.5 rounded-md text-destructive hover:bg-destructive/10 disabled:opacity-60"
                      aria-label="Delete classified"
                    >
                      <Trash2 className="w-4 h-4" aria-hidden="true" />
                    </button>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
