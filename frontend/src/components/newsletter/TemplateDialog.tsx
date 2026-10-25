import { useEffect, useState } from "react"
import { ArrowDown, ArrowUp, ImageOff, Pencil, Plus, X } from "lucide-react"
import { useApiFetch } from "../../hooks/useApiFetch"
import { fetchLatestComic, fetchPublishedArticles, toComic, type PickableArticle } from "../../lib/newsletter/articles"
import {
  buildTemplateBody,
  decodeEntities,
  defaultSubject,
  NEWSLETTER_NAME,
  renderNewsletterEmail,
  type TemplateComic,
} from "../../lib/newsletter/template"
import type { NewsletterInput, NewsletterSponsor } from "../../lib/newsletter/types"
import ArticlePicker from "./ArticlePicker"

// The issue that "To The Point" usually runs: a handful of the week's stories.
const DEFAULT_ARTICLE_COUNT = 5

type TemplateDialogProps = {
  // The most recent issue's sponsor, offered for reuse since ads run for weeks.
  previousSponsor: NewsletterSponsor | null
  onCreate: (input: NewsletterInput) => Promise<void>
  onClose: () => void
}

const todayInput = () => {
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

// A date input's value is a calendar day; read it as local noon so no timezone
// can roll it onto the neighbouring day.
const dateFromInput = (value: string) => new Date(`${value}T12:00:00`)

const inputClass =
  "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
const labelTextClass = "text-xs font-semibold text-muted-foreground uppercase tracking-wide"
const iconButtonClass = "rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-30"

type ListKind = "story" | "comic"

// The open article picker: adding to a list, or swapping one of its entries.
type Picker = { list: ListKind; swapId: number | null }

type PickListProps = {
  kind: ListKind
  items: PickableArticle[]
  isLoading: boolean
  onAdd: () => void
  onSwap: (id: number) => void
  onChange: (items: PickableArticle[]) => void
}

// An ordered list of stories or comics, with swap, reorder and remove.
function PickList({ kind, items, isLoading, onAdd, onSwap, onChange }: PickListProps) {
  const plural = kind === "story" ? "Stories" : "Comics"
  const move = (index: number, delta: number) => {
    const target = index + delta
    if (target < 0 || target >= items.length) return
    const next = [...items]
    ;[next[index], next[target]] = [next[target], next[index]]
    onChange(next)
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className={labelTextClass}>
          {plural} ({items.length})
        </span>
        <button
          className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
          onClick={onAdd}
          type="button"
        >
          <Plus className="h-3.5 w-3.5" />
          Add {kind}
        </button>
      </div>
      {isLoading || items.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
          {isLoading ? `Loading ${plural.toLowerCase()}...` : `No ${plural.toLowerCase()} yet.`}
        </p>
      ) : (
        <ol className="flex flex-col divide-y divide-border rounded-lg border border-border">
          {items.map((item, index) => (
            <li className="flex items-center gap-3 px-3 py-2" key={item.id}>
              {item.image_url ? (
                <img
                  alt=""
                  className="h-10 w-14 flex-shrink-0 rounded object-cover"
                  referrerPolicy="no-referrer"
                  src={item.image_url}
                />
              ) : (
                <span className="flex h-10 w-14 flex-shrink-0 items-center justify-center rounded bg-muted text-muted-foreground">
                  <ImageOff className="h-4 w-4" />
                </span>
              )}
              <span className="min-w-0 flex-1 truncate text-sm text-foreground">{decodeEntities(item.title)}</span>
              <span className="flex flex-shrink-0 items-center gap-0.5">
                <button
                  aria-label={`Swap ${decodeEntities(item.title)}`}
                  className={iconButtonClass}
                  onClick={() => onSwap(item.id)}
                  title={`Swap ${kind}`}
                  type="button"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
                <button
                  aria-label="Move up"
                  className={iconButtonClass}
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                  type="button"
                >
                  <ArrowUp className="h-3.5 w-3.5" />
                </button>
                <button
                  aria-label="Move down"
                  className={iconButtonClass}
                  disabled={index === items.length - 1}
                  onClick={() => move(index, 1)}
                  type="button"
                >
                  <ArrowDown className="h-3.5 w-3.5" />
                </button>
                <button
                  aria-label="Remove"
                  className="rounded p-1 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                  onClick={() => onChange(items.filter((other) => other.id !== item.id))}
                  type="button"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

export default function TemplateDialog({ previousSponsor, onCreate, onClose }: TemplateDialogProps) {
  const apiFetch = useApiFetch()
  const [issueDate, setIssueDate] = useState(todayInput)
  const [subject, setSubject] = useState(() => defaultSubject(new Date()))
  const [subjectEdited, setSubjectEdited] = useState(false)
  const [articles, setArticles] = useState<PickableArticle[]>([])
  const [comics, setComics] = useState<PickableArticle[]>([])
  const [question, setQuestion] = useState("")
  const [reuseSponsor, setReuseSponsor] = useState(Boolean(previousSponsor))
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [picker, setPicker] = useState<Picker | null>(null)
  const [isCreating, setIsCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    Promise.allSettled([
      fetchPublishedArticles(apiFetch, { limit: DEFAULT_ARTICLE_COUNT, signal: controller.signal }),
      fetchLatestComic(apiFetch, controller.signal),
    ]).then(([latest, latestComic]) => {
      if (controller.signal.aborted) return
      if (latest.status === "fulfilled") setArticles(latest.value)
      else setLoadError("Couldn't load the latest articles.")
      if (latestComic.status === "fulfilled" && latestComic.value) setComics([latestComic.value])
      setIsLoading(false)
    })
    return () => controller.abort()
  }, [apiFetch])

  // A picker on top handles its own Escape; this one would close the dialog
  // under it too.
  useEffect(() => {
    if (picker) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose, picker])

  const changeDate = (value: string) => {
    setIssueDate(value)
    if (!subjectEdited && value) setSubject(defaultSubject(dateFromInput(value)))
  }

  const pick = (article: PickableArticle) => {
    if (!picker) return
    const setList = picker.list === "story" ? setArticles : setComics
    const { swapId } = picker
    setList((current) =>
      swapId === null ? [...current, article] : current.map((item) => (item.id === swapId ? article : item)),
    )
    setPicker(null)
  }

  const create = async () => {
    setIsCreating(true)
    setCreateError(null)
    const sponsor = reuseSponsor ? previousSponsor : null
    const body_html = buildTemplateBody({
      articles,
      comics: comics.map(toComic).filter((comic): comic is TemplateComic => comic !== null),
      question,
    })
    const fields = { subject: subject.trim() || defaultSubject(dateFromInput(issueDate)), preheader: "", sponsor, body_html }
    try {
      await onCreate({ ...fields, email_html: renderNewsletterEmail(fields) })
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : "Could not create the newsletter.")
      setIsCreating(false)
    }
  }

  const pickerList = picker?.list === "story" ? articles : comics

  return (
    <div
      aria-labelledby="template-dialog-title"
      aria-modal="true"
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      role="dialog"
    >
      <div className="flex max-h-[90vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-start justify-between border-b border-border px-6 py-4">
          <h2 className="text-lg font-semibold text-foreground" id="template-dialog-title">
            New {NEWSLETTER_NAME} issue
          </h2>
          <button
            aria-label="Close"
            className="rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            onClick={onClose}
            type="button"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="flex flex-1 flex-col gap-5 overflow-y-auto px-6 py-5">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-[180px_1fr]">
            <label className="flex flex-col gap-1.5">
              <span className={labelTextClass}>Issue date</span>
              <input className={inputClass} onChange={(e) => changeDate(e.target.value)} type="date" value={issueDate} />
            </label>
            <label className="flex flex-col gap-1.5">
              <span className={labelTextClass}>Subject</span>
              <input
                className={inputClass}
                onChange={(e) => {
                  setSubject(e.target.value)
                  setSubjectEdited(true)
                }}
                value={subject}
              />
            </label>
          </div>

          <div className="flex flex-col gap-2">
            {loadError ? <p className="text-xs text-destructive">{loadError}</p> : null}
            <PickList
              isLoading={isLoading}
              items={articles}
              kind="story"
              onAdd={() => setPicker({ list: "story", swapId: null })}
              onChange={setArticles}
              onSwap={(id) => setPicker({ list: "story", swapId: id })}
            />
          </div>

          <label className="flex flex-col gap-1.5">
            <span className={labelTextClass}>Question of the day</span>
            <input
              className={inputClass}
              onChange={(e) => setQuestion(e.target.value)}
              placeholder="What is your favorite form of egg to eat?"
              value={question}
            />
          </label>

          <PickList
            isLoading={isLoading}
            items={comics}
            kind="comic"
            onAdd={() => setPicker({ list: "comic", swapId: null })}
            onChange={setComics}
            onSwap={(id) => setPicker({ list: "comic", swapId: id })}
          />

          {previousSponsor ? (
            <label className="flex items-center gap-3 rounded-lg border border-border px-3 py-2">
              <input
                checked={reuseSponsor}
                className="h-4 w-4 accent-primary"
                onChange={(e) => setReuseSponsor(e.target.checked)}
                type="checkbox"
              />
              <img alt="" className="h-10 w-14 rounded object-cover" referrerPolicy="no-referrer" src={previousSponsor.image_url} />
              <span className="text-sm">
                <span className="block font-medium text-foreground">Keep last issue's sponsor banner</span>
                <span className="block text-xs text-muted-foreground">{previousSponsor.alt || previousSponsor.link_url}</span>
              </span>
            </label>
          ) : null}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border px-6 py-4">
          {createError ? <p className="mr-auto text-xs text-destructive">{createError}</p> : null}
          <button
            className="rounded-lg bg-muted px-4 py-2 text-sm font-medium text-foreground transition-colors hover:bg-muted/70"
            onClick={onClose}
            type="button"
          >
            Cancel
          </button>
          <button
            className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={isCreating || isLoading}
            onClick={() => void create()}
            type="button"
          >
            {isCreating ? "Creating..." : "Create draft"}
          </button>
        </div>
      </div>

      {picker ? (
        <ArticlePicker
          comics={picker.list === "comic"}
          excludeIds={pickerList.map((item) => item.id)}
          onClose={() => setPicker(null)}
          onSelect={pick}
          title={`${picker.swapId === null ? "Add" : "Swap"} ${picker.list === "story" ? "a story" : "a comic"}`}
        />
      ) : null}
    </div>
  )
}
