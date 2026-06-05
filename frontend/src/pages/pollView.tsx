import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import { Pencil, Plus, Trash2, RefreshCw } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type PollCountsResponse = {
  counts?: Record<string, number>
}

type PollOptionsResponse = {
  options?: string[]
}
type PollTitleResponse = {
  title?: string
}

type PollRow = {
  option: string
  votes: number
}

async function readJson<T>(res: Response): Promise<T> {
  return (await res.json()) as T
}

export default function PollView() {
  const apiFetch = useApiFetch()
  const [options, setOptions] = useState<string[]>([])
  const [counts, setCounts] = useState<Record<string, number>>({})
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [newOption, setNewOption] = useState("")
  const [pollTitle, setPollTitle] = useState("")
  const [pollTitleDraft, setPollTitleDraft] = useState("")
  const [renamingOption, setRenamingOption] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [isSaving, setIsSaving] = useState(false)

  const totalVotes = useMemo(() => Object.values(counts).reduce((sum, n) => sum + n, 0), [counts])

  const rows: PollRow[] = useMemo(() => {
    return options.map((option) => ({ option, votes: counts[option] ?? 0 }))
  }, [options, counts])

  const loadPoll = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    try {
      const [titleRes, optionsRes, countsRes] = await Promise.all([
        apiFetch("/v1/poll/title"),
        apiFetch("/v1/poll/options"),
        apiFetch("/v1/poll"),
      ])

      if (!titleRes.ok) throw new Error(`Failed loading poll title (${titleRes.status})`)
      if (!optionsRes.ok) throw new Error(`Failed loading poll options (${optionsRes.status})`)
      if (!countsRes.ok) throw new Error(`Failed loading poll counts (${countsRes.status})`)

      const titleBody = await readJson<PollTitleResponse>(titleRes)
      const optionsBody = await readJson<PollOptionsResponse>(optionsRes)
      const countsBody = await readJson<PollCountsResponse>(countsRes)

      const nextTitle = String(titleBody.title ?? "").trim()
      setPollTitle(nextTitle)
      setPollTitleDraft(nextTitle)
      setOptions(optionsBody.options ?? [])
      setCounts(countsBody.counts ?? {})
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load poll data")
    } finally {
      setIsLoading(false)
    }
  }, [apiFetch])

  const savePollTitle = async () => {
    const title = pollTitleDraft.trim()
    if (!title) return
    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/poll/title", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title }),
      })
      if (!res.ok) throw new Error(`Failed to update poll title (${res.status})`)
      setPollTitle(title)
      setPollTitleDraft(title)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update poll title")
    } finally {
      setIsSaving(false)
    }
  }

  useEffect(() => {
    const handle = window.setTimeout(() => {
      void loadPoll()
    }, 0)
    return () => window.clearTimeout(handle)
  }, [loadPoll])

  const addOption = async (e: FormEvent) => {
    e.preventDefault()
    const option = newOption.trim()
    if (!option) return

    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/poll/options", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ option }),
      })
      if (!res.ok) throw new Error(`Failed to add poll option (${res.status})`)
      setNewOption("")
      await loadPoll()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to add poll option")
    } finally {
      setIsSaving(false)
    }
  }

  const saveRename = async (e: FormEvent) => {
    e.preventDefault()
    if (!renamingOption) return

    const newName = renameValue.trim()
    if (!newName || newName === renamingOption) {
      setRenamingOption(null)
      setRenameValue("")
      return
    }

    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/poll/options", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ old_option: renamingOption, new_option: newName }),
      })
      if (!res.ok) throw new Error(`Failed to rename poll option (${res.status})`)
      setRenamingOption(null)
      setRenameValue("")
      await loadPoll()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to rename poll option")
    } finally {
      setIsSaving(false)
    }
  }

  const deleteOption = async (option: string) => {
    if (!confirm(`Delete poll option "${option}"?`)) return

    setIsSaving(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/poll/options", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ option }),
      })
      if (!res.ok) throw new Error(`Failed to delete poll option (${res.status})`)
      await loadPoll()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete poll option")
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Poll</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading..." : `${rows.length} options • ${totalVotes} total votes`}
          </p>
        </div>
        <button
          type="button"
          onClick={loadPoll}
          disabled={isLoading || isSaving}
          className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm text-foreground hover:bg-muted/40 disabled:opacity-60"
        >
          <RefreshCw className="w-4 h-4" />
          Refresh
        </button>
      </div>

      <form onSubmit={addOption} className="flex items-center gap-2">
        <input
          value={pollTitleDraft}
          onChange={(e) => setPollTitleDraft(e.target.value)}
          placeholder="Poll title"
          className="w-full max-w-xl px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
          maxLength={200}
        />
        <button
          type="button"
          onClick={savePollTitle}
          disabled={isSaving || !pollTitleDraft.trim() || pollTitleDraft.trim() === pollTitle}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-border text-sm text-foreground hover:bg-muted/40 disabled:opacity-60"
        >
          Save Title
        </button>
      </form>

      <form onSubmit={addOption} className="flex items-center gap-2">
        <input
          value={newOption}
          onChange={(e) => setNewOption(e.target.value)}
          placeholder="Add a new poll option"
          className="w-full max-w-md px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
          maxLength={128}
        />
        <button
          type="submit"
          disabled={isSaving || !newOption.trim()}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
        >
          <Plus className="w-4 h-4" />
          Add
        </button>
      </form>

      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 text-destructive text-sm px-3 py-2">
          {error}
        </div>
      )}

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Option</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground">Votes</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={3}>Loading poll data...</td>
              </tr>
            ) : rows.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={3}>No poll options found.</td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr key={row.option} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    {renamingOption === row.option ? (
                      <form onSubmit={saveRename} className="flex items-center gap-2">
                        <input
                          value={renameValue}
                          onChange={(e) => setRenameValue(e.target.value)}
                          autoFocus
                          maxLength={128}
                          className="w-full max-w-md px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                        />
                        <button type="submit" className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground">Save</button>
                        <button
                          type="button"
                          onClick={() => {
                            setRenamingOption(null)
                            setRenameValue("")
                          }}
                          className="text-xs px-2 py-1 rounded border border-border"
                        >
                          Cancel
                        </button>
                      </form>
                    ) : (
                      <span className="font-medium text-foreground">{row.option}</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{row.votes.toLocaleString()}</td>
                  <td className="px-4 py-3">
                    {renamingOption !== row.option && (
                      <div className="flex items-center justify-end gap-1">
                        <button
                          type="button"
                          onClick={() => {
                            setRenamingOption(row.option)
                            setRenameValue(row.option)
                          }}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                          title="Rename"
                        >
                          <Pencil className="w-4 h-4" />
                        </button>
                        <button
                          type="button"
                          onClick={() => deleteOption(row.option)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                          title="Delete"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
