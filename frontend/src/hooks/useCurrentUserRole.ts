import { useEffect, useState } from "react"
import { useApiFetch } from "./useApiFetch"

export function useCurrentUserRole() {
  const apiFetch = useApiFetch()
  const [role, setRole] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false

    apiFetch("/v1/users/me")
      .then((res) => {
        if (!res.ok) throw new Error(`Request failed (${res.status})`)
        return res.json() as Promise<{ role?: string }>
      })
      .then((body) => {
        if (!cancelled) {
          setRole(String(body.role ?? "").trim().toLowerCase() || null)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRole(null)
        }
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false)
        }
      })

    return () => {
      cancelled = true
    }
  }, [apiFetch])

  return { role, isLoading, isAdmin: role === "admin" }
}
