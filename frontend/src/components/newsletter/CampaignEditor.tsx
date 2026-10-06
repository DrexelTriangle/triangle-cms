import { ArrowDown, ArrowUp, Lock, Trash2 } from "lucide-react"
import { useCallback, useEffect, useId, useRef, useState } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"
import BlockFields from "./BlockFields"
import Dialog from "./Dialog"
import {
  readErrorMessage,
  type Block,
  type BlockDocument,
  type BlockType,
  type Campaign,
  type EditorBlock,
  type NewsletterList,
  type RenderResult,
} from "./types"

const PREVIEW_DELAY_MS = 400

const BLOCK_LABELS: Record<BlockType, string> = {
  text: "Text",
  heading: "Heading",
  article: "Article",
  button: "Button",
  image: "Image",
  divider: "Divider",
}

let keySeq = 0
const nextKey = () => `b${++keySeq}`

function emptyBlock(type: BlockType): EditorBlock {
  const key = nextKey()
  switch (type) {
    case "text": return { key, type, html: "" }
    case "heading": return { key, type, text: "" }
    case "button": return { key, type, label: "", href: "" }
    case "article": return { key, type, article_id: 0, show_image: true, show_excerpt: true }
    case "image": return { key, type, src: "", alt: "", href: "" }
    case "divider": return { key, type }
  }
}

// toDocument drops the editor-only fields (key, title) and empty optionals,
// leaving exactly the block document the server stores.
function toDocument(blocks: EditorBlock[]): BlockDocument {
  return {
    version: 1,
    blocks: blocks.map((b): Block => {
      switch (b.type) {
        case "text": return { type: "text", html: b.html }
        case "heading": return { type: "heading", text: b.text }
        case "button": return { type: "button", label: b.label, href: b.href }
        case "article": return { type: "article", article_id: b.article_id, show_image: b.show_image, show_excerpt: b.show_excerpt }
        case "divider": return { type: "divider" }
        case "image": {
          const out: Extract<Block, { type: "image" }> = { type: "image", src: b.src }
          if (b.alt) out.alt = b.alt
          if (b.href) out.href = b.href
          return out
        }
      }
    }),
  }
}

function fromDocument(doc?: BlockDocument): EditorBlock[] {
  return (doc?.blocks ?? []).map((b) => ({ ...b, key: nextKey() }) as EditorBlock)
}

type CampaignEditorProps = {
  campaign: Campaign | null // null = new
  lists: NewsletterList[]
  onClose: () => void
  onSaved: () => void
}

// Edits a draft: subject, preview text, target lists and the block body, with
// a live preview rendered by the server from the ported WordPress template.
// Article links are locked to the article id on save (server-side), so the
// "Linked articles" list shows what each link will follow.
export default function CampaignEditor({ campaign, lists, onClose, onSaved }: CampaignEditorProps) {
  const apiFetch = useApiFetch()
  const subjectId = useId()
  const previewId = useId()
  const [subject, setSubject] = useState(campaign?.subject ?? "")
  const [previewText, setPreviewText] = useState(campaign?.preview_text ?? "")
  const [listIds, setListIds] = useState<number[]>(campaign?.list_ids ?? [])
  const [blocks, setBlocks] = useState<EditorBlock[]>(() => fromDocument(campaign?.body_blocks))
  const [rendered, setRendered] = useState<RenderResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const renderSeq = useRef(0)

  useEffect(() => {
    if (blocks.length === 0) {
      setRendered(null)
      return
    }
    const seq = ++renderSeq.current
    const timer = window.setTimeout(async () => {
      try {
        const res = await apiFetch("/v1/newsletter/render", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ subject, preview_text: previewText, body_blocks: toDocument(blocks) }),
        })
        if (!res.ok) throw new Error(await readErrorMessage(res, `Preview failed (${res.status})`))
        const body = (await res.json()) as RenderResult
        if (seq === renderSeq.current) {
          setRendered(body)
          setPreviewError(null)
        }
      } catch (err) {
        if (seq === renderSeq.current) setPreviewError(err instanceof Error ? err.message : "Preview failed")
      }
    }, PREVIEW_DELAY_MS)
    return () => window.clearTimeout(timer)
  }, [apiFetch, blocks, previewText, subject])

  const updateBlock = useCallback((next: EditorBlock) => {
    setBlocks((current) => current.map((b) => (b.key === next.key ? next : b)))
  }, [])

  function move(index: number, delta: number) {
    setBlocks((current) => {
      const target = index + delta
      if (target < 0 || target >= current.length) return current
      const out = [...current]
      ;[out[index], out[target]] = [out[target], out[index]]
      return out
    })
  }

  function toggleList(id: number) {
    setListIds((current) => (current.includes(id) ? current.filter((x) => x !== id) : [...current, id].sort((a, b) => a - b)))
  }

  async function save() {
    setSaving(true)
    setError(null)
    try {
      const payload = { subject, preview_text: previewText, list_ids: listIds, body_blocks: toDocument(blocks) }
      const res = campaign
        ? await apiFetch(`/v1/newsletter/campaigns/${campaign.id}`, {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
        : await apiFetch("/v1/newsletter/campaigns", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          })
      if (!res.ok) throw new Error(await readErrorMessage(res, `Could not save (${res.status})`))
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog className="max-w-6xl" onClose={onClose} title={campaign ? "Edit campaign" : "New campaign"}>
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1">
            <label className="text-sm font-medium" htmlFor={subjectId}>Subject</label>
            <input className="rounded-lg border border-border bg-background px-3 py-2 text-sm" id={subjectId} maxLength={255} onChange={(e) => setSubject(e.target.value)} value={subject} />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-sm font-medium" htmlFor={previewId}>Preview text</label>
            <input className="rounded-lg border border-border bg-background px-3 py-2 text-sm" id={previewId} maxLength={255} onChange={(e) => setPreviewText(e.target.value)} value={previewText} />
          </div>
          <fieldset className="flex flex-col gap-1">
            <legend className="mb-1 text-sm font-medium">Lists</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {lists.map((list) => (
                <label className="flex items-center gap-2 text-sm" key={list.id}>
                  <input checked={listIds.includes(list.id)} onChange={() => toggleList(list.id)} type="checkbox" />
                  {list.name}
                </label>
              ))}
            </div>
          </fieldset>

          <div className="flex flex-col gap-3">
            <p className="text-sm font-medium">Body</p>
            <p className="text-xs text-muted-foreground">The header and the unsubscribe footer come from the template and are always included.</p>
            {blocks.map((block, index) => (
              <div className="flex flex-col gap-2 rounded-lg border border-border p-3" key={block.key}>
                <div className="flex items-center justify-between">
                  <span className="text-xs font-semibold uppercase text-muted-foreground">{BLOCK_LABELS[block.type]}</span>
                  <div className="flex gap-1">
                    <button aria-label={`Move block ${index + 1} up`} className="p-1 hover:bg-muted rounded disabled:opacity-30" disabled={index === 0} onClick={() => move(index, -1)} type="button">
                      <ArrowUp className="h-4 w-4" />
                    </button>
                    <button aria-label={`Move block ${index + 1} down`} className="p-1 hover:bg-muted rounded disabled:opacity-30" disabled={index === blocks.length - 1} onClick={() => move(index, 1)} type="button">
                      <ArrowDown className="h-4 w-4" />
                    </button>
                    <button aria-label={`Remove block ${index + 1}`} className="p-1 hover:bg-muted rounded text-destructive" onClick={() => setBlocks((c) => c.filter((b) => b.key !== block.key))} type="button">
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                </div>
                <BlockFields block={block} onChange={updateBlock} />
              </div>
            ))}
            <div className="flex flex-wrap gap-2">
              {(Object.keys(BLOCK_LABELS) as BlockType[]).map((type) => (
                <button
                  className="rounded-lg border border-border px-3 py-1 text-sm hover:bg-muted"
                  key={type}
                  onClick={() => setBlocks((c) => [...c, emptyBlock(type)])}
                  type="button"
                >
                  {`Add ${BLOCK_LABELS[type].toLowerCase()}`}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="flex flex-col gap-3">
          <p className="text-sm font-medium">Preview</p>
          {previewError && <p className="text-sm text-destructive">{previewError}</p>}
          {rendered && rendered.warnings.length > 0 && (
            <ul className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm text-amber-800 dark:text-amber-300">
              {rendered.warnings.map((w) => <li key={w}>{w}</li>)}
            </ul>
          )}
          {rendered && rendered.articles.length > 0 && (
            <div className="flex flex-col gap-1">
              <p className="text-xs text-muted-foreground">Linked articles (locked to the article, so links follow renames):</p>
              <div className="flex flex-wrap gap-1">
                {rendered.articles.map((a) => (
                  <span className="rounded-full bg-muted px-2 py-0.5 text-xs" key={a.id} title={`Article ${a.id}`}>{`🔒 ${a.title}`}</span>
                ))}
              </div>
            </div>
          )}
          {rendered ? (
            // Empty sandbox: the preview can't run script, submit forms or
            // reach the dashboard's origin.
            <iframe className="h-[60vh] w-full rounded-lg border border-border bg-white" sandbox="" srcDoc={rendered.html} title="Newsletter preview" />
          ) : (
            <p className="flex items-center gap-2 text-sm text-muted-foreground"><Lock className="h-4 w-4" />Add a block to see the preview.</p>
          )}
        </div>
      </div>

      {error && <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2 text-sm text-destructive" role="alert">{error}</div>}
      <div className="flex justify-end gap-2">
        <button className="rounded-lg border border-border px-4 py-2 text-sm hover:bg-muted" onClick={onClose} type="button">Cancel</button>
        <button
          className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
          disabled={saving || subject.trim() === ""}
          onClick={() => void save()}
          type="button"
        >
          {saving ? "Saving…" : "Save draft"}
        </button>
      </div>
    </Dialog>
  )
}
