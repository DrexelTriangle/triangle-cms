import { useCallback } from "react"
import { apiBaseUrl } from "../auth/urls"
import { useAdminOnlyNotice } from "../auth/adminOnlyNoticeContext"

export function useApiFetch() {
  const baseUrl = apiBaseUrl()
  const adminOnlyNotice = useAdminOnlyNotice()

  return useCallback(async (url: string, init?: RequestInit): Promise<Response> => {
    const target = url.startsWith("http://") || url.startsWith("https://")
      ? url
      : `${baseUrl}${url}`

    const response = await fetch(target, { ...init, credentials: "include" })

    // A 403 on a write means the caller is an editor and the route is
    // admin-only, which the server's bare "forbidden" explains to nobody. Reads
    // are left alone: the pages that fetch admin-only data are already behind
    // AdminOnlyRoute, so a 403 there is a background poll rather than something
    // the user just clicked, and a modal would be baffling.
    const method = (init?.method ?? "GET").toUpperCase()
    if (response.status === 403 && method !== "GET" && method !== "HEAD") {
      adminOnlyNotice.show()
    }

    return response
  }, [baseUrl, adminOnlyNotice])
}
