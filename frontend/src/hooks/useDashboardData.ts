import { useEffect, useState } from "react"
import { useApiFetch } from "./useApiFetch"

export type RecentArticle = {
  id: number
  title: string
  slug: string
  status: "published" | "draft" | "scheduled"
  authors: { name: string }[]
  categories: { name: string }[]
  published_date: string | null
}

type Counts = {
  totalArticles: number | null
  publishedArticles: number | null
  draftArticles: number | null
  totalAuthors: number | null
  totalSections: number | null
}

type CountResponse = { pagination: { total_count: number } }
type ArticleResponse = CountResponse & { articles: RecentArticle[] }

export function useDashboardData() {
  const apiFetch = useApiFetch()
  const [recentArticles, setRecentArticles] = useState<RecentArticle[]>([])
  const [apiHealth, setApiHealth] = useState<"ok" | "error" | "checking">("checking")
  const [stats, setStats] = useState<Counts>({
    totalArticles: null, publishedArticles: null, draftArticles: null,
    totalAuthors: null, totalSections: null,
  })

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    function load<T>(url: string, receive: (data: T) => void, failed?: () => void) {
      void apiFetch(url, { signal })
        .then(async (response) => {
          if (!response.ok) throw new Error(`Request failed (${response.status})`)
          return await response.json() as T
        })
        .then((data) => { if (!signal.aborted) receive(data) })
        .catch(() => { if (!signal.aborted) failed?.() })
    }

    load<ArticleResponse>("/v1/articles?limit=10", (data) => {
      setRecentArticles(data.articles)
      setStats((current) => ({ ...current, totalArticles: data.pagination.total_count }))
    })
    for (const [status, key] of [["published", "publishedArticles"], ["draft", "draftArticles"]] as const) {
      load<CountResponse>(`/v1/articles?status=${status}&limit=1`, (data) => {
        setStats((current) => ({ ...current, [key]: data.pagination.total_count }))
      })
    }
    load<CountResponse>("/v1/authors?limit=1", (data) => {
      setStats((current) => ({ ...current, totalAuthors: data.pagination.total_count }))
    })
    load<unknown[]>("/v1/taxonomy?type=section", (data) => {
      setStats((current) => ({ ...current, totalSections: data.length }))
    })
    load("/v1/health/db", () => setApiHealth("ok"), () => setApiHealth("error"))
    return () => controller.abort()
  }, [apiFetch])

  return { recentArticles, stats, apiHealth }
}
