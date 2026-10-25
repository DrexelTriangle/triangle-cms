import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ArrowLeft, Check, Code, Eye, Image, Monitor, Newspaper, Save, Send, Smartphone, X } from "lucide-react"
import { useNavigate, useParams } from "react-router-dom"
import TrixEditor, { type TrixEditorHandle } from "../components/TrixEditor"
import MediaPicker from "../components/MediaPicker"
import ArticlePicker from "../components/newsletter/ArticlePicker"
import { DateTimeField } from "../components/ui/datetime-field"
import { copyText } from "../lib/clipboard"
import { useNewsletterApi } from "../lib/newsletter/api"
import {
  articleCardHtml,
  FOOTER_TAGLINE,
  isHttpUrl,
  mastheadHtml,
  previewEmailHtml,
  renderNewsletterEmail,
} from "../lib/newsletter/template"
import type { Newsletter, NewsletterSponsor } from "../lib/newsletter/types"

const inputClass =
  "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
const labelClass = "flex flex-col gap-1.5"
const labelTextClass = "text-xs font-semibold text-muted-foreground uppercase tracking-wide"
const primaryButtonClass =
  "inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
const secondaryButtonClass =
  "inline-flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-muted text-foreground text-sm font-medium hover:bg-muted/70 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"

type Draft = {
  subject: string
  preheader: string
  sponsor: NewsletterSponsor | null
  body_html: string
}

const draftOf = (n: Newsletter): Draft => ({
  subject: n.subject,
  preheader: n.preheader,
  sponsor: n.sponsor,
  body_html: n.body_html,
})

const sameDraft = (a: Draft, b: Draft) => JSON.stringify(a) === JSON.stringify(b)

function PreviewDialog({ html, onClose }: { html: string; onClose: () => void }) {
  const [width, setWidth] = useState<"desktop" | "mobile">("desktop")

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  return (
    <div
      aria-label="Email preview"
      aria-modal="true"
      className="fixed inset-0 z-50 flex flex-col bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      role="dialog"
    >
      <div className="mx-auto flex h-full w-full max-w-4xl flex-col overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <h2 className="mr-auto text-sm font-semibold text-foreground">Email preview</h2>
          {(["desktop", "mobile"] as const).map((option) => (
            <button
              aria-pressed={width === option}
              className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium capitalize transition-colors ${width === option ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted"}`}
              key={option}
              onClick={() => setWidth(option)}
              type="button"
            >
              {option === "desktop" ? <Monitor className="h-3.5 w-3.5" /> : <Smartphone className="h-3.5 w-3.5" />}
              {option}
            </button>
          ))}
          <button
            aria-label="Close preview"
            className="rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            onClick={onClose}
            type="button"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="flex flex-1 justify-center overflow-hidden bg-muted/40 p-4">
          {/* sandbox with no allowances: the email is author HTML and nothing in
              it should run or navigate the dashboard. */}
          <iframe
            className="h-full rounded-md border border-border bg-white transition-[width]"
            sandbox=""
            srcDoc={html}
            style={{ width: width === "desktop" ? 680 : 375, maxWidth: "100%" }}
            title="Email preview"
          />
        </div>
      </div>
    </div>
  )
}

export default function EditNewsletterView() {
  const { id: idParam } = useParams()
  const id = Number(idParam)
  const navigate = useNavigate()
  const api = useNewsletterApi()
  const editorHandle = useRef<TrixEditorHandle>(null)

  const [newsletter, setNewsletter] = useState<Newsletter | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  // What the editor last loaded or saved, to tell real edits from none. Not
  // the server's echo: if it normalises what we send, comparing against that
  // would leave the page "dirty" forever after a save.
  const [baseline, setBaseline] = useState<Draft | null>(null)
  // Whether the author has done anything to the body since it was loaded; see setBody.
  const bodyTouchedRef = useRef(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [isSaving, setIsSaving] = useState(false)
  const [notice, setNotice] = useState<{ tone: "ok" | "error"; text: string } | null>(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [articlePickerOpen, setArticlePickerOpen] = useState(false)
  const [sponsorPickerOpen, setSponsorPickerOpen] = useState(false)
  const [htmlCopied, setHtmlCopied] = useState(false)
  const [testEmails, setTestEmails] = useState("")
  const [sendAt, setSendAt] = useState("")

  useEffect(() => {
    let cancelled = false
    if (!Number.isInteger(id) || id <= 0) {
      setLoadError("That newsletter doesn't exist.")
      return
    }
    api
      .get(id)
      .then((loaded) => {
        if (cancelled) return
        setNewsletter(loaded)
        setDraft(draftOf(loaded))
        setBaseline(draftOf(loaded))
        bodyTouchedRef.current = false
      })
      .catch((err: unknown) => {
        if (!cancelled) setLoadError(err instanceof Error ? err.message : "Could not load the newsletter.")
      })
    return () => {
      cancelled = true
    }
  }, [api, id])

  const isDirty = Boolean(baseline && draft && !sameDraft(baseline, draft))
  const isLocked = newsletter?.status === "sent" || newsletter?.status === "sending"
  const backendPending = api.backend === "mock"

  const emailHtml = useMemo(() => (draft ? renderNewsletterEmail(draft) : ""), [draft])

  const patch = useCallback((changes: Partial<Draft>) => {
    setDraft((current) => (current ? { ...current, ...changes } : current))
  }, [])
  const markBodyTouched = useCallback(() => {
    bodyTouchedRef.current = true
  }, [])
  const setBody = useCallback(
    (body_html: string) => {
      // Trix re-serialises the template HTML when it loads it, and again as
      // images finish loading, which would otherwise read as edits on an issue
      // nobody has touched. Until the author does something to the body, every
      // emission is that normalisation and becomes the new baseline.
      if (!bodyTouchedRef.current) {
        setBaseline((current) => (current ? { ...current, body_html } : current))
      }
      patch({ body_html })
    },
    [patch],
  )

  const save = useCallback(async () => {
    if (!draft || !newsletter || isLocked) return
    setIsSaving(true)
    setNotice(null)
    try {
      const sent = draft
      const saved = await api.update(newsletter.id, { ...sent, email_html: renderNewsletterEmail(sent) })
      setNewsletter(saved)
      // Edits typed while the save was in flight stay dirty.
      setBaseline(sent)
      setNotice({ tone: "ok", text: "Saved." })
    } catch (err) {
      setNotice({ tone: "error", text: err instanceof Error ? err.message : "Could not save." })
    } finally {
      setIsSaving(false)
    }
  }, [api, draft, isLocked, newsletter])

  // Cmd/Ctrl+S saves, as the muscle memory from every other editor expects.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
        event.preventDefault()
        void save()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [save])

  useEffect(() => {
    if (!isDirty) return
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener("beforeunload", onBeforeUnload)
    return () => window.removeEventListener("beforeunload", onBeforeUnload)
  }, [isDirty])

  const copyHtml = async () => {
    await copyText(emailHtml)
    setHtmlCopied(true)
    window.setTimeout(() => setHtmlCopied(false), 2000)
  }

  const runBackendAction = async (action: () => Promise<unknown>, success: string) => {
    setNotice(null)
    try {
      await action()
      setNotice({ tone: "ok", text: success })
    } catch (err) {
      setNotice({ tone: "error", text: err instanceof Error ? err.message : "That didn't work." })
    }
  }

  if (loadError) {
    return (
      <div className="flex flex-col items-center gap-4 p-12 text-center">
        <p className="text-sm text-muted-foreground">{loadError}</p>
        <button className={secondaryButtonClass} onClick={() => navigate("/newsletter")} type="button">
          Back to newsletters
        </button>
      </div>
    )
  }

  if (!draft || !newsletter) {
    return <div className="p-8 text-center text-sm text-muted-foreground">Loading newsletter...</div>
  }

  const sponsor = draft.sponsor
  const sponsorLinkInvalid = Boolean(sponsor?.link_url.trim()) && !isHttpUrl(sponsor?.link_url.trim() ?? "")

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex items-center gap-4">
          <button
            className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
            onClick={() => navigate("/newsletter")}
            type="button"
          >
            <ArrowLeft className="w-4 h-4" aria-hidden="true" />
            Back
          </button>
          <h1 className="text-2xl font-bold text-foreground">Edit Newsletter</h1>
        </div>
        <p
          className={`rounded-lg border px-3 py-2 text-xs font-medium sm:ml-auto ${
            notice?.tone === "error"
              ? "border-destructive/40 bg-destructive/10 text-destructive"
              : isDirty
                ? "border-yellow-300 bg-yellow-50 text-yellow-800 dark:border-yellow-900 dark:bg-yellow-900/20 dark:text-yellow-300"
                : "border-border bg-muted/40 text-muted-foreground"
          }`}
          role="status"
        >
          {notice?.tone === "error" ? notice.text : isDirty ? "Unsaved changes" : notice?.text ?? "All changes saved"}
        </p>
      </div>

      {backendPending ? (
        <p className="rounded-lg border border-yellow-300 bg-yellow-50 px-4 py-3 text-sm text-yellow-800 dark:border-yellow-900 dark:bg-yellow-900/20 dark:text-yellow-300">
          Preview build: drafts stay in this browser and sending is off.
        </p>
      ) : null}

      {isLocked ? (
        <p className="rounded-lg border border-border bg-muted/40 px-4 py-3 text-sm text-muted-foreground">
          Sent issues can't be edited. Duplicate it to reuse it.
        </p>
      ) : null}

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_300px] gap-6 items-start">
        <div className="flex flex-col gap-5 rounded-xl border border-border bg-card p-6">
          <label className={labelClass}>
            <span className={labelTextClass}>Subject</span>
            <input
              className={inputClass}
              disabled={isLocked}
              onChange={(e) => patch({ subject: e.target.value })}
              value={draft.subject}
            />
          </label>

          <label className={labelClass}>
            <span className={labelTextClass}>Preview text</span>
            <input
              className={inputClass}
              disabled={isLocked}
              onChange={(e) => patch({ preheader: e.target.value })}
              placeholder="Shown after the subject in inboxes"
              value={draft.preheader}
            />
          </label>

          <div className={labelClass}>
            <span className={labelTextClass}>Sponsor banner</span>
            {sponsor ? (
              <div className="flex flex-col gap-3 rounded-lg border border-border bg-muted/30 p-3">
                <div className="flex items-start gap-3">
                  <img
                    alt={sponsor.alt || "Sponsor banner"}
                    className="h-16 w-28 flex-shrink-0 rounded-md object-cover"
                    referrerPolicy="no-referrer"
                    src={sponsor.image_url}
                  />
                  {!isLocked ? (
                    <div className="flex flex-col gap-2">
                      <button
                        className="text-left text-xs font-medium text-primary hover:underline"
                        onClick={() => setSponsorPickerOpen(true)}
                        type="button"
                      >
                        Change image
                      </button>
                      <button
                        className="inline-flex items-center gap-1 text-xs font-medium text-destructive hover:underline"
                        onClick={() => patch({ sponsor: null })}
                        type="button"
                      >
                        <X className="h-3 w-3" />
                        Remove
                      </button>
                    </div>
                  ) : null}
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label className="flex flex-col gap-1">
                    <span className="text-xs font-medium text-muted-foreground">Links to</span>
                    <input
                      aria-invalid={sponsorLinkInvalid}
                      className={inputClass}
                      disabled={isLocked}
                      onChange={(e) => patch({ sponsor: { ...sponsor, link_url: e.target.value } })}
                      placeholder="https://"
                      type="url"
                      value={sponsor.link_url}
                    />
                    {sponsorLinkInvalid ? (
                      <span className="text-xs text-destructive">Needs a full https:// link.</span>
                    ) : null}
                  </label>
                  <label className="flex flex-col gap-1">
                    <span className="text-xs font-medium text-muted-foreground">Alt text</span>
                    <input
                      className={inputClass}
                      disabled={isLocked}
                      onChange={(e) => patch({ sponsor: { ...sponsor, alt: e.target.value } })}
                      placeholder="What the banner says"
                      value={sponsor.alt}
                    />
                  </label>
                </div>
              </div>
            ) : (
              <button
                className="flex h-20 flex-col items-center justify-center gap-1.5 rounded-lg border-2 border-dashed border-border text-muted-foreground transition-colors hover:border-primary hover:bg-primary/5 hover:text-primary disabled:pointer-events-none disabled:opacity-50"
                disabled={isLocked}
                onClick={() => setSponsorPickerOpen(true)}
                type="button"
              >
                <Image className="h-5 w-5" />
                <span className="text-xs font-medium">Add sponsor banner</span>
              </button>
            )}
          </div>

          <div className={labelClass}>
            <div className="flex items-center justify-between">
              <span className={labelTextClass}>Content</span>
              {!isLocked ? (
                <button
                  className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"
                  onClick={() => setArticlePickerOpen(true)}
                  type="button"
                >
                  <Newspaper className="h-3.5 w-3.5" />
                  Insert article
                </button>
              ) : null}
            </div>
            <div className="overflow-hidden rounded-lg border border-border">
              {/* The fixed parts of the template frame the editable body, so the
                  author writes in place rather than in a box with no context. */}
              <div className="bg-white py-3" dangerouslySetInnerHTML={{ __html: mastheadHtml() }} />
              <div
                className={isLocked ? "pointer-events-none opacity-70" : ""}
                onCutCapture={markBodyTouched}
                onDropCapture={markBodyTouched}
                onKeyDownCapture={markBodyTouched}
                onMouseDownCapture={markBodyTouched}
                onPasteCapture={markBodyTouched}
              >
                <TrixEditor handleRef={editorHandle} onChange={setBody} value={draft.body_html} />
              </div>
              <div className="border-t border-border bg-muted/30 px-4 py-3 text-center text-xs text-muted-foreground">
                <div className="text-foreground">{FOOTER_TAGLINE}</div>
                <div className="mt-1">X · Instagram</div>
                <div className="mt-1">Unsubscribe | Manage your subscription | View online</div>
              </div>
            </div>
          </div>
        </div>

        <aside className="flex flex-col gap-6">
          <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">Issue</h2>
            <p className="text-xs text-muted-foreground">
              Status: <span className="font-medium capitalize text-foreground">{newsletter.status}</span>
              {newsletter.scheduled_for ? ` · ${new Date(newsletter.scheduled_for).toLocaleString()}` : ""}
            </p>
            <button className={primaryButtonClass} disabled={isSaving || isLocked || !isDirty} onClick={() => void save()} type="button">
              <Save className="h-4 w-4" />
              {isSaving ? "Saving..." : "Save draft"}
            </button>
            <button className={secondaryButtonClass} onClick={() => setPreviewOpen(true)} type="button">
              <Eye className="h-4 w-4" />
              Preview email
            </button>
            <button className={secondaryButtonClass} onClick={() => void copyHtml()} type="button">
              {htmlCopied ? <Check className="h-4 w-4" /> : <Code className="h-4 w-4" />}
              {htmlCopied ? "HTML copied" : "Copy email HTML"}
            </button>
          </div>

          <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">Send</h2>
            {backendPending ? (
              <p className="text-xs text-muted-foreground">Sending is off in this build.</p>
            ) : null}
            <label className="flex flex-col gap-1.5">
              <span className="text-xs font-medium text-muted-foreground">Send a test to</span>
              <input
                className={inputClass}
                disabled={backendPending}
                onChange={(e) => setTestEmails(e.target.value)}
                placeholder="Emails, separated by commas"
                value={testEmails}
              />
            </label>
            <button
              className={secondaryButtonClass}
              disabled={backendPending || isDirty || !testEmails.trim()}
              onClick={() =>
                void runBackendAction(
                  () =>
                    api.sendTest(
                      newsletter.id,
                      testEmails.split(",").map((e) => e.trim()).filter(Boolean),
                    ),
                  "Test sent.",
                )
              }
              title={isDirty ? "Save before sending a test" : undefined}
              type="button"
            >
              <Send className="h-4 w-4" />
              Send test
            </button>
            <DateTimeField
              clearable
              disabled={backendPending || isLocked}
              label="Send at"
              onChange={setSendAt}
              value={sendAt}
            />
            {newsletter.status === "scheduled" ? (
              <button
                className={secondaryButtonClass}
                disabled={backendPending}
                onClick={() => void runBackendAction(async () => setNewsletter(await api.unschedule(newsletter.id)), "Unscheduled.")}
                type="button"
              >
                Unschedule
              </button>
            ) : (
              <button
                className={primaryButtonClass}
                disabled={backendPending || isLocked || isDirty || !sendAt}
                onClick={() =>
                  void runBackendAction(
                    async () => setNewsletter(await api.schedule(newsletter.id, new Date(sendAt).toISOString())),
                    "Scheduled.",
                  )
                }
                type="button"
              >
                Schedule
              </button>
            )}
          </div>

          <div className="flex flex-col gap-2 rounded-xl border border-border bg-card p-6 text-xs text-muted-foreground">
            <h2 className="text-base font-semibold text-foreground">Formatting</h2>
            <ul className="flex flex-col gap-1">
              <li><span className="font-medium text-foreground">Heading:</span> story headline</li>
              <li><span className="font-medium text-foreground">Quote:</span> section label</li>
              <li><span className="font-medium text-foreground">Link on its own line:</span> button</li>
            </ul>
          </div>
        </aside>
      </div>

      {previewOpen ? <PreviewDialog html={previewEmailHtml(emailHtml)} onClose={() => setPreviewOpen(false)} /> : null}

      {articlePickerOpen ? (
        <ArticlePicker
          onClose={() => setArticlePickerOpen(false)}
          onSelect={(article) => {
            markBodyTouched()
            editorHandle.current?.insertHTML(articleCardHtml(article))
            setArticlePickerOpen(false)
          }}
        />
      ) : null}

      {sponsorPickerOpen ? (
        <MediaPicker
          onClose={() => setSponsorPickerOpen(false)}
          onSelect={(item) => {
            patch({
              sponsor: {
                image_url: item.url,
                link_url: sponsor?.link_url ?? "",
                alt: item.alt_text || sponsor?.alt || "",
              },
            })
            setSponsorPickerOpen(false)
          }}
          onUseUrl={(url) => {
            patch({ sponsor: { image_url: url, link_url: sponsor?.link_url ?? "", alt: sponsor?.alt ?? "" } })
            setSponsorPickerOpen(false)
          }}
          title="Choose a sponsor banner"
        />
      ) : null}
    </div>
  )
}
