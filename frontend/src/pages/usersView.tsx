import { useEffect, useState } from "react"
import { Search, ShieldCheck, Shield } from "lucide-react"
import { useApiFetch } from "../hooks/useApiFetch"

const ROLES = ["admin", "editor"] as const
type Role = typeof ROLES[number]

type CmsUser = {
  id: number
  email: string
  name: string
  role: Role
  created_at: string
}

const ROLE_STYLES: Record<Role, string> = {
  admin: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
  editor: "bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400",
}

function RoleIcon({ role }: { role: Role }) {
  if (role === "admin") return <ShieldCheck className="w-3 h-3" />
  return <Shield className="w-3 h-3" />
}

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

const AVATAR_COLORS = [
  "bg-blue-500", "bg-violet-500", "bg-green-500", "bg-orange-500",
  "bg-rose-500", "bg-teal-500", "bg-indigo-500", "bg-amber-500",
  "bg-cyan-500", "bg-pink-500", "bg-lime-500", "bg-fuchsia-500", "bg-sky-500",
]

export default function UsersView() {
  const apiFetch = useApiFetch()
  const [users, setUsers] = useState<CmsUser[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [savingUserId, setSavingUserId] = useState<number | null>(null)
  const [search, setSearch] = useState("")
  const [roleFilter, setRoleFilter] = useState<Role | "all">("all")

  useEffect(() => {
    setIsLoading(true)
    apiFetch("/v1/users")
      .then((r) => {
        if (!r.ok) throw new Error(`Request failed (${r.status})`)
        return r.json() as Promise<CmsUser[]>
      })
      .then((data) => {
        setUsers(data)
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load users"))
      .finally(() => setIsLoading(false))
  }, [apiFetch])

  const filtered = users.filter((u) => {
    const matchSearch = u.name.toLowerCase().includes(search.toLowerCase()) || u.email.toLowerCase().includes(search.toLowerCase())
    const matchRole = roleFilter === "all" || u.role === roleFilter
    return matchSearch && matchRole
  })

  async function updateRole(userId: number, role: Role) {
    setSavingUserId(userId)
    try {
      const res = await apiFetch(`/v1/users/${userId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role }),
      })
      if (!res.ok) throw new Error(`Request failed (${res.status})`)
      setUsers((prev) => prev.map((u) => (u.id === userId ? { ...u, role } : u)))
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update role")
    } finally {
      setSavingUserId(null)
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Users</h1>
          <p className="text-sm text-muted-foreground mt-0.5">
            {isLoading ? "Loading…" : `${users.length} users total`}
          </p>
        </div>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
      </div>

      <div className="flex gap-3 flex-wrap">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
          <input
            className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
            placeholder="Search users..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="flex items-center gap-1 flex-wrap">
          {(["all", ...ROLES] as const).map((r) => (
            <button
              key={r}
              type="button"
              onClick={() => setRoleFilter(r)}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${roleFilter === r ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/80"}`}
            >
              {r === "all" ? "All" : r.charAt(0).toUpperCase() + r.slice(1)}
            </button>
          ))}
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground w-10" scope="col" />
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Email</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Role</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Joined</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>Loading users…</td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>No users found.</td>
              </tr>
            ) : (
              filtered.map((user, i) => (
                <tr key={user.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <div className={`w-8 h-8 rounded-full ${AVATAR_COLORS[i % AVATAR_COLORS.length]} flex items-center justify-center text-white text-xs font-bold`}>
                      {initials(user.name)}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-medium text-foreground">{user.name}</td>
                  <td className="px-4 py-3 text-muted-foreground">{user.email}</td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${ROLE_STYLES[user.role]}`}>
                      <RoleIcon role={user.role} />
                      {user.role.charAt(0).toUpperCase() + user.role.slice(1)}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden lg:table-cell">{new Date(user.created_at).toLocaleDateString()}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end">
                      <select
                        value={user.role}
                        disabled={savingUserId === user.id}
                        onChange={(e) => updateRole(user.id, e.target.value as Role)}
                        className="px-2 py-1 rounded border border-border bg-background text-xs"
                      >
                        <option value="editor">Editor</option>
                        <option value="admin">Admin</option>
                      </select>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
