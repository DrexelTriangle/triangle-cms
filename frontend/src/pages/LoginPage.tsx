import { ArrowRight } from "lucide-react"
import { useMemo } from "react"
import { useLocation } from "react-router-dom"
import { useSessionAuth } from "../auth/sessionAuthContext"

function LoginPage() {
  const auth = useSessionAuth()
  const location = useLocation()
  const authError = useMemo(() => {
    const error = new URLSearchParams(location.search).get("error")
    if (!error) return null
    const friendly: Record<string, string> = {
      missing_state: "Sign-in expired. Start again.",
      state_mismatch: "Sign-in expired. Start again.",
      missing_code: "The identity provider did not return a sign-in code.",
      token_exchange_failed: "Could not complete sign-in with the identity provider.",
      invalid_token: "The identity provider returned an invalid token.",
      invalid_claims: "Your account is missing required profile information.",
      user_error: "Could not load your CMS profile.",
      session_error: "Could not create your CMS session.",
    }
    return friendly[error] ?? "Sign-in failed. Try again or contact a web admin."
  }, [location.search])

  return (
    <div className="min-h-screen flex">

      <div className="flex-1 flex flex-col bg-background px-6 sm:px-12 py-8">

        <div className="flex items-center gap-2.5 mb-auto">
          <div className="w-7 h-7 rounded-lg bg-primary flex items-center justify-center">
            <span className="text-white font-bold text-sm leading-none">T</span>
          </div>
          <span className="font-semibold text-sm text-foreground tracking-normal">Delta CMS</span>
        </div>

        <div className="flex-1 flex flex-col justify-center max-w-sm mx-auto w-full py-16">
          <p className="text-xs font-bold text-primary uppercase tracking-normal mb-3">Triangle CMS</p>
          <h1 className="text-3xl font-bold text-foreground leading-tight mb-8">
            Sign in
          </h1>

          <button
            onClick={auth.login}
            className="w-full py-2.5 rounded-lg bg-primary text-white font-semibold text-sm hover:bg-primary/90 active:scale-[0.98] transition-all flex items-center justify-center gap-2"
          >
            Continue with Authentik
            <ArrowRight className="w-4 h-4" />
          </button>
          {authError && (
            <p className="mt-4 text-sm text-destructive text-center">{authError}</p>
          )}
        </div>

      </div>
    </div>
  )
}

export default LoginPage
