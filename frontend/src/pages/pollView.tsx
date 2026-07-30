import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import { Pencil, Plus, Trash2, RefreshCw, Check, X, Play, Archive } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

type PollStatus = "draft" | "active" | "closed"

type PollOption = {
  id: number
  option: string
  votes: number
  percentage: number
}

type Poll = {
  id: number
  question: string
  status: PollStatus
  starts_at?: string
  ends_at?: string
  total_votes: number
  options: PollOption[]
}

type PollListResponse = {
  polls?: Poll[]
}

const STATUS_STYLES: Record<PollStatus, string> = {
  active: "bg-emerald-500/10 text-emerald-600 border-emerald-500/30",
  draft: "bg-amber-500/10 text-amber-600 border-amber-500/30",
  closed: "bg-muted text-muted-foreground border-border",
}

// The API speaks RFC3339; <input type="datetime-local"> speaks "YYYY-MM-DDTHH:mm"
// with no zone. Slicing the local ISO string is what keeps a date the editor
// picked from shifting by the UTC offset on the way to the form and back.
function toLocalInput(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  const offsetMs = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16)
}

function formatRunDate(value?: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}

export default function PollView() {
  const apiFetch = useApiFetch()
  const [polls, setPolls] = useState<Poll[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [isCreating, setIsCreating] = useState(false)
  const [newQuestion, setNewQuestion] = useState("")
  const [newOptions, setNewOptions] = useState("")
  const [newStartsAt, setNewStartsAt] = useState("")
  const [newEndsAt, setNewEndsAt] = useState("")
  const [newActivate, setNewActivate] = useState(false)

  const [editingPollId, setEditingPollId] = useState<number | null>(null)
  const [editQuestion, setEditQuestion] = useState("")
  const [editStartsAt, setEditStartsAt] = useState("")
  const [editEndsAt, setEditEndsAt] = useState("")

  const [addingOptionFor, setAddingOptionFor] = useState<number | null>(null)
  const [newOptionName, setNewOptionName] = useState("")
  const [renamingOptionId, setRenamingOptionId] = useState<number | null>(null)
  const [renameValue, setRenameValue] = useState("")

  const activePoll = useMemo(() => polls.find((poll) => poll.status === "active"), [polls])

  const loadPolls = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/polls/manage")
      if (!res.ok) throw new Error(`Failed loading polls (${res.status})`)
      const body = (await res.json()) as PollListResponse
      setPolls(body.polls ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load polls")
    } finally {
      setIsLoading(false)
    }
  }, [apiFetch])

  useEffect(() => {
    void loadPolls()
  }, [loadPolls])

  // Every mutation funnels through here so a failure surfaces one way and the
  // list is always refetched rather than patched optimistically -- activating a
  // poll changes another poll's status server-side, which local state can't know.
  const mutate = useCallback(
    async (path: string, init: RequestInit, failure: string) => {
      setIsSaving(true)
      setError(null)
      try {
        const res = await apiFetch(path, init)
        if (!res.ok) {
          let detail = ""
          try {
            const body = (await res.json()) as { error?: string }
            detail = body?.error ? `: ${body.error}` : ""
          } catch {
            detail = ""
          }
          throw new Error(`${failure} (${res.status})${detail}`)
        }
        await loadPolls()
        return true
      } catch (err) {
        setError(err instanceof Error ? err.message : failure)
        return false
      } finally {
        setIsSaving(false)
      }
    },
    [apiFetch, loadPolls],
  )

  const createPoll = async (e: FormEvent) => {
    e.preventDefault()
    const question = newQuestion.trim()
    if (!question) return

    const options = newOptions
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)

    const ok = await mutate(
      "/v1/polls",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          question,
          options,
          status: newActivate ? "active" : "draft",
          starts_at: newStartsAt || null,
          ends_at: newEndsAt || null,
        }),
      },
      "Failed to create poll",
    )

    if (ok) {
      setNewQuestion("")
      setNewOptions("")
      setNewStartsAt("")
      setNewEndsAt("")
      setNewActivate(false)
      setIsCreating(false)
    }
  }

  const savePollEdits = async (pollId: number) => {
    const question = editQuestion.trim()
    if (!question) return
    const ok = await mutate(
      `/v1/polls/${pollId}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          question,
          // Explicit null clears the date; omitting it would leave it unchanged.
          starts_at: editStartsAt || null,
          ends_at: editEndsAt || null,
        }),
      },
      "Failed to update poll",
    )
    if (ok) setEditingPollId(null)
  }

  const setStatus = (pollId: number, status: PollStatus) =>
    mutate(
      `/v1/polls/${pollId}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      },
      "Failed to change poll status",
    )

  const deletePoll = (poll: Poll) => {
    if (
      !confirm(
        `Delete "${poll.question}"?\n\nThis permanently removes the poll and its ${poll.total_votes} recorded votes. It will disappear from the public archive.`,
      )
    ) {
      return
    }
    void mutate(`/v1/polls/${poll.id}`, { method: "DELETE" }, "Failed to delete poll")
  }

  const addOption = async (e: FormEvent, pollId: number) => {
    e.preventDefault()
    const option = newOptionName.trim()
    if (!option) return
    const ok = await mutate(
      `/v1/polls/${pollId}/options`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ option }),
      },
      "Failed to add poll option",
    )
    if (ok) {
      setNewOptionName("")
      setAddingOptionFor(null)
    }
  }

  const renameOption = async (e: FormEvent, pollId: number, optionId: number) => {
    e.preventDefault()
    const option = renameValue.trim()
    if (!option) return
    const ok = await mutate(
      `/v1/polls/${pollId}/options/${optionId}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ option }),
      },
      "Failed to rename poll option",
    )
    if (ok) setRenamingOptionId(null)
  }

  const deleteOption = (pollId: number, option: PollOption) => {
    if (!confirm(`Delete option "${option.option}" and its ${option.votes} votes?`)) return
    void mutate(
      `/v1/polls/${pollId}/options/${option.id}`,
      { method: "DELETE" },
      "Failed to delete poll option",
    )
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Polls</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading
              ? "Loading..."
              : `${polls.length} poll${polls.length === 1 ? "" : "s"} • ${
                  activePoll ? `running: ${activePoll.question}` : "none running"
                }`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={loadPolls}
            disabled={isLoading || isSaving}
            className="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border text-sm text-foreground hover:bg-muted/40 disabled:opacity-60"
          >
            <RefreshCw className="w-4 h-4" />
            Refresh
          </button>
          <button
            type="button"
            onClick={() => setIsCreating((open) => !open)}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90"
          >
            <Plus className="w-4 h-4" />
            New poll
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 text-destructive text-sm px-3 py-2">
          {error}
        </div>
      )}

      {isCreating && (
        <form onSubmit={createPoll} className="rounded-xl border border-border bg-card p-4 flex flex-col gap-4">
          <h2 className="font-semibold text-foreground">New poll</h2>
          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium text-foreground">Question</label>
            <input
              value={newQuestion}
              onChange={(e) => setNewQuestion(e.target.value)}
              placeholder="Where do you stream music?"
              maxLength={255}
              className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium text-foreground">Options</label>
            <textarea
              value={newOptions}
              onChange={(e) => setNewOptions(e.target.value)}
              placeholder={"Spotify\nApple Music\nYoutube Music"}
              rows={4}
              className="px-3 py-2 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-primary/40"
            />
            <p className="text-xs text-muted-foreground">One option per line. You can add more later.</p>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-foreground">Start date</label>
              <input
                type="datetime-local"
                value={newStartsAt}
                onChange={(e) => setNewStartsAt(e.target.value)}
                className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-foreground">End date</label>
              <input
                type="datetime-local"
                value={newEndsAt}
                onChange={(e) => setNewEndsAt(e.target.value)}
                className="px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
              />
              <p className="text-xs text-muted-foreground">Leave blank for no expiry.</p>
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-foreground">
            <input type="checkbox" checked={newActivate} onChange={(e) => setNewActivate(e.target.checked)} />
            Publish immediately
            {activePoll && newActivate && (
              <span className="text-xs text-amber-600">(this will close "{activePoll.question}")</span>
            )}
          </label>
          <div className="flex items-center gap-2">
            <button
              type="submit"
              disabled={isSaving || !newQuestion.trim()}
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
            >
              Create poll
            </button>
            <button
              type="button"
              onClick={() => setIsCreating(false)}
              className="px-4 py-2 rounded-lg border border-border text-sm hover:bg-muted/40"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {isLoading ? (
        <div className="rounded-xl border border-border bg-card px-4 py-12 text-center text-muted-foreground">
          Loading polls...
        </div>
      ) : polls.length === 0 ? (
        <div className="rounded-xl border border-border bg-card px-4 py-12 text-center text-muted-foreground">
          No polls yet. Create one to get started.
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          {polls.map((poll) => (
            <div key={poll.id} className="rounded-xl border border-border bg-card overflow-hidden">
              <div className="flex items-start justify-between gap-4 px-4 py-3 border-b border-border">
                <div className="min-w-0 flex-1">
                  {editingPollId === poll.id ? (
                    <div className="flex flex-col gap-3">
                      <input
                        value={editQuestion}
                        onChange={(e) => setEditQuestion(e.target.value)}
                        maxLength={255}
                        autoFocus
                        className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                      />
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                          Start date
                          <input
                            type="datetime-local"
                            value={editStartsAt}
                            onChange={(e) => setEditStartsAt(e.target.value)}
                            className="px-2 py-1.5 rounded-md border border-border bg-background text-sm text-foreground"
                          />
                        </label>
                        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
                          End date (blank = no expiry)
                          <input
                            type="datetime-local"
                            value={editEndsAt}
                            onChange={(e) => setEditEndsAt(e.target.value)}
                            className="px-2 py-1.5 rounded-md border border-border bg-background text-sm text-foreground"
                          />
                        </label>
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => savePollEdits(poll.id)}
                          disabled={isSaving || !editQuestion.trim()}
                          className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md bg-primary text-primary-foreground disabled:opacity-60"
                        >
                          <Check className="w-3.5 h-3.5" />
                          Save
                        </button>
                        <button
                          type="button"
                          onClick={() => setEditingPollId(null)}
                          className="inline-flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border border-border"
                        >
                          <X className="w-3.5 h-3.5" />
                          Cancel
                        </button>
                      </div>
                    </div>
                  ) : (
                    <>
                      <div className="flex items-center gap-2 flex-wrap">
                        <h2 className="font-semibold text-foreground">{poll.question}</h2>
                        <span
                          className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full border ${STATUS_STYLES[poll.status]}`}
                        >
                          {poll.status}
                        </span>
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">
                        {poll.total_votes.toLocaleString()} vote{poll.total_votes === 1 ? "" : "s"}
                        {poll.starts_at && ` • Start: ${formatRunDate(poll.starts_at)}`}
                        {` • End: ${poll.ends_at ? formatRunDate(poll.ends_at) : "No Expiry"}`}
                      </p>
                    </>
                  )}
                </div>

                {editingPollId !== poll.id && (
                  <div className="flex items-center gap-1 shrink-0">
                    {poll.status !== "active" && (
                      <button
                        type="button"
                        onClick={() => setStatus(poll.id, "active")}
                        disabled={isSaving}
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-emerald-600 hover:bg-emerald-500/10 transition-colors"
                        title="Make this the running poll"
                      >
                        <Play className="w-4 h-4" />
                      </button>
                    )}
                    {poll.status === "active" && (
                      <button
                        type="button"
                        onClick={() => setStatus(poll.id, "closed")}
                        disabled={isSaving}
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                        title="Close this poll"
                      >
                        <Archive className="w-4 h-4" />
                      </button>
                    )}
                    <button
                      type="button"
                      onClick={() => {
                        setEditingPollId(poll.id)
                        setEditQuestion(poll.question)
                        setEditStartsAt(toLocalInput(poll.starts_at))
                        setEditEndsAt(toLocalInput(poll.ends_at))
                      }}
                      className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                      title="Edit question and dates"
                    >
                      <Pencil className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => deletePoll(poll)}
                      className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                      title="Delete poll"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                )}
              </div>

              <div className="px-4 py-3 flex flex-col gap-2">
                {poll.options.length === 0 ? (
                  <p className="text-sm text-muted-foreground">No options yet.</p>
                ) : (
                  poll.options.map((option) => (
                    <div key={option.id} className="flex items-center gap-3">
                      {renamingOptionId === option.id ? (
                        <form onSubmit={(e) => renameOption(e, poll.id, option.id)} className="flex items-center gap-2 flex-1">
                          <input
                            value={renameValue}
                            onChange={(e) => setRenameValue(e.target.value)}
                            autoFocus
                            maxLength={128}
                            className="flex-1 px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                          />
                          <button type="submit" className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground">
                            Save
                          </button>
                          <button
                            type="button"
                            onClick={() => setRenamingOptionId(null)}
                            className="text-xs px-2 py-1 rounded border border-border"
                          >
                            Cancel
                          </button>
                        </form>
                      ) : (
                        <>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-baseline justify-between gap-2">
                              <span className="text-sm text-foreground truncate">{option.option}</span>
                              <span className="text-xs text-muted-foreground shrink-0">
                                {option.votes.toLocaleString()} ({option.percentage}%)
                              </span>
                            </div>
                            <div className="mt-1 h-1.5 rounded-full bg-muted overflow-hidden">
                              <div className="h-full bg-primary/60" style={{ width: `${option.percentage}%` }} />
                            </div>
                          </div>
                          <div className="flex items-center gap-1 shrink-0">
                            <button
                              type="button"
                              onClick={() => {
                                setRenamingOptionId(option.id)
                                setRenameValue(option.option)
                              }}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors"
                              title="Rename option"
                            >
                              <Pencil className="w-3.5 h-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => deleteOption(poll.id, option)}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                              title="Delete option"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </button>
                          </div>
                        </>
                      )}
                    </div>
                  ))
                )}

                {addingOptionFor === poll.id ? (
                  <form onSubmit={(e) => addOption(e, poll.id)} className="flex items-center gap-2 mt-1">
                    <input
                      value={newOptionName}
                      onChange={(e) => setNewOptionName(e.target.value)}
                      autoFocus
                      maxLength={128}
                      placeholder="New option"
                      className="flex-1 px-2 py-1.5 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40"
                    />
                    <button type="submit" className="text-xs px-3 py-1.5 rounded bg-primary text-primary-foreground">
                      Add
                    </button>
                    <button
                      type="button"
                      onClick={() => setAddingOptionFor(null)}
                      className="text-xs px-3 py-1.5 rounded border border-border"
                    >
                      Cancel
                    </button>
                  </form>
                ) : (
                  <button
                    type="button"
                    onClick={() => {
                      setAddingOptionFor(poll.id)
                      setNewOptionName("")
                    }}
                    className="self-start inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-primary mt-1"
                  >
                    <Plus className="w-3.5 h-3.5" />
                    Add option
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
