import { useEffect } from "react"
import { useNavigate } from "react-router-dom"
import { useSessionAuth } from "../auth/sessionAuthContext"
import { authBaseUrl } from "../auth/urls"

export default function AuthCallback() {
  const auth = useSessionAuth()
  const navigate = useNavigate()

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get("code")
    const state = params.get("state")
    if (code && state) {
      window.location.replace(`${authBaseUrl()}/v1/auth/callback?${params.toString()}`)
      return
    }

    if (!auth.isLoading && auth.isAuthenticated) {
      navigate("/", { replace: true })
      return
    }
    if (!auth.isLoading && !auth.isAuthenticated) {
      navigate("/login", { replace: true })
    }
  }, [auth.isAuthenticated, auth.isLoading, navigate])

  return (
    <div className="min-h-screen flex items-center justify-center">
      <p className="text-sm text-muted-foreground">Signing you in...</p>
    </div>
  )
}
