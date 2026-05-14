import { useState } from "react"
import { Link } from "react-router-dom"
import { Mail, Lock, ArrowRight } from "lucide-react"

function LoginPage() {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")

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

        {/* Form content */}
        <div className="flex-1 flex flex-col justify-center max-w-sm mx-auto w-full py-16">
          <p className="text-xs font-bold text-primary uppercase tracking-[0.18em] mb-3">Welcome back</p>
          <h1 className="text-3xl font-bold text-foreground leading-tight mb-2">
            Sign in to your account.
          </h1>
          <p className="text-sm text-muted-foreground mb-10">
            The Triangle's editorial platform. Publish, manage, and grow.
          </p>

          <form className="flex flex-col gap-5" onSubmit={(e) => e.preventDefault()}>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-semibold text-foreground" htmlFor="login-email">
                Email address
              </label>
              <div className="relative">
                <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  id="login-email"
                  type="email"
                  autoComplete="email"
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary transition"
                  placeholder="you@thetriangle.org"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <label className="text-xs font-semibold text-foreground" htmlFor="login-password">
                  Password
                </label>
                <a className="text-xs text-primary hover:underline" href="#">
                  Forgot password?
                </a>
              </div>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  id="login-password"
                  type="password"
                  autoComplete="current-password"
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary transition"
                  placeholder="••••••••"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </div>
            </div>

            <button
              type="submit"
              className="mt-1 w-full py-2.5 rounded-xl bg-primary text-white font-semibold text-sm hover:bg-primary/90 active:scale-[0.98] transition-all flex items-center justify-center gap-2"
            >
              Sign In
              <ArrowRight className="w-4 h-4" />
            </button>
          </form>

          <p className="mt-6 text-sm text-muted-foreground text-center">
            New to the team?{" "}
            <Link to="/signup" className="text-primary font-semibold hover:underline">
              Request access →
            </Link>
          </p>
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
