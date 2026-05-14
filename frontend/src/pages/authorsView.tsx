import { useState } from "react"
import { Search, Plus, Pencil, Trash2 } from "lucide-react"

const AUTHORS = [
  { id: 1, name: "Zarina Morgan", slug: "zarina-morgan", articles: 47, email: "zmorgan@thetriangle.org" },
  { id: 2, name: "Sanjana Bandi", slug: "sanjana-bandi", articles: 31, email: "sbandi@thetriangle.org" },
  { id: 3, name: "Nina Feinberg", slug: "nina-feinberg", articles: 28, email: "nfeinberg@thetriangle.org" },
  { id: 4, name: "Paulie Loscalzo", slug: "paulie-loscalzo", articles: 52, email: "ploscalzo@thetriangle.org" },
  { id: 5, name: "Ava Buckingham", slug: "ava-buckingham", articles: 19, email: "abuckingham@thetriangle.org" },
  { id: 6, name: "Coco Li", slug: "coco-li", articles: 23, email: "cli@thetriangle.org" },
  { id: 7, name: "Erik Heyman-Meltzer", slug: "erik-heyman-meltzer", articles: 41, email: "eheyman@thetriangle.org" },
  { id: 8, name: "Sophie Fang", slug: "sophie-fang", articles: 14, email: "sfang@thetriangle.org" },
  { id: 9, name: "Marcus Chen", slug: "marcus-chen", articles: 36, email: "mchen@thetriangle.org" },
  { id: 10, name: "Jenna Pedorenko", slug: "jenna-pedorenko", articles: 22, email: "jpedorenko@thetriangle.org" },
]

function initials(name: string) {
  return name.split(" ").map((n) => n[0]).join("").slice(0, 2).toUpperCase()
}

const AVATAR_COLORS = [
  "bg-blue-500", "bg-violet-500", "bg-green-500", "bg-orange-500",
  "bg-rose-500", "bg-teal-500", "bg-indigo-500", "bg-amber-500",
  "bg-cyan-500", "bg-pink-500",
]

function AuthorsView() {
  const [search, setSearch] = useState("")

  const filtered = AUTHORS.filter((a) =>
    a.name.toLowerCase().includes(search.toLowerCase()) ||
    a.email.toLowerCase().includes(search.toLowerCase())
  )

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Authors</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{AUTHORS.length} authors total</p>
        </div>
        <button className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors" type="button">
          <Plus className="w-4 h-4" />
          Add New
        </button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" />
        <input
          className="w-full pl-9 pr-4 py-2 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/40 focus:border-primary transition"
          placeholder="Search authors..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-muted/40">
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground w-10" scope="col" />
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Name</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Slug</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Articles</th>
              <th className="text-left px-4 py-3 font-semibold text-muted-foreground" scope="col">Email</th>
              <th className="text-right px-4 py-3 font-semibold text-muted-foreground" scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length === 0 ? (
              <tr>
                <td className="px-4 py-8 text-center text-muted-foreground" colSpan={6}>
                  No authors found for "{search}"
                </td>
              </tr>
            ) : (
              filtered.map((author, i) => (
                <tr key={author.id} className="border-b border-border last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    <div className={`w-8 h-8 rounded-full ${AVATAR_COLORS[i % AVATAR_COLORS.length]} flex items-center justify-center text-white text-xs font-bold`}>
                      {initials(author.name)}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-medium text-foreground">{author.name}</td>
                  <td className="px-4 py-3 text-muted-foreground font-mono text-xs">{author.slug}</td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary">
                      {author.articles}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{author.email}</td>
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

export default AuthorsView
