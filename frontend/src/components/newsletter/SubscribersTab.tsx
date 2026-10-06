import { Search, Trash2 } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"
import { formatDate, listNames, readErrorMessage, type NewsletterList, type Subscriber, type SubscriberStatus } from "./types"

type StatusFilter = "all" | SubscriberStatus

type SubscribersTabProps = {
  lists: NewsletterList[]
  isAdmin: boolean
  onChanged: () => void
}

const STATUS_LABEL: Record<SubscriberStatus, string> = { subscribed: "Subscribed", unsubscribed: "Unsubscribed" }
const SOURCE_LABEL: Record<string, string> = { public_form: "Signup form", admin: "Added in CMS", wordpress_import: "WordPress" }

// Subscriber management. Unsubscribing is a status change that keeps the row
// (and its unsubscribe token); only admins can erase a row outright, for
// erasure requests.
export default function SubscribersTab({ lists, isAdmin, onChanged }: SubscribersTabProps) {
  const apiFetch = useApiFetch()
  const [filter, setFilter] = useState<StatusFilter>("all")
  const [query, setQuery] = useState("")
  const [items, setItems] = useState<Subscriber[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const params = new URLSearchParams({ status: filter, limit: "100" })
      if (query.trim()) params.set("q", query.trim())
      const res = await apiFetch(`/v1/newsletter/subscribers?${params}`)
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load subscribers (${res.status})`))
      const body = (await res.json()) as { subscribers?: Subscriber[]; pagination?: { total?: number } }
      setItems(body.subscribers ?? [])
      setTotal(body.pagination?.total ?? body.subscribers?.length ?? 0)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load subscribers.")
    } finally {
      setLoading(false)
    }
  }, [apiFetch, filter, query])

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), query ? 250 : 0)
    return () => window.clearTimeout(timer)
  }, [load, query])

  async function setStatus(sub: Subscriber, status: SubscriberStatus) {
    setBusyId(sub.id)
    setError(null)
    try {
      const res = await apiFetch(`/v1/newsletter/subscribers/${sub.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not update subscriber (${res.status})`))
      const updated = (await res.json()) as Subscriber
      setItems((current) => current.map((s) => (s.id === updated.id ? updated : s)))
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not update subscriber.")
    } finally {
      setBusyId(null)
    }
  }

  async function erase(sub: Subscriber) {
    if (!window.confirm(`Erase ${sub.email} permanently? To stop mail, unsubscribe instead.`)) return
    setBusyId(sub.id)
    setError(null)
    try {
      const res = await apiFetch(`/v1/newsletter/subscribers/${sub.id}`, { method: "DELETE" })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not erase subscriber (${res.status})`))
      setItems((current) => current.filter((s) => s.id !== sub.id))
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not erase subscriber.")
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {(["all", "subscribed", "unsubscribed"] as StatusFilter[]).map((s) => (
          <button
            className={`px-3 py-1.5 rounded-lg text-sm font-medium capitalize transition-colors ${filter === s ? "bg-primary text-primary-foreground" : "border border-border hover:bg-muted"}`}
            key={s}
            onClick={() => setFilter(s)}
            type="button"
          >
            {s}
          </button>
        ))}
        <div className="relative ml-auto">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            aria-label="Search subscribers"
            className="rounded-lg border border-border bg-background py-1.5 pl-9 pr-3 text-sm"
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Email or name"
            value={query}
          />
        </div>
      </div>

      {error && <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive" role="alert">{error}</div>}

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{query || filter !== "all" ? "No matching subscribers" : "No subscribers yet"}</p>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="px-4 py-2">Email</th>
                <th className="px-4 py-2">Name</th>
                <th className="px-4 py-2">Lists</th>
                <th className="px-4 py-2">Source</th>
                <th className="px-4 py-2">Status</th>
                <th className="px-4 py-2">Since</th>
                <th className="px-4 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {items.map((sub) => (
                <tr className="border-t border-border" key={sub.id}>
                  <td className="px-4 py-2 font-medium">{sub.email}</td>
                  <td className="px-4 py-2">{sub.name || "—"}</td>
                  <td className="px-4 py-2 text-muted-foreground">{listNames(sub.list_ids ?? [], lists).join(", ") || "—"}</td>
                  <td className="px-4 py-2 text-muted-foreground">{SOURCE_LABEL[sub.source] ?? sub.source}</td>
                  <td className="px-4 py-2">{STATUS_LABEL[sub.status]}</td>
                  <td className="px-4 py-2 text-muted-foreground">{formatDate(sub.status === "unsubscribed" ? sub.unsubscribed_at : sub.subscribed_at)}</td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-2">
                      {sub.status === "subscribed" ? (
                        <button aria-label={`Unsubscribe ${sub.email}`} className="text-xs text-primary hover:underline disabled:opacity-50" disabled={busyId === sub.id} onClick={() => void setStatus(sub, "unsubscribed")} type="button">
                          Unsubscribe
                        </button>
                      ) : (
                        <button aria-label={`Resubscribe ${sub.email}`} className="text-xs text-primary hover:underline disabled:opacity-50" disabled={busyId === sub.id} onClick={() => void setStatus(sub, "subscribed")} type="button">
                          Resubscribe
                        </button>
                      )}
                      {isAdmin && (
                        <button aria-label={`Delete ${sub.email}`} className="p-1 text-destructive hover:bg-muted rounded disabled:opacity-50" disabled={busyId === sub.id} onClick={() => void erase(sub)} type="button">
                          <Trash2 className="h-4 w-4" />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {total > items.length && <p className="px-4 py-2 text-xs text-muted-foreground">Showing {items.length} of {total.toLocaleString("en-US")}. Search to narrow down.</p>}
        </div>
      )}
    </div>
  )
}
