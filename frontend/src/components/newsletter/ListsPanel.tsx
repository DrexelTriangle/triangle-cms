import { useId, useState } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"
import { readErrorMessage, type NewsletterList } from "./types"

type ListsPanelProps = {
  lists: NewsletterList[]
  isAdmin: boolean
  onChanged: () => void
}

// Lists with their subscribed counts. Admins can add a list and choose
// whether the public signup form may offer it. Lists ported from WordPress
// keep their old ids (1-999); lists made here start at 1000.
export default function ListsPanel({ lists, isAdmin, onChanged }: ListsPanelProps) {
  const apiFetch = useApiFetch()
  const nameId = useId()
  const [name, setName] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function send(url: string, method: string, body: object) {
    setBusy(true)
    setError(null)
    try {
      const res = await apiFetch(url, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not save the list (${res.status})`))
      onChanged()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save the list.")
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-label="Lists" className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
      <h2 className="text-sm font-semibold">Lists</h2>
      {lists.length === 0 ? (
        <p className="text-sm text-muted-foreground">No lists yet.</p>
      ) : (
        <ul className="flex flex-col gap-1 text-sm">
          {lists.map((list) => (
            <li className="flex items-center justify-between gap-2" key={list.id}>
              <span>
                {list.name}
                {!list.is_public && <span className="ml-2 text-xs text-muted-foreground">(not on signup form)</span>}
              </span>
              <span className="flex items-center gap-3">
                <span className="text-muted-foreground">{list.subscribed_count.toLocaleString("en-US")}</span>
                {isAdmin && (
                  <button
                    className="text-xs text-primary hover:underline disabled:opacity-50"
                    disabled={busy}
                    onClick={() => void send(`/v1/newsletter/lists/${list.id}`, "PATCH", { is_public: !list.is_public })}
                    type="button"
                  >
                    {list.is_public ? "Hide from form" : "Offer on form"}
                  </button>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
      {isAdmin && (
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            void send("/v1/newsletter/lists", "POST", { name }).then((ok) => ok && setName(""))
          }}
        >
          <div className="flex flex-1 flex-col gap-1">
            <label className="text-xs text-muted-foreground" htmlFor={nameId}>New list name</label>
            <input className="rounded-lg border border-border bg-background px-3 py-1.5 text-sm" id={nameId} maxLength={128} onChange={(e) => setName(e.target.value)} value={name} />
          </div>
          <button className="rounded-lg border border-border px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50" disabled={busy || name.trim() === ""} type="submit">
            Add list
          </button>
        </form>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
    </section>
  )
}
