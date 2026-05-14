import { useState } from "react"
import { Save, Globe, Mail, Palette, Shield, Bell, Database } from "lucide-react"

type Tab = "general" | "email" | "appearance" | "security" | "notifications" | "advanced"

const TABS: { id: Tab; label: string; icon: React.ElementType }[] = [
  { id: "general", label: "General", icon: Globe },
  { id: "email", label: "Email", icon: Mail },
  { id: "appearance", label: "Appearance", icon: Palette },
  { id: "security", label: "Security", icon: Shield },
  { id: "notifications", label: "Notifications", icon: Bell },
  { id: "advanced", label: "Advanced", icon: Database },
]

function Field({ label, children, hint }: { label: string; children: React.ReactNode; hint?: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">{label}</label>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  )
}

function inputCls() {
  return "w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
}

function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${checked ? "bg-primary" : "bg-muted"}`}
    >
      <span className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${checked ? "translate-x-4.5" : "translate-x-0.5"}`} />
    </button>
  )
}

export default function SettingsPage() {
  const [tab, setTab] = useState<Tab>("general")
  const [saved, setSaved] = useState(false)

  const [siteName, setSiteName] = useState("The Triangle")
  const [tagline, setTagline] = useState("Drexel University's Independent Student Newspaper")
  const [siteUrl, setSiteUrl] = useState("https://thetriangle.org")
  const [timezone, setTimezone] = useState("America/New_York")
  const [dateFormat, setDateFormat] = useState("MMMM D, YYYY")

  const [fromEmail, setFromEmail] = useState("editors@thetriangle.org")
  const [fromName, setFromName] = useState("The Triangle Editors")
  const [smtpHost, setSmtpHost] = useState("smtp.mailgun.org")

  const [allowComments, setAllowComments] = useState(true)
  const [moderateComments, setModerateComments] = useState(true)
  const [allowRegistration, setAllowRegistration] = useState(false)

  const [twoFactor, setTwoFactor] = useState(false)
  const [sessionTimeout, setSessionTimeout] = useState("30")

  const handleSave = () => {
    setSaved(true)
    setTimeout(() => setSaved(false), 2000)
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Manage your CMS configuration</p>
        </div>
        <button
          type="button"
          onClick={handleSave}
          className={`inline-flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-colors ${saved ? "bg-green-600 text-white" : "bg-primary text-primary-foreground hover:bg-primary/90"}`}
        >
          <Save className="w-4 h-4" />
          {saved ? "Saved!" : "Save Changes"}
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[200px_1fr] gap-6 items-start">
        {/* Tab nav */}
        <nav className="flex flex-row lg:flex-col gap-1 overflow-x-auto lg:overflow-visible">
          {TABS.map((t) => {
            const Icon = t.icon
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => setTab(t.id)}
                className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors whitespace-nowrap ${tab === t.id ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}
              >
                <Icon className="w-4 h-4 shrink-0" />
                {t.label}
              </button>
            )
          })}
        </nav>

        {/* Settings panels */}
        <div className="rounded-xl border border-border bg-card p-6 flex flex-col gap-6">
          {tab === "general" && (
            <>
              <h2 className="text-base font-semibold text-foreground">General Settings</h2>
              <Field label="Site Name">
                <input className={inputCls()} value={siteName} onChange={(e) => setSiteName(e.target.value)} />
              </Field>
              <Field label="Tagline" hint="Appears in browser tabs and as the default meta description.">
                <input className={inputCls()} value={tagline} onChange={(e) => setTagline(e.target.value)} />
              </Field>
              <Field label="Site URL">
                <input className={inputCls()} value={siteUrl} onChange={(e) => setSiteUrl(e.target.value)} />
              </Field>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Field label="Timezone">
                  <select className={inputCls()} value={timezone} onChange={(e) => setTimezone(e.target.value)}>
                    <option value="America/New_York">Eastern Time (ET)</option>
                    <option value="America/Chicago">Central Time (CT)</option>
                    <option value="America/Denver">Mountain Time (MT)</option>
                    <option value="America/Los_Angeles">Pacific Time (PT)</option>
                    <option value="UTC">UTC</option>
                  </select>
                </Field>
                <Field label="Date Format">
                  <select className={inputCls()} value={dateFormat} onChange={(e) => setDateFormat(e.target.value)}>
                    <option value="MMMM D, YYYY">April 28, 2025</option>
                    <option value="MM/DD/YYYY">04/28/2025</option>
                    <option value="YYYY-MM-DD">2025-04-28</option>
                    <option value="D MMMM YYYY">28 April 2025</option>
                  </select>
                </Field>
              </div>
              <div className="flex flex-col gap-3 pt-2 border-t border-border">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Allow Comments</p>
                    <p className="text-xs text-muted-foreground">Enable reader comments on articles</p>
                  </div>
                  <Toggle checked={allowComments} onChange={setAllowComments} />
                </div>
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Moderate Comments</p>
                    <p className="text-xs text-muted-foreground">Require approval before comments appear</p>
                  </div>
                  <Toggle checked={moderateComments} onChange={setModerateComments} />
                </div>
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Open Registration</p>
                    <p className="text-xs text-muted-foreground">Allow new users to request access</p>
                  </div>
                  <Toggle checked={allowRegistration} onChange={setAllowRegistration} />
                </div>
              </div>
            </>
          )}

          {tab === "email" && (
            <>
              <h2 className="text-base font-semibold text-foreground">Email Settings</h2>
              <Field label="From Name">
                <input className={inputCls()} value={fromName} onChange={(e) => setFromName(e.target.value)} />
              </Field>
              <Field label="From Email">
                <input className={inputCls()} type="email" value={fromEmail} onChange={(e) => setFromEmail(e.target.value)} />
              </Field>
              <Field label="SMTP Host">
                <input className={inputCls()} value={smtpHost} onChange={(e) => setSmtpHost(e.target.value)} />
              </Field>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Field label="SMTP Port">
                  <input className={inputCls()} defaultValue="587" />
                </Field>
                <Field label="Encryption">
                  <select className={inputCls()}>
                    <option>TLS</option>
                    <option>SSL</option>
                    <option>None</option>
                  </select>
                </Field>
              </div>
              <Field label="SMTP Username">
                <input className={inputCls()} defaultValue="postmaster@thetriangle.org" />
              </Field>
              <Field label="SMTP Password">
                <input className={inputCls()} type="password" defaultValue="••••••••••••" />
              </Field>
              <button type="button" className="self-start px-4 py-2 rounded-lg border border-border bg-background text-sm font-medium text-foreground hover:bg-muted transition-colors">
                Send Test Email
              </button>
            </>
          )}

          {tab === "appearance" && (
            <>
              <h2 className="text-base font-semibold text-foreground">Appearance</h2>
              <Field label="Theme">
                <select className={inputCls()}>
                  <option>System Default</option>
                  <option>Light</option>
                  <option>Dark</option>
                </select>
              </Field>
              <Field label="Primary Color" hint="Used for buttons, links, and active states throughout the CMS.">
                <div className="flex items-center gap-3">
                  <input type="color" className="w-10 h-10 rounded-lg border border-border cursor-pointer" defaultValue="#e60000" />
                  <input className={inputCls()} defaultValue="#e60000" />
                </div>
              </Field>
              <Field label="Logo">
                <div className="flex items-center gap-3">
                  <div className="w-12 h-12 rounded-lg border border-border bg-muted flex items-center justify-center text-xl font-bold">T</div>
                  <button type="button" className="px-3 py-2 rounded-lg border border-border bg-background text-sm font-medium text-muted-foreground hover:bg-muted transition-colors">
                    Upload New Logo
                  </button>
                </div>
              </Field>
            </>
          )}

          {tab === "security" && (
            <>
              <h2 className="text-base font-semibold text-foreground">Security</h2>
              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-foreground">Two-Factor Authentication</p>
                    <p className="text-xs text-muted-foreground">Require 2FA for all admin accounts</p>
                  </div>
                  <Toggle checked={twoFactor} onChange={setTwoFactor} />
                </div>
              </div>
              <Field label="Session Timeout (minutes)" hint="Users are logged out after this period of inactivity.">
                <input className={inputCls()} type="number" value={sessionTimeout} onChange={(e) => setSessionTimeout(e.target.value)} min="5" max="480" />
              </Field>
              <Field label="Allowed Login IPs" hint="Leave blank to allow any IP. Enter one IP or CIDR per line.">
                <textarea className={`${inputCls()} resize-none font-mono`} rows={4} placeholder="e.g. 143.215.0.0/16" />
              </Field>
            </>
          )}

          {tab === "notifications" && (
            <>
              <h2 className="text-base font-semibold text-foreground">Notifications</h2>
              {[
                { label: "New comment submitted", desc: "Notify editors when a new comment needs moderation" },
                { label: "Article published", desc: "Send email when an article goes live" },
                { label: "New user registration", desc: "Notify admins when someone requests access" },
                { label: "Contact form message", desc: "Notify editors when a contact message arrives" },
                { label: "Weekly activity digest", desc: "Send a weekly summary of CMS activity" },
              ].map((item, i) => (
                <div key={i} className="flex items-center justify-between py-2 border-b border-border last:border-0">
                  <div>
                    <p className="text-sm font-medium text-foreground">{item.label}</p>
                    <p className="text-xs text-muted-foreground">{item.desc}</p>
                  </div>
                  <Toggle checked={i < 3} onChange={() => {}} />
                </div>
              ))}
            </>
          )}

          {tab === "advanced" && (
            <>
              <h2 className="text-base font-semibold text-foreground">Advanced</h2>
              <Field label="Cache TTL (seconds)" hint="How long to cache article list queries.">
                <input className={inputCls()} type="number" defaultValue="300" />
              </Field>
              <Field label="Max Upload Size (MB)">
                <input className={inputCls()} type="number" defaultValue="64" />
              </Field>
              <Field label="API Rate Limit (req/min)">
                <input className={inputCls()} type="number" defaultValue="120" />
              </Field>
              <div className="pt-4 border-t border-border flex flex-col gap-3">
                <p className="text-sm font-semibold text-foreground">Danger Zone</p>
                <div className="flex flex-wrap gap-3">
                  <button type="button" className="px-4 py-2 rounded-lg border border-yellow-400 text-yellow-600 dark:text-yellow-400 text-sm font-medium hover:bg-yellow-50 dark:hover:bg-yellow-900/20 transition-colors">
                    Clear All Caches
                  </button>
                  <button type="button" className="px-4 py-2 rounded-lg border border-destructive text-destructive text-sm font-medium hover:bg-destructive/10 transition-colors">
                    Reset to Defaults
                  </button>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
