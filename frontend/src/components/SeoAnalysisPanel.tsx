import { useState } from "react"
import { ChevronDown, Frown, Meh, RefreshCw, Smile } from "lucide-react"

// Map a Yoast 0–9 score to a rating bucket. Mirrors yoastseo's scoreToRating:
// <=4 bad, 5–7 ok, >7 good, 0 informational feedback.
function ratingFor(score: number): "good" | "ok" | "bad" | "feedback" {
  if (score > 7) return "good"
  if (score > 4) return "ok"
  if (score > 0) return "bad"
  return "feedback"
}

const RATING_FACE = {
  good: { Icon: Smile, className: "text-emerald-500" },
  ok: { Icon: Meh, className: "text-amber-500" },
  bad: { Icon: Frown, className: "text-red-500" },
  feedback: { Icon: Meh, className: "text-muted-foreground" },
} as const

type Assessment = { identifier: string; score: number; text: string }

function AssessmentList({ results }: { results: Assessment[] }) {
  if (results.length === 0) {
    return <p className="px-1 py-2 text-xs text-muted-foreground">No checks to report yet.</p>
  }
  // Worst-first so the most actionable issues surface at the top.
  const ordered = [...results].sort((a, b) => a.score - b.score)
  return (
    <ul className="space-y-2">
      {ordered.map((result, index) => {
        const { Icon, className } = RATING_FACE[ratingFor(result.score)]
        return (
          <li key={result.identifier || index} className="flex items-start gap-2">
            <Icon className={`mt-0.5 w-4 h-4 shrink-0 ${className}`} aria-hidden="true" />
            {/* Yoast feedback contains markup (links, <strong>); render as-is. */}
            <span
              className="text-xs leading-relaxed text-foreground [&_a]:text-primary [&_a]:underline"
              dangerouslySetInnerHTML={{ __html: result.text }}
            />
          </li>
        )
      })}
    </ul>
  )
}

type Section = {
  title: string
  score: number
  results: Assessment[]
}

function Section({ section }: { section: Section }) {
  const [open, setOpen] = useState(true)
  const { Icon, className } = RATING_FACE[ratingFor(section.score)]
  return (
    <div className="rounded-lg border border-border">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left"
        aria-expanded={open}
      >
        <span className="flex items-center gap-2 text-sm font-medium text-foreground">
          <Icon className={`w-4 h-4 ${className}`} aria-hidden="true" />
          {section.title}
        </span>
        <ChevronDown
          className={`w-4 h-4 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
          aria-hidden="true"
        />
      </button>
      {open && (
        <div className="border-t border-border px-3 py-3">
          <AssessmentList results={section.results} />
        </div>
      )}
    </div>
  )
}

export type SeoAnalysisPanelProps = {
  isReady: boolean
  isAnalyzing: boolean
  isStale: boolean
  error: string | null
  readability: { score: number; results: Assessment[] } | null
  seo: { score: number; results: Assessment[] } | null
  onRefresh: () => void
}

export default function SeoAnalysisPanel({
  isReady,
  isAnalyzing,
  isStale,
  error,
  readability,
  seo,
  onRefresh,
}: SeoAnalysisPanelProps) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">SEO analysis</h3>
        <button
          type="button"
          onClick={onRefresh}
          disabled={!isReady || isAnalyzing}
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${isAnalyzing ? "animate-spin" : ""}`} aria-hidden="true" />
          {isAnalyzing ? "Analyzing…" : "Refresh"}
        </button>
      </div>

      {error ? (
        <p className="rounded-lg bg-red-500/10 px-3 py-2 text-xs text-red-500">{error}</p>
      ) : !isReady ? (
        <p className="text-xs text-muted-foreground">Loading SEO analysis…</p>
      ) : !readability && !seo ? (
        <p className="text-xs text-muted-foreground">Add some content, then refresh to score it.</p>
      ) : (
        <div className="space-y-3">
          {isStale && (
            <p className="rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-600">
              Content changed since the last run — refresh to update the scores.
            </p>
          )}
          {seo && <Section section={{ title: "SEO", score: seo.score, results: seo.results }} />}
          {readability && (
            <Section section={{ title: "Readability", score: readability.score, results: readability.results }} />
          )}
        </div>
      )}
    </div>
  )
}
