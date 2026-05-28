import { useCallback } from "react"
import { useAuth } from "react-oidc-context"

export function useApiFetch() {
  const { user } = useAuth()
  const token = user?.id_token

  return useCallback((url: string, init?: RequestInit): Promise<Response> => {
    const headers: Record<string, string> = {
      ...(init?.headers as Record<string, string>),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    }
    return fetch(url, { ...init, headers })
  }, [token])
}
