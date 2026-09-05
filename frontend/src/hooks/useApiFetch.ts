import { useCallback } from "react"
import { apiBaseUrl } from "../auth/urls"

export function useApiFetch() {
  const baseUrl = apiBaseUrl()

  return useCallback(async (url: string, init?: RequestInit): Promise<Response> => {
    const target = url.startsWith("http://") || url.startsWith("https://")
      ? url
      : `${baseUrl}${url}`

    return fetch(target, { ...init, credentials: "include" })
  }, [baseUrl])
}
