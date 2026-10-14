import { Check, Pencil, RefreshCw, Search, X } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useApiFetch } from "../hooks/useApiFetch"

type WordangleWord = {
  date?: string
  number?: number
  word: string
  source: "generated" | "custom"
  set_by?: string
  retired_on?: string
  updated_at?: string
}

type ManageResponse = {
  today: string
  queue: WordangleWord[]
  seen: WordangleWord[]
}

type CheckResponse = {
  word: string
  is_word: boolean
  status: "unused" | "queued" | "seen"
  entry?: WordangleWord
}

type Span = "day" | "week" | "month"

const SPANS: { span: Span; label: string }[] = [
  { span: "day", label: "Today's word" },
  { span: "week", label: "A week" },
  { span: "month", label: "A month" },
]

// Puzzle dates are calendar days, not instants; build them at UTC midnight so
// adding days never trips over a DST change.
function addDays(date: string, days: number) {
  const parsed = new Date(`${date}T00:00:00Z`)
  parsed.setUTCDate(parsed.getUTCDate() + days)
  return parsed.toISOString().slice(0, 10)
}

function formatDay(date: string) {
  return new Date(`${date}T00:00:00Z`).toLocaleDateString("en-US", {
    weekday: "short",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  })
}

// Matches the server's wordangle.PuzzleNumber: #0 is 2026-10-09, launch day.
function puzzleNumber(date: string) {
  return Math.round((Date.parse(`${date}T00:00:00Z`) - Date.UTC(2026, 9, 9)) / 86400000)
}

async function readErrorMessage(res: Response, fallback: string) {
  try {
    const body = await res.json() as { error?: string }
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

function describeCheck(result: CheckResponse) {
  const word = result.word.toUpperCase()
  if (!result.is_word) return { ok: false, text: `${word} isn't a six-letter word in the dictionary.` }
  if (result.status === "unused") return { ok: true, text: `${word} hasn't been used.` }
  const entry = result.entry
  if (entry?.retired_on) return { ok: false, text: `${word} was live on ${formatDay(entry.retired_on)} before being replaced.` }
  if (result.status === "queued") return { ok: false, text: `${word} is queued for ${formatDay(entry?.date ?? "")}.` }
  return { ok: false, text: `${word} was the word on ${formatDay(entry?.date ?? "")}.` }
}

function WordTiles({ word }: { word: string }) {
  return (
    <span className="inline-flex gap-1" aria-label={word}>
      {word.toUpperCase().split("").map((letter, i) => (
        <span
          key={i}
          aria-hidden="true"
          className="w-7 h-7 inline-flex items-center justify-center rounded border border-border bg-muted text-sm font-bold"
        >
          {letter}
        </span>
      ))}
    </span>
  )
}

export default function WordangleView() {
  const apiFetch = useApiFetch()
  const [data, setData] = useState<ManageResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState("")
  const [draftCheck, setDraftCheck] = useState<CheckResponse | null>(null)
  const [lookup, setLookup] = useState("")
  const [lookupResult, setLookupResult] = useState<CheckResponse | null>(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      const res = await apiFetch("/v1/wordangle/manage")
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not load Wordangle (${res.status})`))
      setData(await res.json() as ManageResponse)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not load Wordangle.")
    } finally {
      setLoading(false)
    }
  }, [apiFetch])

  useEffect(() => {
    void load()
  }, [load])

  const check = useCallback(async (word: string) => {
    const res = await apiFetch(`/v1/wordangle/check?word=${encodeURIComponent(word)}`)
    if (!res.ok) throw new Error(await readErrorMessage(res, "Could not check that word."))
    return await res.json() as CheckResponse
  }, [apiFetch])

  // Check the custom word as soon as it is six letters, so a collision with
  // the seen-before list shows before Save rather than after.
  useEffect(() => {
    setDraftCheck(null)
    if (draft.length !== 6) return
    let cancelled = false
    check(draft)
      .then((result) => { if (!cancelled) setDraftCheck(result) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [draft, check])

  // Today through the last queued day, empty days included, so a gap in the
  // queue shows up as a row to fill instead of silently falling back.
  const days = useMemo(() => {
    if (!data) return []
    const byDate = new Map(data.queue.map((entry) => [entry.date ?? "", entry]))
    const last = data.queue.reduce((max, entry) => (entry.date && entry.date > max ? entry.date : max), data.today)
    const rows: { date: string; entry?: WordangleWord }[] = []
    for (let date = data.today; date <= last; date = addDays(date, 1)) {
      rows.push({ date, entry: byDate.get(date) })
    }
    return rows
  }, [data])

  async function generate(span: Span) {
    setBusy(`generate-${span}`)
    setError(null)
    try {
      const res = await apiFetch("/v1/wordangle/generate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ span }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not generate words (${res.status})`))
      setData(await res.json() as ManageResponse)
    } catch (err) {
      // Generation can fail part way (the pool ran out), so refresh to show
      // what did land, then put the reason back: load() clears the error.
      const message = err instanceof Error ? err.message : "Could not generate words."
      await load()
      setError(message)
    } finally {
      setBusy(null)
    }
  }

  async function setDay(date: string, word: string) {
    setBusy(date)
    setError(null)
    try {
      const res = await apiFetch(`/v1/wordangle/days/${date}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ word }),
      })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not set the word (${res.status})`))
      setEditing(null)
      setDraft("")
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not set the word.")
    } finally {
      setBusy(null)
    }
  }

  function regenerate(date: string, isToday: boolean, hasWord: boolean) {
    if (isToday && hasWord && !window.confirm(
      "Replace today's word? Readers may already have played it, so it stays on the seen-before list.",
    )) return
    void setDay(date, "")
  }

  async function runLookup(event: React.FormEvent) {
    event.preventDefault()
    const word = lookup.trim().toLowerCase()
    if (!word) return
    try {
      setLookupResult(await check(word))
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not check that word.")
    }
  }

  const draftStatus = draftCheck ? describeCheck(draftCheck) : null

  return (
    <div className="flex flex-col gap-6 p-6">
      <div>
        <h1 className="text-2xl font-bold text-foreground">Wordangle</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          The daily word queue. No word is ever used twice. A day left empty gets a generated word when the first reader opens it.
        </p>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <section className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-foreground">Queue</h2>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-muted-foreground">Generate</span>
            {SPANS.map(({ span, label }) => (
              <button
                key={span}
                type="button"
                onClick={() => void generate(span)}
                disabled={busy !== null || !data}
                className="px-3 py-1.5 rounded-lg border border-border text-sm font-medium hover:bg-muted disabled:opacity-60"
              >
                {busy === `generate-${span}` ? "Generating..." : label}
              </button>
            ))}
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Generating fills empty days from today forward and never touches days that already have a word.
        </p>

        {loading ? (
          <p className="text-sm text-muted-foreground">Loading...</p>
        ) : !data ? null : (
          <div className="rounded-xl border border-border bg-card divide-y divide-border">
            {days.length === 1 && !days[0].entry && (
              <p className="px-4 pt-3 text-sm text-muted-foreground">The queue is empty. Generate some words to get started.</p>
            )}
            {days.map(({ date, entry }) => {
              const isToday = date === data.today
              const isEditing = editing === date
              return (
                <div key={date} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
                  <div className="w-40 shrink-0">
                    <div className="text-sm font-medium text-foreground flex items-center gap-2">
                      {formatDay(date)}
                      {isToday && (
                        <span className="px-2 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">Today</span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">#{puzzleNumber(date)}</div>
                  </div>

                  {isEditing ? (
                    <form
                      className="flex flex-1 flex-wrap items-center gap-2"
                      onSubmit={(event) => {
                        event.preventDefault()
                        void setDay(date, draft)
                      }}
                    >
                      <input
                        autoFocus
                        value={draft}
                        onChange={(event) => setDraft(event.target.value.toLowerCase().replace(/[^a-z]/g, "").slice(0, 6))}
                        placeholder="six letters"
                        aria-label={`Word for ${formatDay(date)}`}
                        className="w-32 px-3 py-1.5 rounded-lg border border-border bg-background text-sm uppercase tracking-widest"
                      />
                      <button
                        type="submit"
                        disabled={draft.length !== 6 || busy === date || (draftStatus !== null && !draftStatus.ok)}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 disabled:opacity-60"
                      >
                        <Check className="w-4 h-4" aria-hidden="true" />
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => { setEditing(null); setDraft("") }}
                        className="p-1.5 rounded-md hover:bg-muted"
                        aria-label="Cancel"
                      >
                        <X className="w-4 h-4" aria-hidden="true" />
                      </button>
                      {draftStatus && (
                        <span className={`text-xs ${draftStatus.ok ? "text-emerald-700 dark:text-emerald-400" : "text-destructive"}`}>
                          {draftStatus.text}
                        </span>
                      )}
                    </form>
                  ) : (
                    <>
                      <div className="flex flex-1 flex-wrap items-center gap-3">
                        {entry ? (
                          <>
                            <WordTiles word={entry.word} />
                            <span className="text-xs text-muted-foreground">
                              {entry.source === "custom" ? "Custom" : "Generated"}
                              {entry.set_by ? ` · ${entry.set_by}` : ""}
                            </span>
                          </>
                        ) : (
                          <span className="text-sm text-muted-foreground italic">No word yet</span>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => regenerate(date, isToday, Boolean(entry))}
                          disabled={busy !== null}
                          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm font-medium hover:bg-muted disabled:opacity-60"
                        >
                          <RefreshCw className={`w-4 h-4 ${busy === date ? "animate-spin" : ""}`} aria-hidden="true" />
                          {entry ? "Regenerate" : "Generate"}
                        </button>
                        <button
                          type="button"
                          onClick={() => { setEditing(date); setDraft("") }}
                          disabled={busy !== null}
                          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm font-medium hover:bg-muted disabled:opacity-60"
                        >
                          <Pencil className="w-4 h-4" aria-hidden="true" />
                          Write your own
                        </button>
                      </div>
                    </>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-lg font-semibold text-foreground">Seen before</h2>
        <form onSubmit={(event) => void runLookup(event)} className="flex flex-wrap items-center gap-2">
          <input
            value={lookup}
            onChange={(event) => { setLookup(event.target.value); setLookupResult(null) }}
            placeholder="Check a word"
            aria-label="Check a word"
            className="w-48 px-3 py-1.5 rounded-lg border border-border bg-background text-sm"
          />
          <button
            type="submit"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm font-medium hover:bg-muted"
          >
            <Search className="w-4 h-4" aria-hidden="true" />
            Check
          </button>
          {lookupResult && (
            <span className={`text-sm ${describeCheck(lookupResult).ok ? "text-emerald-700 dark:text-emerald-400" : "text-destructive"}`}>
              {describeCheck(lookupResult).text}
            </span>
          )}
        </form>

        {data && (data.seen.length === 0 ? (
          <p className="text-sm text-muted-foreground">No words have been used yet.</p>
        ) : (
          <div className="rounded-xl border border-border bg-card divide-y divide-border">
            {data.seen.map((entry) => (
              <div key={entry.word} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2 text-sm">
                <span className="w-40 shrink-0 text-muted-foreground">
                  {formatDay(entry.date ?? entry.retired_on ?? "")}
                  {entry.number != null ? ` · #${entry.number}` : ""}
                </span>
                <span className="font-mono font-semibold tracking-widest uppercase">{entry.word}</span>
                {entry.retired_on && (
                  <span className="text-xs text-muted-foreground">Replaced after going live</span>
                )}
              </div>
            ))}
          </div>
        ))}
      </section>
    </div>
  )
}
