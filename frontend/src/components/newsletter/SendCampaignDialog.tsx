import { useEffect, useState } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"
import Dialog from "./Dialog"
import { listNames, readErrorMessage, type Campaign, type NewsletterList } from "./types"

type SendCampaignDialogProps = {
  campaign: Campaign
  lists: NewsletterList[]
  onClose: () => void
}

// The confirmation step a real send will go through: who it would reach,
// counted by the server exactly as the send will count them. Delivery does not
// exist yet, so the confirm button stays disabled and nothing is called.
export default function SendCampaignDialog({ campaign, lists, onClose }: SendCampaignDialogProps) {
  const apiFetch = useApiFetch()
  const [count, setCount] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const res = await apiFetch(`/v1/newsletter/campaigns/${campaign.id}/recipients/count`)
        if (!res.ok) throw new Error(await readErrorMessage(res, `Could not count recipients (${res.status})`))
        const body = (await res.json()) as { count: number }
        if (!cancelled) setCount(body.count)
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Could not count recipients.")
      }
    })()
    return () => {
      cancelled = true
    }
  }, [apiFetch, campaign.id])

  const names = listNames(campaign.list_ids, lists)
  let reach: string
  if (names.length === 0) reach = "No lists are selected, so nobody would receive it."
  else if (count === null) reach = "Counting recipients…"
  else reach = `Would reach ${count.toLocaleString("en-US")} ${count === 1 ? "subscriber" : "subscribers"} in ${names.join(", ")}.`

  return (
    <Dialog onClose={onClose} title="Send campaign">
      <p className="text-sm font-medium">{campaign.subject}</p>
      {error ? <p className="text-sm text-destructive">{error}</p> : <p className="text-sm">{reach}</p>}
      <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
        <p className="font-medium">Delivery isn't built yet.</p>
        <p>Nothing will be sent from here. The newsletter still goes out through WordPress.</p>
      </div>
      <div className="flex justify-end gap-2">
        <button className="rounded-lg border border-border px-4 py-2 text-sm hover:bg-muted" onClick={onClose} type="button">Cancel</button>
        <button className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50" disabled type="button">
          Send
        </button>
      </div>
    </Dialog>
  )
}
