import { useAuth } from "react-oidc-context"
import { ArrowRight } from "lucide-react"

function LoginPage() {
  const auth = useAuth()

  return (
    <div className="min-h-screen flex">
      {/* Form panel */}
      <div className="flex-1 flex flex-col bg-white px-12 py-8">
        {/* Logo */}
        <div className="flex items-center gap-2.5 mb-auto">
          <div className="w-7 h-7 rounded-lg bg-primary flex items-center justify-center">
            <span className="text-white font-bold text-sm leading-none">T</span>
          </div>
          <span className="font-semibold text-sm text-foreground tracking-tight">Delta CMS</span>
        </div>

        {/* Content */}
        <div className="flex-1 flex flex-col justify-center max-w-sm mx-auto w-full py-16">
          <p className="text-xs font-bold text-primary uppercase tracking-[0.18em] mb-3">Welcome back</p>
          <h1 className="text-3xl font-bold text-foreground leading-tight mb-2">
            Sign in to your account.
          </h1>
          <p className="text-sm text-muted-foreground mb-10">
            The Triangle's editorial platform. Publish, manage, and grow.
          </p>

          <button
            onClick={() => auth.signinRedirect()}
            className="w-full py-2.5 rounded-xl bg-primary text-white font-semibold text-sm hover:bg-primary/90 active:scale-[0.98] transition-all flex items-center justify-center gap-2"
          >
            Continue with Authentik
            <ArrowRight className="w-4 h-4" />
          </button>

          {auth.error && (
            <p className="mt-4 text-sm text-destructive text-center">{auth.error.message}</p>
          )}
        </div>

        {/* Footer */}
        <p className="text-xs text-muted-foreground mt-auto">
          © 2025 The Triangle · Drexel University
        </p>
      </div>
    </div>
  )
}

export default LoginPage
