import type { Newsletter, NewsletterApi, NewsletterInput, NewsletterSubscriber } from "./types"

// A browser-only stand-in for /v1/newsletters, so the editor can be built and
// used before the backend exists. Drafts live in this browser's localStorage
// and nowhere else. Anything that would reach a real inbox refuses instead of
// pretending to have worked.

const STORAGE_KEY = "delta.newsletters.v1"

export class NewsletterBackendPendingError extends Error {
  constructor(action: string) {
    super(`${action} needs the newsletter backend, which isn't connected yet.`)
    this.name = "NewsletterBackendPendingError"
  }
}

type StoredState = {
  nextId: number
  newsletters: Newsletter[]
}

const emptyState = (): StoredState => ({ nextId: 1, newsletters: [] })

const readState = (): StoredState => {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return emptyState()
    const parsed = JSON.parse(raw) as Partial<StoredState>
    if (!Array.isArray(parsed.newsletters) || typeof parsed.nextId !== "number") return emptyState()
    return { nextId: parsed.nextId, newsletters: parsed.newsletters }
  } catch {
    return emptyState()
  }
}

const writeState = (state: StoredState) => {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch {
    // Private windows and full quotas: the edit stays in memory for this page.
  }
}

// The subscriber list is the backend's to own; this only gives the tab
// something to render.
const SAMPLE_SUBSCRIBERS: NewsletterSubscriber[] = [
  { id: 1, email: "jsmith@drexel.edu", name: "John Smith", status: "confirmed", lists: [1], subscribed_at: "2024-09-03" },
  { id: 2, email: "mgarcia@drexel.edu", name: "Maria Garcia", status: "confirmed", lists: [2], subscribed_at: "2024-09-05" },
  { id: 3, email: "rkim2025@drexel.edu", name: "Rachel Kim", status: "confirmed", lists: [], subscribed_at: "2024-10-20" },
]

const notFound = (id: number) => new Error(`Newsletter ${id} not found`)

export function createMockNewsletterApi(): NewsletterApi {
  const find = (state: StoredState, id: number) => {
    const found = state.newsletters.find((n) => n.id === id)
    if (!found) throw notFound(id)
    return found
  }

  return {
    backend: "mock",

    async list() {
      return [...readState().newsletters].sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    },

    async get(id) {
      return find(readState(), id)
    },

    async create(input: NewsletterInput) {
      const state = readState()
      const now = new Date().toISOString()
      const created: Newsletter = {
        ...input,
        id: state.nextId,
        status: "draft",
        scheduled_for: null,
        sent_at: null,
        recipient_count: 0,
        open_count: 0,
        created_at: now,
        updated_at: now,
      }
      writeState({ nextId: state.nextId + 1, newsletters: [...state.newsletters, created] })
      return created
    },

    async update(id, input) {
      const state = readState()
      const existing = find(state, id)
      if (existing.status === "sent" || existing.status === "sending") {
        throw new Error("A sent newsletter can't be edited.")
      }
      const updated: Newsletter = { ...existing, ...input, updated_at: new Date().toISOString() }
      writeState({ ...state, newsletters: state.newsletters.map((n) => (n.id === id ? updated : n)) })
      return updated
    },

    async remove(id) {
      const state = readState()
      const existing = find(state, id)
      if (existing.status === "sent" || existing.status === "sending") {
        throw new Error("A sent newsletter can't be deleted.")
      }
      writeState({ ...state, newsletters: state.newsletters.filter((n) => n.id !== id) })
    },

    async schedule() {
      throw new NewsletterBackendPendingError("Scheduling")
    },

    async unschedule() {
      throw new NewsletterBackendPendingError("Scheduling")
    },

    async sendTest() {
      throw new NewsletterBackendPendingError("Sending a test")
    },

    async stats() {
      const sent = readState().newsletters.filter((n) => n.status === "sent")
      const withRecipients = sent.filter((n) => n.recipient_count > 0)
      return {
        subscriber_count: SAMPLE_SUBSCRIBERS.length,
        sent_count: sent.length,
        recipients_reached: sent.reduce((sum, n) => sum + n.recipient_count, 0),
        avg_open_rate: withRecipients.length
          ? withRecipients.reduce((sum, n) => sum + n.open_count / n.recipient_count, 0) / withRecipients.length
          : null,
      }
    },

    async listSubscribers() {
      return SAMPLE_SUBSCRIBERS
    },

    async removeSubscriber() {
      throw new NewsletterBackendPendingError("Removing a subscriber")
    },
  }
}
