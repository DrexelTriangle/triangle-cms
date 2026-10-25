import { useEffect, useState } from "react"
import { ImageOff, Search, X } from "lucide-react"
import { useApiFetch } from "../../hooks/useApiFetch"
import { COMICS_SECTION, fetchPickableArticles, toComic, type PickableArticle } from "../../lib/newsletter/articles"
import { decodeEntities } from "../../lib/newsletter/template"

type ArticlePickerProps = {
  title?: string
  // Already in the issue; shown as added rather than offered twice.
  excludeIds?: number[]
  // Only comics with art (see toComic).
  comics?: boolean
  onSelect: (article: PickableArticle) => void
  onClose: () => void
}

const formatDate = (value?: string) =>
  value ? new Date(value).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" }) : ""

export default function ArticlePicker({
  title = "Insert an article",
  excludeIds = [],
  comics = false,
  onSelect,
  onClose,
}: ArticlePickerProps) {
  const apiFetch = useApiFetch()
  const [query, setQuery] = useState("")
  const [results, setResults] = useState<PickableArticle[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    // Debounced so each keystroke doesn't hit the search.
    const timer = window.setTimeout(() => {
      setIsLoading(true)
      setError(null)
      fetchPickableArticles(apiFetch, {
        limit: 20,
        title: query,
        sectionSlug: comics ? COMICS_SECTION : undefined,
        signal: controller.signal,
      })
        .then((items) => setResults(comics ? items.filter((item) => toComic(item)) : items))
        .catch((err: unknown) => {
          if (controller.signal.aborted) return
          setError(err instanceof Error ? err.message : "Could not load articles.")
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsLoading(false)
        })
    }, query ? 250 : 0)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [apiFetch, comics, query])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  return (
    <div
      aria-labelledby="article-picker-title"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      role="dialog"
    >
      <div className="flex max-h-[80vh] w-full max-w-xl flex-col overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-4">
          <h2 className="text-base font-semibold text-foreground" id="article-picker-title">{title}</h2>
          <button
            aria-label="Close"
            className="rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            onClick={onClose}
            type="button"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="border-b border-border px-5 py-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <input
              autoFocus
              className="w-full rounded-lg border border-border bg-background py-2 pl-9 pr-4 text-sm placeholder:text-muted-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
              onChange={(e) => setQuery(e.target.value)}
              placeholder={comics ? "Search comics by title" : "Search published articles by title"}
              value={query}
            />
          </div>
        </div>
        <div className="flex-1 overflow-y-auto">
          {error ? (
            <p className="px-5 py-8 text-center text-sm text-destructive">{error}</p>
          ) : isLoading && results.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-muted-foreground">Loading articles...</p>
          ) : results.length === 0 ? (
            <p className="px-5 py-8 text-center text-sm text-muted-foreground">
              {comics ? "No comics match." : "No published articles match."}
            </p>
          ) : (
            <ul>
              {results.map((article) => {
                const added = excludeIds.includes(article.id)
                return (
                  <li className="border-b border-border last:border-0" key={article.id}>
                    <button
                      className="flex w-full items-center gap-3 px-5 py-3 text-left transition-colors hover:bg-muted/40 disabled:cursor-not-allowed disabled:opacity-50"
                      disabled={added}
                      onClick={() => onSelect(article)}
                      type="button"
                    >
                      {article.image_url ? (
                        <img
                          alt=""
                          className="h-12 w-16 flex-shrink-0 rounded-md object-cover"
                          referrerPolicy="no-referrer"
                          src={article.image_url}
                        />
                      ) : (
                        <span className="flex h-12 w-16 flex-shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                          <ImageOff className="h-4 w-4" />
                        </span>
                      )}
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium text-foreground">
                          {decodeEntities(article.title)}
                        </span>
                        <span className="block text-xs text-muted-foreground">
                          {added
                            ? "Already in this issue"
                            : article.scheduled
                              ? `Scheduled for ${formatDate(article.published_date)}`
                              : formatDate(article.published_date)}
                        </span>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
