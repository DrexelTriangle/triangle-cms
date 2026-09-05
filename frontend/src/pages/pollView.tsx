import { readErrorMessage } from "../lib/apiError"
import { useCallback, useEffect, useMemo, useState } from "react"
import type { FormEvent } from "react"
import { Pencil, Plus, Trash2, RefreshCw, Check, X, Play, Archive } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"
import { DateTimeField } from "../components/ui/datetime-field"

type PollStatus = "draft" | "active" | "closed"

// What the poll is doing right now, computed server-side from its status and
// its date window. The editor displays this rather than status so it can never
// claim a poll is running when readers cannot see it.
type PollState = "draft" | "scheduled" | "live" | "ended" | "superseded" | "closed"

type PublishTiming = "draft" | "now" | "schedule"

const TIMING_OPTIONS: { value: PublishTiming; label: string; blurb: string }[] = [
  { value: "draft", label: "Save as draft", blurb: "Nobody sees it until you publish." },
  { value: "now", label: "Publish now", blurb: "Goes on the site as soon as you save." },
  { value: "schedule", label: "Schedule", blurb: "Goes on the site at the start date." },
]

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
  state: PollState
  starts_at?: string
  ends_at?: string
  total_votes: number
  options: PollOption[]
}

type PollListResponse = {
  polls?: Poll[]
}

type StateMeta = { label: string; className: string }

const UNKNOWN_STATE_META: StateMeta = {
  label: "Unknown",
  className: "bg-muted text-muted-foreground border-border",
}

const STATE_META: Record<PollState, StateMeta> = {
  live: { label: "Live", className: "bg-emerald-500/10 text-emerald-600 border-emerald-500/30" },
  scheduled: { label: "Scheduled", className: "bg-blue-500/10 text-blue-600 border-blue-500/30" },
  draft: { label: "Draft", className: "bg-amber-500/10 text-amber-600 border-amber-500/30" },
  ended: { label: "Ended", className: "bg-muted text-muted-foreground border-border" },
  superseded: { label: "Replaced", className: "bg-muted text-muted-foreground border-border" },
  closed: { label: "Closed", className: "bg-muted text-muted-foreground border-border" },
}

// A state the client does not recognise is a server that has moved ahead of it,
// not a reason to take the page down: an unlabelled badge beats a blank screen.
function stateMeta(state: PollState): StateMeta {
  return STATE_META[state] ?? UNKNOWN_STATE_META
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

// The way back out. A zoneless "YYYY-MM-DDTHH:mm" reaching the API is read as
// UTC, so a 9am start saved from Philadelphia comes back as 5am. The browser
// is the only side that knows which zone the editor typed in, so it has to say.
function localInputToISO(value: string): string {
  if (!value) return ""
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toISOString()
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

const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["minute", 60_000],
  ["hour", 3_600_000],
  ["day", 86_400_000],
  ["week", 604_800_000],
  ["month", 2_629_800_000],
  ["year", 31_557_600_000],
]

// "in 3 days" / "2 hours ago". An absolute timestamp alone makes an editor do
// the arithmetic to answer the only question they actually have, which is
// whether this happens before or after the thing they are about to do.
function relativeDate(value?: string): string {
  if (!value) return ""
  const target = new Date(value).getTime()
  if (Number.isNaN(target)) return ""
  const diff = target - Date.now()
  if (Math.abs(diff) < 60_000) return diff >= 0 ? "any moment now" : "just now"

  let unit = RELATIVE_UNITS[0]
  for (const candidate of RELATIVE_UNITS) {
    if (Math.abs(diff) >= candidate[1]) unit = candidate
  }
  return new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(
    Math.round(diff / unit[1]),
    unit[0],
  )
}

// One plain-language sentence about where the poll sits in its schedule.
function scheduleSummary(poll: Poll): string {
  switch (poll.state) {
    case "live":
      return poll.ends_at
        ? `On the site now. Closes ${relativeDate(poll.ends_at)}, ${formatRunDate(poll.ends_at)}`
        : "On the site now. Runs until you replace or close it."
    case "scheduled":
      return `Goes live ${relativeDate(poll.starts_at)}, ${formatRunDate(poll.starts_at)}${
        poll.ends_at ? `. Closes ${formatRunDate(poll.ends_at)}` : ""
      }`
    case "draft":
      return poll.starts_at
        ? `Not published. Publishing keeps its start date of ${formatRunDate(poll.starts_at)}`
        : "Not published. Readers cannot see this."
    case "ended":
      return `Ended ${relativeDate(poll.ends_at)}, ${formatRunDate(poll.ends_at)}`
    case "superseded":
      return "Taken off the site by a later poll"
    case "closed":
      return poll.starts_at ? `Closed. Ran from ${formatRunDate(poll.starts_at)}` : "Closed"
    default:
      return poll.status === "active" ? "Published" : "Not published"
  }
}

// Local "YYYY-MM-DDTHH:mm" strings compare correctly as strings, but only
// against each other; anything involving "now" has to go through Date.
function isPast(localValue: string): boolean {
  return localValue !== "" && new Date(localValue).getTime() <= Date.now()
}

// Whether publishing this poll would queue it rather than put it straight on
// the site: the difference between "this replaces what is running" and "this
// waits its turn".
function startsLater(poll: Poll): boolean {
  return !!poll.starts_at && new Date(poll.starts_at).getTime() > Date.now()
}

// Ordering only. Editing a poll that has already run must stay possible, so a
// date in the past is not by itself an error; the create form checks that
// separately, where it really would mean "publish something already over".
function windowError(startsAt: string, endsAt: string): string {
  if (startsAt && endsAt && endsAt <= startsAt) return "The end date must come after the start date."
  return ""
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
  // Publishing is one decision with three answers, not a checkbox plus two
  // dates that quietly mean different things depending on which are filled in.
  const [newTiming, setNewTiming] = useState<PublishTiming>("draft")

  const [editingPollId, setEditingPollId] = useState<number | null>(null)
  const [editQuestion, setEditQuestion] = useState("")
  const [editStartsAt, setEditStartsAt] = useState("")
  const [editEndsAt, setEditEndsAt] = useState("")

  const [addingOptionFor, setAddingOptionFor] = useState<number | null>(null)
  const [newOptionName, setNewOptionName] = useState("")
  const [renamingOptionId, setRenamingOptionId] = useState<number | null>(null)
  const [renameValue, setRenameValue] = useState("")

  const livePoll = useMemo(() => polls.find((poll) => poll.state === "live"), [polls])
  const queuedPolls = useMemo(() => polls.filter((poll) => poll.state === "scheduled"), [polls])

  const editWindowError = useMemo(
    () => windowError(editStartsAt, editEndsAt),
    [editStartsAt, editEndsAt],
  )

  const newWindowError = useMemo(() => {
    if (newTiming === "schedule" && newStartsAt && isPast(newStartsAt)) {
      return "That start date has already passed. Pick a later one, or choose Publish now."
    }
    const ordering = windowError(newTiming === "schedule" ? newStartsAt : "", newEndsAt)
    if (ordering) return ordering
    if (newEndsAt && isPast(newEndsAt)) return "That end date has already passed."
    return ""
  }, [newTiming, newStartsAt, newEndsAt])

  const loadPolls = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    try {
      const res = await apiFetch("/v1/polls/manage")
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load polls (${res.status})`))
      const body = (await res.json()) as PollListResponse
      setPolls(body.polls ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load polls.")
    } finally {
      setIsLoading(false)
    }
  }, [apiFetch])

  useEffect(() => {
    void loadPolls()
  }, [loadPolls])

  // Every mutation funnels through here so a failure surfaces one way and the
  // list is always refetched rather than patched optimistically: activating a
  // poll changes another poll's status server-side, which local state can't know.
  const mutate = useCallback(
    async (path: string, init: RequestInit, failure: string) => {
      setIsSaving(true)
      setError(null)
      try {
        const res = await apiFetch(path, init)
        if (!res.ok) {
          throw new Error(await readErrorMessage(res, `${failure} (${res.status})`))
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
    if (!question || newWindowError) return
    if (newTiming === "schedule" && !newStartsAt) return

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
          // "Now" and "scheduled" are the same published status; the start date
          // is what decides when readers see it.
          status: newTiming === "draft" ? "draft" : "active",
          starts_at: newTiming === "schedule" ? localInputToISO(newStartsAt) : null,
          ends_at: newTiming === "draft" ? null : localInputToISO(newEndsAt) || null,
        }),
      },
      "Could not create poll",
    )

    if (ok) {
      setNewQuestion("")
      setNewOptions("")
      setNewStartsAt("")
      setNewEndsAt("")
      setNewTiming("draft")
      setIsCreating(false)
    }
  }

  const savePollEdits = async (pollId: number) => {
    const question = editQuestion.trim()
    if (!question || editWindowError) return
    const ok = await mutate(
      `/v1/polls/${pollId}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          question,
          // Explicit null clears the date; omitting it would leave it unchanged.
          starts_at: localInputToISO(editStartsAt) || null,
          ends_at: localInputToISO(editEndsAt) || null,
        }),
      },
      "Could not update poll",
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
      "Could not change poll status",
    )

  // Publishing something that goes live immediately displaces the poll on the
  // site, which is not recoverable from the editor, so ask. A poll queued
  // behind a start date displaces nothing yet, so it just publishes.
  const publishPoll = (poll: Poll) => {
    if (!startsLater(poll) && livePoll && livePoll.id !== poll.id) {
      if (
        !confirm(
          `Publish "${poll.question}" now?\n\nThis takes "${livePoll.question}" off the site immediately. Its results are kept.`,
        )
      ) {
        return
      }
    }
    void setStatus(poll.id, "active")
  }

  const deletePoll = (poll: Poll) => {
    if (
      !confirm(
        `Delete "${poll.question}"?\n\nThis permanently removes the poll and its ${poll.total_votes} recorded votes. It will disappear from the public archive.`,
      )
    ) {
      return
    }
    void mutate(`/v1/polls/${poll.id}`, { method: "DELETE" }, "Could not delete poll")
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
      "Could not add poll option",
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
      "Could not rename poll option",
    )
    if (ok) setRenamingOptionId(null)
  }

  const deleteOption = (pollId: number, option: PollOption) => {
    if (!confirm(`Delete option "${option.option}" and its ${option.votes} votes?`)) return
    void mutate(
      `/v1/polls/${pollId}/options/${option.id}`,
      { method: "DELETE" },
      "Could not delete poll option",
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
                  livePoll ? `on the site: ${livePoll.question}` : "nothing on the site"
                }${queuedPolls.length ? ` • ${queuedPolls.length} scheduled` : ""}`}
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
        <form onSubmit={createPoll} className="rounded-lg border border-border bg-card p-4 flex flex-col gap-4">
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
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium text-foreground">When should this run?</span>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
              {TIMING_OPTIONS.map((option) => (
                <label
                  key={option.value}
                  className={`cursor-pointer rounded-lg border px-3 py-2.5 transition-colors ${
                    newTiming === option.value
                      ? "border-primary bg-primary/5 ring-1 ring-primary/30"
                      : "border-border hover:bg-muted/40"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <input
                      type="radio"
                      name="poll-timing"
                      className="accent-primary"
                      checked={newTiming === option.value}
                      onChange={() => setNewTiming(option.value)}
                    />
                    <span className="text-sm font-medium text-foreground">{option.label}</span>
                  </div>
                  <p className="text-xs text-muted-foreground mt-1 pl-6">{option.blurb}</p>
                </label>
              ))}
            </div>
          </div>

          {newTiming !== "draft" && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {newTiming === "schedule" && (
                <DateTimeField
                  label="Start date"
                  value={newStartsAt}
                  onChange={setNewStartsAt}
                  clearable
                  hint={
                    newStartsAt && !isPast(newStartsAt)
                      ? `Goes on the site ${relativeDate(newStartsAt)}.`
                      : "Readers see the poll from this moment."
                  }
                />
              )}
              <DateTimeField
                label="End date"
                value={newEndsAt}
                onChange={setNewEndsAt}
                clearable
                hint="Optional. Leave blank to run until you replace it."
              />
            </div>
          )}

          {newWindowError && <p className="text-xs text-destructive">{newWindowError}</p>}

          {/* Publishing takes a slot that only holds one poll, so say up front
              what happens to whatever is in it. */}
          {newTiming === "now" && livePoll && (
            <p className="text-xs text-amber-600">
              This closes "{livePoll.question}" as soon as you save.
            </p>
          )}
          {newTiming === "schedule" && newStartsAt && !newWindowError && livePoll && (
            <p className="text-xs text-muted-foreground">
              "{livePoll.question}" keeps running until then, and is replaced automatically.
            </p>
          )}

          <div className="flex items-center gap-2">
            <button
              type="submit"
              disabled={
                isSaving ||
                !newQuestion.trim() ||
                !!newWindowError ||
                (newTiming === "schedule" && !newStartsAt)
              }
              className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
            >
              {newTiming === "now" ? "Publish poll" : newTiming === "schedule" ? "Schedule poll" : "Save draft"}
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
        <div className="rounded-lg border border-border bg-card px-4 py-12 text-center text-muted-foreground">
          Loading polls...
        </div>
      ) : polls.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-4 py-12 text-center text-muted-foreground">
          No polls yet. Create one to get started.
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          {polls.map((poll) => (
            <div key={poll.id} className="rounded-lg border border-border bg-card overflow-hidden">
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
                        <DateTimeField
                          label="Start date"
                          value={editStartsAt}
                          onChange={setEditStartsAt}
                          clearable
                          hint={
                            poll.status === "active" && editStartsAt && !isPast(editStartsAt)
                              ? "Published, but held back until this date."
                              : "Blank means it starts the moment it is published."
                          }
                        />
                        <DateTimeField
                          label="End date"
                          value={editEndsAt}
                          onChange={setEditEndsAt}
                          clearable
                          error={editWindowError}
                          hint="Blank means no expiry."
                        />
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => savePollEdits(poll.id)}
                          disabled={isSaving || !editQuestion.trim() || !!editWindowError}
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
                          className={`text-[11px] uppercase tracking-normal px-2 py-0.5 rounded-full border ${stateMeta(poll.state).className}`}
                        >
                          {stateMeta(poll.state).label}
                        </span>
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">{scheduleSummary(poll)}</p>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        {poll.total_votes.toLocaleString()} vote{poll.total_votes === 1 ? "" : "s"}
                      </p>
                    </>
                  )}
                </div>

                {editingPollId !== poll.id && (
                  <div className="flex items-center gap-1 shrink-0">
                    {poll.status !== "active" && (
                      <button
                        type="button"
                        onClick={() => publishPoll(poll)}
                        disabled={isSaving}
                        className="p-1.5 rounded-lg text-muted-foreground hover:text-emerald-600 hover:bg-emerald-500/10 transition-colors"
                        title={
                          startsLater(poll)
                            ? `Publish. Goes live ${formatRunDate(poll.starts_at)}`
                            : "Publish. Goes on the site now"
                        }
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
                        title={poll.state === "scheduled" ? "Cancel this scheduled poll" : "Take this poll off the site"}
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
