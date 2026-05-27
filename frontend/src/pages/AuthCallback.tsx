import { useEffect } from "react"
import { useAuth } from "react-oidc-context"
import { useNavigate } from "react-router-dom"

export default function AuthCallback() {
  const auth = useAuth()
  const navigate = useNavigate()

  useEffect(() => {
    if (!auth.isLoading && !auth.error) {
      navigate("/", { replace: true })
    }
  }, [auth.isLoading, auth.error, navigate])

  if (auth.error) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-center">
          <p className="text-destructive font-semibold mb-2">Authentication failed</p>
          <p className="text-sm text-muted-foreground">{auth.error.message}</p>
          <button
            className="mt-4 text-sm text-primary hover:underline"
            onClick={() => navigate("/login", { replace: true })}
          >
            Back to login
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center">
      <p className="text-sm text-muted-foreground">Signing you in…</p>
    </div>
  )
}
