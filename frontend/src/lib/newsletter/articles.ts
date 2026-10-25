import type { useApiFetch } from "../../hooks/useApiFetch"
import type { TemplateArticle, TemplateComic } from "./template"

type ApiFetch = ReturnType<typeof useApiFetch>

type ListedArticle = {
  id: number
  title: string
  slug: string
  excerpt?: string
  published_date?: string
  featured_image?: string
  featured_image_alt?: string
  photo_alt?: string
  status?: string
}

export type PickableArticle = TemplateArticle & {
  id: number
  // For a scheduled article, when it is due to go up.
  published_date?: string
  scheduled?: boolean
}

const toPickable = (item: ListedArticle): PickableArticle => ({
  id: item.id,
  title: item.title,
  slug: item.slug,
  excerpt: item.excerpt ?? "",
  image_url: item.featured_image || undefined,
  image_alt: item.featured_image_alt ?? item.photo_alt ?? "",
  published_date: item.published_date,
  scheduled: item.status === "scheduled",
})

type Query = {
  limit: number
  title?: string
  sectionSlug?: string
  signal?: AbortSignal
}

const fetchByStatus = async (
  apiFetch: ApiFetch,
  status: "published" | "scheduled",
  query: Query,
): Promise<PickableArticle[]> => {
  const params = new URLSearchParams({
    limit: String(query.limit),
    page: "1",
    status,
    sort_by: "published_date",
    sort_direction: "desc",
    exclude_type: "developing-stories",
  })
  if (query.title?.trim()) params.set("title", query.title.trim())
  if (query.sectionSlug) params.set("section_slug", query.sectionSlug)
  const response = await apiFetch(`/v1/articles?${params.toString()}`, { signal: query.signal })
  if (!response.ok) throw new Error(`Could not load articles (${response.status})`)
  const payload = (await response.json()) as { articles?: ListedArticle[] }
  return (payload.articles ?? []).map(toPickable)
}

// Published stories, newest first: the only ones a newsletter should link to.
// Developing stories are left out; their landing pages aren't articles.
export async function fetchPublishedArticles(apiFetch: ApiFetch, query: Query): Promise<PickableArticle[]> {
  return fetchByStatus(apiFetch, "published", query)
}

// What the picker offers: stories scheduled to go up (soonest first, since an
// issue often goes out alongside them), then published ones, newest first. A
// scheduled story's link works once it publishes, which is on the editor.
export async function fetchPickableArticles(apiFetch: ApiFetch, query: Query): Promise<PickableArticle[]> {
  const [scheduled, published] = await Promise.all([
    fetchByStatus(apiFetch, "scheduled", query),
    fetchByStatus(apiFetch, "published", query),
  ])
  return [...scheduled.reverse(), ...published]
}

export const COMICS_SECTION = "comics-puzzles"

// A Comics & Puzzles post as the issue's comic, or null if it has no art to
// show: the section also holds word searches and other text-only posts.
export const toComic = (item: PickableArticle): TemplateComic | null =>
  item.image_url && !/word search/i.test(item.title)
    ? { title: item.title, slug: item.slug, image_url: item.image_url, image_alt: item.image_alt }
    : null

// The newest comic post with art to show.
export async function fetchLatestComic(apiFetch: ApiFetch, signal?: AbortSignal): Promise<PickableArticle | null> {
  const items = await fetchPublishedArticles(apiFetch, { limit: 10, sectionSlug: COMICS_SECTION, signal })
  return items.find((item) => toComic(item)) ?? null
}
