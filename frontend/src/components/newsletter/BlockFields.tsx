import { useCallback, useId } from "react"
import TrixEditor from "../TrixEditor"
import ArticlePicker from "./ArticlePicker"
import type { EditorBlock } from "./types"

type BlockFieldsProps = {
  block: EditorBlock
  onChange: (next: EditorBlock) => void
}

const inputClass = "rounded-lg border border-border bg-background px-3 py-1.5 text-sm"

function Field({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  const id = useId()
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs font-medium text-muted-foreground" htmlFor={id}>{label}</label>
      <input className={inputClass} id={id} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} value={value} />
    </div>
  )
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input checked={checked} onChange={(e) => onChange(e.target.checked)} type="checkbox" />
      {label}
    </label>
  )
}

// Fields for one block. Each block type maps to a row of the old WordPress
// "To The Point" template; the server renders it with that row's markup.
export default function BlockFields({ block, onChange }: BlockFieldsProps) {
  // TrixEditor re-subscribes when onChange changes identity, so keep it stable
  // per block.
  const key = block.key
  const onTextChange = useCallback((html: string) => onChange({ key, type: "text", html }), [key, onChange])

  switch (block.type) {
    case "text":
      return <TrixEditor onChange={onTextChange} value={block.html} />
    case "heading":
      return <Field label="Heading text" onChange={(text) => onChange({ ...block, text })} value={block.text} />
    case "button":
      return (
        <div className="grid gap-2 sm:grid-cols-2">
          <Field label="Button label" onChange={(label) => onChange({ ...block, label })} value={block.label} />
          <Field
            label="Button link"
            onChange={(href) => onChange({ ...block, href })}
            placeholder="https://www.thetriangle.org/article/…"
            value={block.href}
          />
        </div>
      )
    case "image":
      return (
        <div className="grid gap-2 sm:grid-cols-3">
          <Field label="Image URL" onChange={(src) => onChange({ ...block, src })} value={block.src} />
          <Field label="Alt text" onChange={(alt) => onChange({ ...block, alt })} value={block.alt ?? ""} />
          <Field label="Image link" onChange={(href) => onChange({ ...block, href })} value={block.href ?? ""} />
        </div>
      )
    case "divider":
      return <p className="text-xs text-muted-foreground">A horizontal rule.</p>
    case "article":
      return (
        <div className="flex flex-col gap-2">
          {block.article_id > 0 ? (
            <div className="flex items-center justify-between gap-2 rounded-lg bg-muted px-3 py-1.5 text-sm">
              <span>
                {block.title ?? `Article ${block.article_id}`}
                <span className="ml-2 text-xs text-muted-foreground">linked by id, follows renames</span>
              </span>
              <button
                className="text-xs text-primary hover:underline"
                onClick={() => onChange({ ...block, article_id: 0, title: undefined })}
                type="button"
              >
                Change
              </button>
            </div>
          ) : (
            <ArticlePicker onPick={({ id, title }) => onChange({ ...block, article_id: id, title })} />
          )}
          <div className="flex gap-4">
            <Toggle checked={block.show_image} label="Show image" onChange={(show_image) => onChange({ ...block, show_image })} />
            <Toggle checked={block.show_excerpt} label="Show excerpt" onChange={(show_excerpt) => onChange({ ...block, show_excerpt })} />
          </div>
        </div>
      )
  }
}
