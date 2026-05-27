import { useAuth } from "react-oidc-context"
import { LogOut } from "lucide-react"

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{label}</label>
      <div className="text-sm text-foreground">{children}</div>
    </div>
  )
}

export default function SettingsPage() {
  const { user, signoutRedirect } = useAuth()
  const profile = user?.profile
  const name = profile?.name ?? "—"
  const email = profile?.email ?? "—"
  const username = profile?.preferred_username ?? "—"
  const groups = (profile?.groups as string[] | undefined) ?? []
  const initials = name.split(" ").map((n: string) => n[0]).join("").slice(0, 2).toUpperCase()

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Your account information</p>
        </div>
        <button
          type="button"
          onClick={() => signoutRedirect()}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border border-destructive/40 text-destructive text-sm font-medium hover:bg-destructive/10 transition-colors"
        >
          <LogOut className="w-4 h-4" />
          Sign out
        </button>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-6">
        <div className="flex items-center gap-4">
          <div className="w-16 h-16 rounded-full bg-primary flex items-center justify-center text-white text-xl font-bold shrink-0">
            {initials}
          </div>
          <div>
            <p className="text-base font-semibold text-foreground">{name}</p>
            <p className="text-sm text-muted-foreground">{email}</p>
          </div>
        </div>

        <div className="border-t border-border pt-6 grid grid-cols-1 md:grid-cols-2 gap-6">
          <Field label="Full Name">{name}</Field>
          <Field label="Username">{username}</Field>
          <Field label="Email">{email}</Field>
          <Field label="Groups">
            {groups.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {groups.map((g) => (
                  <span key={g} className="px-2.5 py-0.5 rounded-full bg-primary/10 text-primary text-xs font-medium">
                    {g}
                  </span>
                ))}
              </div>
            ) : "—"}
          </Field>
        </div>
      </div>
    </div>
  )
}
