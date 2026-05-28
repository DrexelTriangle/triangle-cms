import { useState } from "react"
import { Link } from "react-router-dom"
import { Mail, Lock, User, ArrowRight } from "lucide-react"

function SignupPage() {
  const [name, setName] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState("Writer")

  const roles = ["Writer", "Editor", "Photographer", "Section Lead"]

  return (
    <div className="min-h-screen flex">

      {/* Form panel */}
      <div className="flex-1 flex flex-col bg-white px-12 py-8">
        {/* Top bar */}
        <div className="flex items-center justify-between mb-auto">
          <p className="text-xs font-bold text-muted-foreground uppercase tracking-[0.14em]">Create account</p>
          <Link to="/login" className="text-sm text-primary font-semibold hover:underline">
            Already have access? Sign In →
          </Link>
        </div>

        {/* Form content */}
        <div className="flex-1 flex flex-col justify-center max-w-sm mx-auto w-full py-12">
          <h1 className="text-3xl font-bold text-foreground leading-tight mb-1">Request access.</h1>
          <p className="text-sm text-muted-foreground mb-9">
            Fill in your details. An editor will review and approve your account.
          </p>

          <form className="flex flex-col gap-5" onSubmit={(e) => e.preventDefault()}>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-semibold text-foreground" htmlFor="signup-name">
                Full name
              </label>
              <div className="relative">
                <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  id="signup-name"
                  type="text"
                  autoComplete="name"
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary transition"
                  placeholder="Alex Johnson"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-semibold text-foreground" htmlFor="signup-email">
                Email address
              </label>
              <div className="relative">
                <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  id="signup-email"
                  type="email"
                  autoComplete="email"
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary transition"
                  placeholder="you@drexel.edu"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
              <p className="text-[11px] text-muted-foreground">We'll send your confirmation here.</p>
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-semibold text-foreground" htmlFor="signup-password">
                Create password
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
                <input
                  id="signup-password"
                  type="password"
                  autoComplete="new-password"
                  className="w-full pl-10 pr-4 py-2.5 rounded-xl border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary transition"
                  placeholder="At least 8 characters"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <span className="text-xs font-semibold text-foreground">Your role</span>
              <div className="grid grid-cols-2 gap-2">
                {roles.map((r) => (
                  <button
                    key={r}
                    type="button"
                    onClick={() => setRole(r)}
                    className={`py-2.5 rounded-xl text-sm font-medium border transition-all ${
                      role === r
                        ? "bg-primary text-white border-primary shadow-sm"
                        : "bg-background text-foreground border-border hover:border-primary/50 hover:bg-primary/5"
                    }`}
                  >
                    {r}
                  </button>
                ))}
              </div>
            </div>

            <button
              type="submit"
              className="mt-1 w-full py-2.5 rounded-xl bg-primary text-white font-semibold text-sm hover:bg-primary/90 active:scale-[0.98] transition-all flex items-center justify-center gap-2"
            >
              Request access
              <ArrowRight className="w-4 h-4" />
            </button>
          </form>

          <p className="mt-5 text-center text-xs text-muted-foreground">
            By requesting access, you agree to The Triangle's editorial policies.
          </p>
        </div>
      </div>
    </div>
  )
}

export default SignupPage
