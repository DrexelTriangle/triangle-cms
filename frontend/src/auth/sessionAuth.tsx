import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"
import { apiBaseUrl, authBaseUrl } from "./urls"

type User = {
  id: number
  email: string
  name: string
  role: string
}

type SessionAuthValue = {
  user: User | null
  isLoading: boolean
  isAuthenticated: boolean
  hasPendingAuthFlow: boolean
  refresh: () => Promise<void>
  login: () => void
  logout: () => Promise<void>
}

const SessionAuthContext = createContext<SessionAuthValue | null>(null)

async function fetchCurrentUser() {
  const res = await fetch(`${apiBaseUrl()}/v1/users/me`, { credentials: "include" })
  if (!res.ok) return null
  return (await res.json()) as User
}

export function SessionAuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [hasPendingAuthFlow, setHasPendingAuthFlow] = useState(
    () => window.sessionStorage.getItem("cms_auth_in_progress") === "1",
  )

  const refresh = useCallback(async () => {
    try {
      let nextUser = await fetchCurrentUser()
      const pending = window.sessionStorage.getItem("cms_auth_in_progress") === "1"
      if (!nextUser && pending) {
        await new Promise((resolve) => setTimeout(resolve, 350))
        nextUser = await fetchCurrentUser()
      }
      setUser(nextUser)
      if (nextUser) {
        window.sessionStorage.removeItem("cms_auth_in_progress")
        setHasPendingAuthFlow(false)
      } else if (pending) {
        window.sessionStorage.removeItem("cms_auth_in_progress")
        setHasPendingAuthFlow(false)
      }
    } finally {
      setIsLoading(false)
    }
  }, [])

  const login = useCallback(() => {
    window.sessionStorage.setItem("cms_auth_in_progress", "1")
    setHasPendingAuthFlow(true)
    window.location.href = `${authBaseUrl()}/v1/auth/login`
  }, [])

  const logout = useCallback(async () => {
    try {
      await fetch(`${apiBaseUrl()}/v1/auth/logout`, {
        method: "POST",
        credentials: "include",
      })
    } finally {
      setUser(null)
      window.sessionStorage.removeItem("cms_auth_in_progress")
      setHasPendingAuthFlow(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const value = useMemo<SessionAuthValue>(() => ({
    user,
    isLoading,
    isAuthenticated: user !== null,
    hasPendingAuthFlow,
    refresh,
    login,
    logout,
  }), [hasPendingAuthFlow, isLoading, login, logout, refresh, user])

  return <SessionAuthContext.Provider value={value}>{children}</SessionAuthContext.Provider>
}

export function useSessionAuth() {
  const ctx = useContext(SessionAuthContext)
  if (!ctx) {
    throw new Error("useSessionAuth must be used within SessionAuthProvider")
  }
  return ctx
}
