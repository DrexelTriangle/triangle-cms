// Shapes returned by /v1/newsletter/* (server/internal/models/newsletter.go).

export type CampaignStatus = "draft" | "scheduled" | "sent"
export type SubscriberStatus = "subscribed" | "unsubscribed"

export type NewsletterList = {
  id: number
  name: string
  description: string
  is_public: boolean
  subscribed_count: number
}

export type NewsletterStats = {
  subscribers: Partial<Record<SubscriberStatus | "all", number>>
  campaigns: Partial<Record<CampaignStatus | "all", number>>
  lists: NewsletterList[]
}

export type Campaign = {
  id: number
  subject: string
  preview_text: string
  status: CampaignStatus
  list_ids: number[]
  body_blocks?: BlockDocument
  sent_at?: string
  recipient_count?: number
  updated_at?: string
  updated_by?: string
}

export type Subscriber = {
  id: number
  email: string
  name: string
  status: SubscriberStatus
  source: string
  list_ids: number[]
  subscribed_at?: string
  unsubscribed_at?: string
}

// The block document stored in newsletter_campaigns.body_blocks
// (server/internal/newsletter/blocks.go). The header (logo, social links) and
// footer (unsubscribe) are fixed template parts, so they are not blocks.
export type Block =
  | { type: "text"; html: string }
  | { type: "heading"; text: string }
  | { type: "button"; label: string; href: string }
  | { type: "article"; article_id: number; show_image: boolean; show_excerpt: boolean }
  | { type: "image"; src: string; alt?: string; href?: string }
  | { type: "divider" }

export type BlockType = Block["type"]

export type BlockDocument = { version: 1; blocks: Block[] }

// Editor-side block: a stable key for React plus, for article blocks, the
// title the picker showed. Neither is sent to the server.
export type EditorBlock = Block & { key: string; title?: string }

export type RenderResult = {
  html: string
  warnings: string[]
  articles: Array<{ id: number; title: string; published: boolean }>
}

export async function readErrorMessage(res: Response, fallback: string) {
  try {
    const body = (await res.json()) as { error?: string }
    return body.error?.trim() || fallback
  } catch {
    return fallback
  }
}

export function formatDate(value?: string) {
  if (!value) return "—"
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return "—"
  return parsed.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })
}

export function listNames(ids: number[], lists: NewsletterList[]) {
  const byId = new Map(lists.map((l) => [l.id, l.name]))
  return ids.map((id) => byId.get(id) ?? `List ${id}`)
}
