import { useEffect, useMemo, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { useApiFetch } from "../hooks/useApiFetch"
import MediaPicker, { type MediaPickerItem } from "../components/MediaPicker"

type Kind = "trivia" | "personality"
type Status = "draft" | "published"

type Answer = { text: string; correct: boolean; result_key: string | null }
type Question = { text: string; image_media_id: number | null; image_url?: string | null; answers: Answer[]; explanation: string }
type Result = { key: string; min?: number; max?: number; title: string; body: string; image_media_id: number | null; image_url?: string | null }
type Definition = { questions: Question[]; results: Result[] }

type Quiz = {
  id: number
  slug: string
  title: string
  kind: Kind
  status: Status
  intro: string
  cover_media_id: number | null
  cover_url: string | null
  definition: Definition
  definition_version: number
  edit_version: number
  counts: number[][]
}

type ConflictBody = { reason?: string }
type ValidationBody = { errors?: string[]; warnings?: string[] }

const emptyAnswer = (): Answer => ({ text: "", correct: false, result_key: null })
const emptyQuestion = (): Question => ({ text: "", image_media_id: null, answers: [emptyAnswer(), emptyAnswer()], explanation: "" })

const slugify = (title: string) =>
  title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 190)

// Keeps the last band ending at the question count so adding or removing a
// question never leaves a gap the publish check would refuse.
const retargetBands = (results: Result[], questionCount: number): Result[] => {
  if (results.length === 0) return [{ key: `0-${questionCount}`, min: 0, max: questionCount, title: "", body: "", image_media_id: null }]
  const sorted = [...results].sort((a, b) => (a.min ?? 0) - (b.min ?? 0))
  const last = sorted[sorted.length - 1]
  return sorted.map((r) => (r === last ? { ...r, max: Math.max(r.min ?? 0, questionCount) } : r))
}

const inputClass = "w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
const buttonClass = "rounded-lg border border-border px-3 py-1.5 text-sm hover:bg-muted"
const primaryClass = "rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"

export default function QuizEditor() {
  const { id } = useParams()
  const navigate = useNavigate()
  const apiFetch = useApiFetch()
  const isNew = !id

  const [quiz, setQuiz] = useState<Quiz | null>(null)
  const [title, setTitle] = useState("")
  const [slug, setSlug] = useState("")
  const [slugEdited, setSlugEdited] = useState(false)
  const [kind, setKind] = useState<Kind>("trivia")
  const [intro, setIntro] = useState("")
  const [cover, setCover] = useState<{ id: number | null; url: string | null }>({ id: null, url: null })
  const [definition, setDefinition] = useState<Definition>({ questions: [emptyQuestion()], results: retargetBands([], 1) })
  const [errors, setErrors] = useState<string[]>([])
  const [warnings, setWarnings] = useState<string[]>([])
  const [notice, setNotice] = useState<string | null>(null)
  const [pendingStatus, setPendingStatus] = useState<Status | null>(null)
  const [resetPrompt, setResetPrompt] = useState<Status | null>(null)
  const [referencePrompt, setReferencePrompt] = useState<{ action: "unpublish" | "delete"; text: string } | null>(null)
  const [picker, setPicker] = useState<{ target: "cover" | { question: number } | { result: number } } | null>(null)
  const [tab, setTab] = useState<"edit" | "results">("edit")

  const hasCounts = useMemo(() => (quiz?.counts ?? []).some((row) => row.some((n) => n > 0)), [quiz])
  const isPublished = quiz?.status === "published"

  useEffect(() => {
    if (isNew) return
    let cancelled = false
    void (async () => {
      const res = await apiFetch(`/v1/quizzes/${id}`)
      if (!res.ok) {
        if (!cancelled) setErrors([`Could not load quiz (${res.status})`])
        return
      }
      const q = (await res.json()) as Quiz
      if (cancelled) return
      setQuiz(q)
      setTitle(q.title)
      setSlug(q.slug)
      setSlugEdited(true)
      setKind(q.kind)
      setIntro(q.intro)
      setCover({ id: q.cover_media_id, url: q.cover_url })
      setDefinition(q.definition)
    })()
    return () => {
      cancelled = true
    }
  }, [apiFetch, id, isNew])

  const updateQuestion = (qi: number, patch: Partial<Question>) =>
    setDefinition((d) => ({ ...d, questions: d.questions.map((q, i) => (i === qi ? { ...q, ...patch } : q)) }))
  const updateAnswer = (qi: number, ai: number, patch: Partial<Answer>) =>
    updateQuestion(qi, { answers: definition.questions[qi].answers.map((a, i) => (i === ai ? { ...a, ...patch } : a)) })
  const setCorrect = (qi: number, ai: number) =>
    updateQuestion(qi, { answers: definition.questions[qi].answers.map((a, i) => ({ ...a, correct: i === ai })) })
  const moveQuestion = (qi: number, delta: number) =>
    setDefinition((d) => {
      const next = [...d.questions]
      const target = qi + delta
      if (target < 0 || target >= next.length) return d
      ;[next[qi], next[target]] = [next[target], next[qi]]
      return { ...d, questions: next }
    })
  const setQuestions = (questions: Question[]) =>
    setDefinition((d) => ({ questions, results: kind === "trivia" ? retargetBands(d.results, questions.length) : d.results }))
  const updateResult = (ri: number, patch: Partial<Result>) =>
    setDefinition((d) => ({ ...d, results: d.results.map((r, i) => (i === ri ? { ...r, ...patch } : r)) }))

  const body = (status: Status) => ({
    slug: slug || slugify(title),
    title,
    kind,
    status,
    intro,
    cover_media_id: cover.id,
    definition,
    edit_version: quiz?.edit_version ?? 1,
  })

  // A plain function, not useCallback: nothing depends on its identity, and
  // body() is rebuilt every render anyway.
  const save = async (status: Status, reset = false) => {
    setErrors([])
    setWarnings([])
    setNotice(null)
    setPendingStatus(status)
    const url = isNew ? "/v1/quizzes" : `/v1/quizzes/${id}${reset ? "?reset_counts=true" : ""}`
    const res = await apiFetch(url, {
      method: isNew ? "POST" : "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body(status)),
    })
    setPendingStatus(null)
    if (res.status === 422) {
      const v = (await res.json()) as ValidationBody
      setErrors(v.errors ?? [])
      setWarnings(v.warnings ?? [])
      return
    }
    if (res.status === 409) {
      const c = (await res.json()) as ConflictBody
      if (c.reason === "reset_required") setResetPrompt(status)
      else setErrors(["Someone else saved this quiz. Reload to see their changes."])
      return
    }
    if (!res.ok) {
      setErrors([`Save failed (${res.status})`])
      return
    }
    const saved = (await res.json()) as Quiz
    setQuiz(saved)
    setSlug(saved.slug)
    setDefinition(saved.definition)
    setNotice(status === "published" ? "Published." : "Saved.")
    if (isNew) navigate(`/quizzes/${saved.id}`, { replace: true })
  }

  const confirmReferences = async (action: "unpublish" | "delete") => {
    const res = await apiFetch(`/v1/quizzes/${id}/references`)
    const refs = res.ok ? ((await res.json()) as { published: number; scheduled: number; draft: number }) : { published: 0, scheduled: 0, draft: 0 }
    const parts = [
      refs.published ? `${refs.published} published` : "",
      refs.scheduled ? `${refs.scheduled} scheduled` : "",
      refs.draft ? `${refs.draft} draft` : "",
    ].filter(Boolean)
    const total = refs.published + refs.scheduled + refs.draft
    const text = total === 0
      ? "No article contains this quiz."
      : `${parts.join(" and ")} article${total === 1 ? "" : "s"} contain this quiz. Readers will see "This quiz is no longer available" in its place.`
    setReferencePrompt({ action, text })
  }

  const remove = async () => {
    const res = await apiFetch(`/v1/quizzes/${id}`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ edit_version: quiz?.edit_version ?? 1 }),
    })
    if (res.status === 409) {
      setErrors(["Someone else saved this quiz. Reload to see their changes."])
      return
    }
    if (!res.ok && res.status !== 204) {
      setErrors([`Delete failed (${res.status})`])
      return
    }
    navigate("/quizzes")
  }

  const onPick = (item: MediaPickerItem) => {
    if (!picker) return
    const t = picker.target
    if (t === "cover") setCover({ id: item.id, url: item.url })
    else if ("question" in t) updateQuestion(t.question, { image_media_id: item.id, image_url: item.url })
    else updateResult(t.result, { image_media_id: item.id, image_url: item.url })
    setPicker(null)
  }

  const resultKeys = definition.results.map((r) => r.key).filter(Boolean)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-foreground">{isNew ? "New quiz" : "Edit quiz"}</h1>
        {!isNew && (
          <div className="flex gap-2">
            <button className={tab === "edit" ? primaryClass : buttonClass} onClick={() => setTab("edit")} type="button">Edit</button>
            <button className={tab === "results" ? primaryClass : buttonClass} onClick={() => setTab("results")} type="button">Results</button>
          </div>
        )}
      </div>

      {errors.length > 0 && (
        <ul className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {errors.map((e) => <li key={e}>{e}</li>)}
        </ul>
      )}
      {warnings.length > 0 && (
        <div className="rounded-lg border border-border p-3 text-sm">
          <ul>{warnings.map((w) => <li key={w}>{w}</li>)}</ul>
          <button className={`${buttonClass} mt-2`} onClick={() => void save("published")} type="button">Publish anyway</button>
        </div>
      )}
      {notice && <p className="text-sm text-muted-foreground">{notice}</p>}

      {tab === "results" && quiz && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">Percentages are of submitted answers. Starts are approximate.</p>
          {quiz.definition.questions.map((q, qi) => {
            const row = quiz.counts[qi] ?? []
            const total = row.reduce((a, b) => a + b, 0)
            return (
              <div className="rounded-lg border border-border p-3" key={qi}>
                <p className="font-medium">{qi + 1}. {q.text}</p>
                <ul className="mt-2 text-sm">
                  {q.answers.map((a, ai) => (
                    <li className="flex justify-between" key={ai}>
                      <span>{a.text}{a.correct ? " (correct)" : ""}</span>
                      <span>{row[ai] ?? 0} <span className="text-muted-foreground">{total ? `${Math.round(((row[ai] ?? 0) / total) * 100)}%` : "0%"}</span></span>
                    </li>
                  ))}
                </ul>
              </div>
            )
          })}
        </div>
      )}

      {tab === "edit" && (
        <form className="space-y-6" onSubmit={(e) => { e.preventDefault(); void save(quiz?.status ?? "draft") }}>
          <div className="grid gap-4 md:grid-cols-2">
            <label className="text-sm">Title
              <input className={inputClass} onChange={(e) => { setTitle(e.target.value); if (!slugEdited) setSlug(slugify(e.target.value)) }} required value={title} />
            </label>
            <label className="text-sm">Slug
              <input className={inputClass} onChange={(e) => { setSlug(e.target.value); setSlugEdited(true) }} readOnly={isPublished} value={slug} />
              {isPublished && <span className="text-xs text-muted-foreground">The slug is locked because shared links use it.</span>}
            </label>
          </div>
          <fieldset className="text-sm">
            <legend>Kind</legend>
            {(["trivia", "personality"] as Kind[]).map((k) => (
              <label className="mr-4" key={k}>
                <input checked={kind === k} name="kind" onChange={() => setKind(k)} type="radio" value={k} /> {k === "trivia" ? "Trivia" : "Personality"}
              </label>
            ))}
            {hasCounts && <p className="text-xs text-muted-foreground">Changing the kind resets the answer counts.</p>}
          </fieldset>
          <label className="block text-sm">Intro
            <textarea className={inputClass} onChange={(e) => setIntro(e.target.value)} rows={2} value={intro} />
          </label>
          <div className="text-sm">
            <span>Cover image</span>
            <div className="mt-1 flex items-center gap-3">
              {cover.url && <img alt="" className="h-16 w-24 rounded object-cover" src={cover.url} />}
              <button className={buttonClass} onClick={() => setPicker({ target: "cover" })} type="button">{cover.id ? "Change" : "Choose"}</button>
              {cover.id && <button className={buttonClass} onClick={() => setCover({ id: null, url: null })} type="button">Remove</button>}
            </div>
          </div>

          <section className="space-y-4">
            <h2 className="text-lg font-semibold">Questions</h2>
            {definition.questions.map((q, qi) => (
              <div className="space-y-3 rounded-lg border border-border p-4" key={qi}>
                <div className="flex items-center justify-between">
                  <span className="font-medium">Question {qi + 1}</span>
                  <div className="flex gap-2">
                    <button className={buttonClass} onClick={() => moveQuestion(qi, -1)} type="button">Move up</button>
                    <button className={buttonClass} onClick={() => moveQuestion(qi, 1)} type="button">Move down</button>
                    <button className={buttonClass} onClick={() => setQuestions(definition.questions.filter((_, i) => i !== qi))} type="button">Remove</button>
                  </div>
                </div>
                <input aria-label={`Question ${qi + 1} text`} className={inputClass} onChange={(e) => updateQuestion(qi, { text: e.target.value })} value={q.text} />
                <div className="flex items-center gap-3 text-sm">
                  {q.image_url && <img alt="" className="h-12 w-16 rounded object-cover" src={q.image_url} />}
                  <button className={buttonClass} onClick={() => setPicker({ target: { question: qi } })} type="button">{q.image_media_id ? "Change image" : "Add image"}</button>
                  {q.image_media_id && <button className={buttonClass} onClick={() => updateQuestion(qi, { image_media_id: null, image_url: null })} type="button">Remove image</button>}
                </div>
                {q.answers.map((a, ai) => (
                  <div className="flex items-center gap-2" key={ai}>
                    {kind === "trivia" ? (
                      <input aria-label={`Question ${qi + 1} answer ${ai + 1} correct`} checked={a.correct} name={`correct-${qi}`} onChange={() => setCorrect(qi, ai)} type="radio" />
                    ) : (
                      <select aria-label={`Question ${qi + 1} answer ${ai + 1} result`} className="rounded-lg border border-border bg-background px-2 py-1 text-sm" onChange={(e) => updateAnswer(qi, ai, { result_key: e.target.value || null })} value={a.result_key ?? ""}>
                        <option value="">Result</option>
                        {resultKeys.map((k) => <option key={k} value={k}>{k}</option>)}
                      </select>
                    )}
                    <input aria-label={`Question ${qi + 1} answer ${ai + 1} text`} className={inputClass} onChange={(e) => updateAnswer(qi, ai, { text: e.target.value })} value={a.text} />
                    {q.answers.length > 2 && <button className={buttonClass} onClick={() => updateQuestion(qi, { answers: q.answers.filter((_, i) => i !== ai) })} type="button">Remove</button>}
                  </div>
                ))}
                {q.answers.length < 6 && <button className={buttonClass} onClick={() => updateQuestion(qi, { answers: [...q.answers, emptyAnswer()] })} type="button">Add answer</button>}
                {kind === "trivia" && (
                  <label className="block text-sm">Explanation (shown after answering)
                    <input className={inputClass} onChange={(e) => updateQuestion(qi, { explanation: e.target.value })} value={q.explanation} />
                  </label>
                )}
              </div>
            ))}
            {definition.questions.length < 30 && <button className={buttonClass} onClick={() => setQuestions([...definition.questions, emptyQuestion()])} type="button">Add question</button>}
          </section>

          <section className="space-y-4">
            <h2 className="text-lg font-semibold">{kind === "trivia" ? "Score bands" : "Results"}</h2>
            {definition.results.map((r, ri) => (
              <div className="space-y-2 rounded-lg border border-border p-4" key={ri}>
                <div className="grid gap-2 md:grid-cols-3">
                  {kind === "trivia" ? (
                    <>
                      <label className="text-sm">From
                        <input className={inputClass} min={0} onChange={(e) => updateResult(ri, { min: Number(e.target.value) })} type="number" value={r.min ?? 0} />
                      </label>
                      <label className="text-sm">To
                        <input className={inputClass} min={0} onChange={(e) => updateResult(ri, { max: Number(e.target.value) })} type="number" value={r.max ?? 0} />
                      </label>
                    </>
                  ) : (
                    <label className="text-sm">Key
                      <input className={inputClass} onChange={(e) => updateResult(ri, { key: e.target.value })} pattern="[a-z0-9_-]{1,32}" value={r.key} />
                    </label>
                  )}
                  <label className="text-sm">Title
                    <input className={inputClass} onChange={(e) => updateResult(ri, { title: e.target.value })} value={r.title} />
                  </label>
                </div>
                <textarea className={inputClass} onChange={(e) => updateResult(ri, { body: e.target.value })} placeholder="Result text" rows={2} value={r.body} />
                <div className="flex items-center gap-3 text-sm">
                  {r.image_url && <img alt="" className="h-12 w-16 rounded object-cover" src={r.image_url} />}
                  <button className={buttonClass} onClick={() => setPicker({ target: { result: ri } })} type="button">{r.image_media_id ? "Change image" : "Add image"}</button>
                  <button className={buttonClass} onClick={() => setDefinition((d) => ({ ...d, results: d.results.filter((_, i) => i !== ri) }))} type="button">Remove result</button>
                </div>
              </div>
            ))}
            {definition.results.length < (kind === "trivia" ? 4 : 8) && (
              <button className={buttonClass} onClick={() => setDefinition((d) => ({ ...d, results: [...d.results, { key: "", min: 0, max: 0, title: "", body: "", image_media_id: null }] }))} type="button">
                {kind === "trivia" ? "Add band" : "Add result"}
              </button>
            )}
          </section>

          <div className="flex flex-wrap gap-2">
            {!isPublished && <button className={buttonClass} disabled={pendingStatus !== null} onClick={() => void save("draft")} type="button">Save draft</button>}
            <button className={primaryClass} disabled={pendingStatus !== null} onClick={() => void save("published")} type="button">Publish</button>
            {isPublished && <button className={buttonClass} onClick={() => void confirmReferences("unpublish")} type="button">Unpublish</button>}
            {!isNew && <button className={`${buttonClass} text-destructive`} onClick={() => void confirmReferences("delete")} type="button">Delete</button>}
          </div>
        </form>
      )}

      {resetPrompt && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation">
          <div aria-modal="true" className="w-full max-w-md space-y-3 rounded-xl border border-border bg-background p-5" role="dialog">
            <p className="text-sm">Saving changes the questions. The answer counts for this quiz will be reset. Continue?</p>
            <div className="flex justify-end gap-2">
              <button className={buttonClass} onClick={() => setResetPrompt(null)} type="button">Cancel</button>
              <button className={primaryClass} onClick={() => { const s = resetPrompt; setResetPrompt(null); void save(s, true) }} type="button">Save and reset</button>
            </div>
          </div>
        </div>
      )}

      {referencePrompt && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation">
          <div aria-modal="true" className="w-full max-w-md space-y-3 rounded-xl border border-border bg-background p-5" role="dialog">
            <p className="text-sm">{referencePrompt.text}</p>
            <div className="flex justify-end gap-2">
              <button className={buttonClass} onClick={() => setReferencePrompt(null)} type="button">Cancel</button>
              <button className={primaryClass} onClick={() => { const a = referencePrompt.action; setReferencePrompt(null); if (a === "delete") void remove(); else void save("draft") }} type="button">
                {referencePrompt.action === "delete" ? "Delete" : "Unpublish"}
              </button>
            </div>
          </div>
        </div>
      )}

      {picker && <MediaPicker imagesOnly onClose={() => setPicker(null)} onSelect={onPick} title="Choose an image" />}
    </div>
  )
}
