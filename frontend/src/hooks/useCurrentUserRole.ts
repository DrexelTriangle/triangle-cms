import { useSessionAuth } from "../auth/sessionAuthContext"

export function useCurrentUserRole() {
  const { user, isLoading } = useSessionAuth()
  const role = user?.role?.trim().toLowerCase() ?? null
  return { role, isLoading, isAdmin: role === "admin" }
}
