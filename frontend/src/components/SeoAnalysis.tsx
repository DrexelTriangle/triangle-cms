import { useMemo } from "react"
import { useYoastAnalysis } from "../seo/useYoastAnalysis"
import SeoAnalysisPanel from "./SeoAnalysisPanel"

export type SeoAnalysisProps = {
  content: string
  keyphrase: string
  title: string
  description: string
  slug: string
  permalink: string
}

// Yoast measures SEO-title width against the Google SERP font. Without a real
// pixel width the "SEO title width" assessment always reports a failure.
function measureTitleWidth(title: string): number {
  if (typeof document === "undefined" || title === "") return 0
  const canvas = document.createElement("canvas")
  const ctx = canvas.getContext("2d")
  if (!ctx) return 0
  ctx.font = "400 20px Arial"
  return Math.round(ctx.measureText(title).width)
}

// Trix only models a single heading level (`h1`), and legacy WordPress imports
// often mark section headings as a fully-bold paragraph instead of a real
// heading. Yoast's "Subheading distribution" check counts only real <h2>–<h6>,
// so we normalize a COPY of the body for analysis (this is never saved):
//   - body <h1> → <h2>
//   - a short (<100 char), wholly-bold <p><strong>…</strong></p> → <h2>
// Partial-bold paragraphs and long bold passages are left as emphasis.
function promoteSubheadings(html: string): string {
  if (typeof document === "undefined" || html === "") return html
  const doc = new DOMParser().parseFromString(html, "text/html")

  doc.querySelectorAll("h1").forEach((h1) => {
    const h2 = doc.createElement("h2")
    h2.innerHTML = h1.innerHTML
    h1.replaceWith(h2)
  })

  doc.querySelectorAll("p").forEach((p) => {
    const text = (p.textContent ?? "").trim()
    if (text === "" || text.length >= 100) return
    // Wholly bold: a single <strong>/<b> child whose text is the whole paragraph.
    const children = Array.from(p.children)
    const onlyChild = children.length === 1 ? children[0] : null
    const isBoldEl = onlyChild && /^(strong|b)$/i.test(onlyChild.tagName)
    if (isBoldEl && (onlyChild.textContent ?? "").trim() === text) {
      const h2 = doc.createElement("h2")
      h2.textContent = text
      p.replaceWith(h2)
    }
  })

  return doc.body.innerHTML
}

export default function SeoAnalysis({
  content,
  keyphrase,
  title,
  description,
  slug,
  permalink,
}: SeoAnalysisProps) {
  const text = useMemo(() => promoteSubheadings(content), [content])
  const titleWidth = useMemo(() => measureTitleWidth(title), [title])

  const { results, isReady, isAnalyzing, isStale, error, analyze } = useYoastAnalysis({
    text,
    keyphrase,
    title,
    titleWidth,
    description,
    slug,
    permalink,
  })

  // The worker keys SEO results by keyword identifier; the main keyword is "".
  const seo = results?.seo?.[""] ?? null
  const readability = results?.readability ?? null

  return (
    <SeoAnalysisPanel
      isReady={isReady}
      isAnalyzing={isAnalyzing}
      isStale={isStale}
      error={error}
      readability={readability}
      seo={seo}
      onRefresh={analyze}
    />
  )
}
