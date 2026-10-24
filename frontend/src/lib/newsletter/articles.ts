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
}

export type PickableArticle = TemplateArticle & {
  id: number
  published_date?: string
}

const toPickable = (item: ListedArticle): PickableArticle => ({
  id: item.id,
  title: item.title,
  slug: item.slug,
  excerpt: item.excerpt ?? "",
  image_url: item.featured_image || undefined,
  image_alt: item.featured_image_alt ?? item.photo_alt ?? "",
  published_date: item.published_date,
})

type Query = {
  limit: number
  title?: string
  sectionSlug?: string
  signal?: AbortSignal
}

// Published stories, newest first: the only ones a newsletter should link to.
// Developing stories are left out; their landing pages aren't articles.
export async function fetchPublishedArticles(apiFetch: ApiFetch, query: Query): Promise<PickableArticle[]> {
  const params = new URLSearchParams({
    limit: String(query.limit),
    page: "1",
    status: "published",
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

const COMICS_SECTION = "comics-puzzles"

// The newest comic with art to show. Comics & Puzzles also holds word searches
// and other text-only posts, so skip anything without a featured image.
export async function fetchLatestComic(apiFetch: ApiFetch, signal?: AbortSignal): Promise<TemplateComic | null> {
  const items = await fetchPublishedArticles(apiFetch, { limit: 10, sectionSlug: COMICS_SECTION, signal })
  const comic = items.find((item) => item.image_url && !/word search/i.test(item.title))
  if (!comic?.image_url) return null
  return { title: comic.title, slug: comic.slug, image_url: comic.image_url, image_alt: comic.image_alt }
}
