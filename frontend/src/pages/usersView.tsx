import { useState } from "react"
import { Search, Plus, Pencil, Trash2, ShieldCheck, Shield, User } from "lucide-react"

const ROLES = ["Admin", "Editor", "Author", "Contributor", "Subscriber"] as const
type Role = typeof ROLES[number]

const USERS = [
  { id: 1, name: "Juste Mugisha", email: "jmugisha@thetriangle.org", role: "Admin" as Role, joined: "2023-09-01", articles: 0 },
  { id: 2, name: "Sarah Goldstein", email: "sgoldstein@thetriangle.org", role: "Editor" as Role, joined: "2023-09-05", articles: 0 },
  { id: 3, name: "Paulie Loscalzo", email: "ploscalzo@thetriangle.org", role: "Author" as Role, joined: "2023-10-12", articles: 52 },
  { id: 4, name: "Zarina Morgan", email: "zmorgan@thetriangle.org", role: "Author" as Role, joined: "2024-01-15", articles: 47 },
  { id: 5, name: "Erik Heyman-Meltzer", email: "eheyman@thetriangle.org", role: "Author" as Role, joined: "2024-01-20", articles: 41 },
  { id: 6, name: "Marcus Chen", email: "mchen@thetriangle.org", role: "Author" as Role, joined: "2024-02-03", articles: 36 },
  { id: 7, name: "Sanjana Bandi", email: "sbandi@thetriangle.org", role: "Author" as Role, joined: "2024-02-14", articles: 31 },
  { id: 8, name: "Nina Feinberg", email: "nfeinberg@thetriangle.org", role: "Author" as Role, joined: "2024-03-01", articles: 28 },
  { id: 9, name: "Coco Li", email: "cli@thetriangle.org", role: "Contributor" as Role, joined: "2024-03-10", articles: 23 },
  { id: 10, name: "Jenna Pedorenko", email: "jpedorenko@thetriangle.org", role: "Contributor" as Role, joined: "2024-03-22", articles: 22 },
  { id: 11, name: "Ava Buckingham", email: "abuckingham@thetriangle.org", role: "Author" as Role, joined: "2024-04-05", articles: 19 },
  { id: 12, name: "Sophie Fang", email: "sfang@thetriangle.org", role: "Contributor" as Role, joined: "2024-04-18", articles: 14 },
  { id: 13, name: "Alex Rivera", email: "arivera@thetriangle.org", role: "Subscriber" as Role, joined: "2025-01-10", articles: 0 },
]

const ROLE_STYLES: Record<Role, string> = {
  Admin: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
  Editor: "bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400",
  Author: "bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400",
  Contributor: "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400",
  Subscriber: "bg-muted text-muted-foreground",
}

function RoleIcon({ role }: { role: Role }) {
  if (role === "Admin") return <ShieldCheck className="w-3 h-3" />
  if (role === "Editor") return <Shield className="w-3 h-3" />
  return <User className="w-3 h-3" />
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
  const [search, setSearch] = useState("")
  const [roleFilter, setRoleFilter] = useState<Role | "All">("All")

  const filtered = USERS.filter((u) => {
    const matchSearch = u.name.toLowerCase().includes(search.toLowerCase()) || u.email.toLowerCase().includes(search.toLowerCase())
    const matchRole = roleFilter === "All" || u.role === roleFilter
    return matchSearch && matchRole
  })

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Users</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{USERS.length} users total</p>
        </div>
        <button className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
          <Plus className="w-4 h-4" />
          Add New User
        </button>
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
          {(["All", ...ROLES] as const).map((r) => (
            <button
              key={r}
              type="button"
              onClick={() => setRoleFilter(r)}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${roleFilter === r ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/80"}`}
            >
              {r}
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
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden md:table-cell" scope="col">Articles</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground hidden lg:table-cell" scope="col">Joined</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={7}>No users found.</td>
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
                      {user.role}
                    </span>
                  </td>
                  <td className="px-4 py-3 hidden md:table-cell">
                    {user.articles > 0 ? (
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">{user.articles}</span>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground hidden lg:table-cell">{user.joined}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-primary hover:bg-primary/10 transition-colors" type="button" title="Edit">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors" type="button" title="Delete">
                        <Trash2 className="w-4 h-4" />
                      </button>
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
