import { useEffect, useId, useState } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"

type ArticleHit = { id: number; title: string; slug: string }

type ArticlePickerProps = {
  onPick: (article: { id: number; title: string }) => void
}

const SEARCH_DELAY_MS = 250

// Searches the CMS's articles by title. Picking one stores only its id: the
// newsletter resolves the live URL from the id when it is rendered or sent,
// so a later slug change cannot break the link.
export default function ArticlePicker({ onPick }: ArticlePickerProps) {
  const apiFetch = useApiFetch()
  const inputId = useId()
  const [query, setQuery] = useState("")
  const [hits, setHits] = useState<ArticleHit[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const q = query.trim()
    if (q.length < 2) {
      setHits([])
      return
    }
    let cancelled = false
    const timer = window.setTimeout(async () => {
      try {
        const res = await apiFetch(`/v1/articles?title=${encodeURIComponent(q)}&limit=8`)
        if (!res.ok) throw new Error(`Search failed (${res.status})`)
        const body = (await res.json()) as { articles?: ArticleHit[] }
        if (!cancelled) {
          setHits(body.articles ?? [])
          setError(null)
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Search failed")
      }
    }, SEARCH_DELAY_MS)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [apiFetch, query])

  return (
    <div className="flex flex-col gap-2">
      <label className="text-xs font-medium text-muted-foreground" htmlFor={inputId}>Search articles</label>
      <input
        className="rounded-lg border border-border bg-background px-3 py-1.5 text-sm"
        id={inputId}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Type at least two letters of the title"
        value={query}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      {hits.length > 0 && (
        <ul className="flex flex-col rounded-lg border border-border">
          {hits.map((hit) => (
            <li key={hit.id}>
              <button
                className="w-full px-3 py-1.5 text-left text-sm hover:bg-muted"
                onClick={() => onPick({ id: hit.id, title: hit.title })}
                type="button"
              >
                {hit.title}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
