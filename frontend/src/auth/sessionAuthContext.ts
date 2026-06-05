import { createContext, useContext } from "react"

export type User = {
  id: number
  email: string
  name: string
  role: string
}

export type SessionAuthValue = {
  user: User | null
  isLoading: boolean
  isAuthenticated: boolean
  hasPendingAuthFlow: boolean
  refresh: () => Promise<void>
  login: () => void
  logout: () => Promise<void>
}

export const SessionAuthContext = createContext<SessionAuthValue | null>(null)

export function useSessionAuth() {
  const ctx = useContext(SessionAuthContext)
  if (!ctx) {
    throw new Error("useSessionAuth must be used within SessionAuthProvider")
  }
  return ctx
}
