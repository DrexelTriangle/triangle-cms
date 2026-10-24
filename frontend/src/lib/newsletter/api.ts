import { useMemo } from "react"
import { useApiFetch } from "../../hooks/useApiFetch"
import { createMockNewsletterApi } from "./mockStore"
import type { Newsletter, NewsletterApi, NewsletterInput, NewsletterStats, NewsletterSubscriber } from "./types"

// Off until scheduled sending exists: the CRUD endpoints are live, and test
// sends work wherever the server has a sender (501 otherwise). Set
// VITE_NEWSLETTER_API=live to point the dashboard at /v1/newsletters instead
// of the in-browser mock.
const useLiveBackend = import.meta.env.VITE_NEWSLETTER_API === "live"

type ApiFetch = ReturnType<typeof useApiFetch>

const readError = async (response: Response, fallback: string) => {
  try {
    const body = (await response.json()) as { error?: string; message?: string }
    return body.error || body.message || `${fallback} (${response.status})`
  } catch {
    return `${fallback} (${response.status})`
  }
}

function createLiveNewsletterApi(apiFetch: ApiFetch): NewsletterApi {
  const request = async <T>(path: string, init: RequestInit | undefined, fallback: string): Promise<T> => {
    const response = await apiFetch(path, init)
    if (!response.ok) throw new Error(await readError(response, fallback))
    if (response.status === 204) return undefined as T
    return (await response.json()) as T
  }
  const json = (method: string, body: unknown): RequestInit => ({
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })

  return {
    backend: "live",
    list: async () =>
      (await request<{ newsletters?: Newsletter[] }>("/v1/newsletters", undefined, "Could not load newsletters"))
        .newsletters ?? [],
    get: (id) => request<Newsletter>(`/v1/newsletters/${id}`, undefined, "Could not load the newsletter"),
    create: (input: NewsletterInput) =>
      request<Newsletter>("/v1/newsletters", json("POST", input), "Could not create the newsletter"),
    update: (id, input) =>
      request<Newsletter>(`/v1/newsletters/${id}`, json("PUT", input), "Could not save the newsletter"),
    remove: (id) => request<void>(`/v1/newsletters/${id}`, { method: "DELETE" }, "Could not delete the newsletter"),
    schedule: (id, sendAt) =>
      request<Newsletter>(`/v1/newsletters/${id}/schedule`, json("POST", { send_at: sendAt }), "Could not schedule"),
    unschedule: (id) =>
      request<Newsletter>(`/v1/newsletters/${id}/schedule`, { method: "DELETE" }, "Could not unschedule"),
    sendTest: (id, emails) =>
      request<void>(`/v1/newsletters/${id}/test`, json("POST", { emails }), "Could not send the test"),
    stats: () => request<NewsletterStats>("/v1/newsletters/stats", undefined, "Could not load stats"),
    listSubscribers: async () =>
      (
        await request<{ subscribers?: NewsletterSubscriber[] }>(
          "/v1/newsletter-subscribers",
          undefined,
          "Could not load subscribers",
        )
      ).subscribers ?? [],
    removeSubscriber: (id) =>
      request<void>(`/v1/newsletter-subscribers/${id}`, { method: "DELETE" }, "Could not remove the subscriber"),
  }
}

export function useNewsletterApi(): NewsletterApi {
  const apiFetch = useApiFetch()
  return useMemo(
    () => (useLiveBackend ? createLiveNewsletterApi(apiFetch) : createMockNewsletterApi()),
    [apiFetch],
  )
}
